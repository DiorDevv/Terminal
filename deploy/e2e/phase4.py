#!/usr/bin/env python3
"""Phase 4 end-to-end: access rules, the policy tester and block lists, against
a real squid under systemd with the panel installed by deploy/install.sh.

Run on the host as root:

    E2E_ADMIN_PW='<admin password>' python3 deploy/e2e/phase4.py

The centrepiece is an ORACLE test: for a matrix of requests (different source
addresses, users, methods, ports, User-Agents, URLs) the panel's policy tester
predicts allow / deny / login-required, and the same request is then sent
through the real squid. The two must agree. Everything the script changes is
undone at the end and squid.conf is asserted byte-for-byte unchanged.
"""
import datetime, http.cookiejar, json, os, subprocess, sys, time, urllib.error, urllib.request

API = "http://localhost:8080/api"
ADMIN_PW = os.environ["E2E_ADMIN_PW"]
TMP_PW = "e2e-temporary-Pass-42"
fails = []


class Client:
    def __init__(self):
        self.jar = http.cookiejar.CookieJar()
        self.op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))

    def call(self, method, path, body=None):
        req = urllib.request.Request(API + path, method=method)
        req.add_header("Content-Type", "application/json")
        req.add_header("Origin", "http://localhost:5173")
        req.add_header("X-Requested-With", "squidadmin")
        data = json.dumps(body).encode() if body is not None else None
        try:
            with self.op.open(req, data, timeout=200) as r:
                return r.status, json.loads(r.read() or b"{}")
        except urllib.error.HTTPError as e:
            raw = e.read()
            try:
                return e.code, json.loads(raw or b"{}")
            except ValueError:
                return e.code, {"raw": raw.decode(errors="replace")}


def check(name, cond, detail=""):
    print(("PASS  " if cond else "FAIL  ") + name + (f"   [{detail}]" if detail and not cond else ""))
    if not cond:
        fails.append(name)


def sh(cmd):
    return subprocess.run(cmd, shell=True, capture_output=True, text=True).stdout.strip()


def conf():
    return open("/etc/squid/squid.conf").read()


def squid_up():
    return sh("systemctl is-active squid") in ("active", "deactivating")


def parse_ok():
    return subprocess.run(["squid", "-k", "parse", "-f", "/etc/squid/squid.conf"], capture_output=True).returncode == 0


WSL_IP = sh("hostname -I").split()[0]
SOURCES = {"local": "127.0.0.1", "lan": WSL_IP}
PASSWORDS = {"alice": "alicepass1", "bob": "bobpass22"}


def actual(src, url, method="GET", user=None, ua=None):
    """What real squid does with the request: allow / deny / auth_required."""
    proxy = f"http://{SOURCES[src]}:3128"
    extra = ""
    if user:
        extra += f" --proxy-user {user}:{PASSWORDS[user]}"
    if ua:
        extra += f" -A '{ua}'"
    if method == "CONNECT":
        code = sh(f"curl -s -m 15 -o /dev/null -p -x {proxy}{extra} -w '%{{http_connect}}' {url}")
    else:
        body = " -d x" if method == "POST" else ""
        code = sh(f"curl -s -m 15 -o /dev/null -x {proxy}{extra} -X {method}{body} -w '%{{http_code}}' {url}")
    if code == "403":
        return "deny", code
    if code == "407":
        return "auth_required", code
    return "allow", code


def predicted(cl, src, url, method="GET", user=None, ua=None):
    body = {"src_ip": SOURCES[src], "url": url, "method": method}
    if user:
        body["user"] = user
    if ua:
        body["user_agent"] = ua
    s, v = cl.call("POST", "/squid/access/test", body)
    assert s == 200, (s, v)
    return v


def wait_for(pred, timeout=10, step=0.2):
    t0 = time.time()
    while time.time() - t0 < timeout:
        v = pred()
        if v:
            return v
        time.sleep(step)
    return False


admin = Client()
must_reset = False
s, r = admin.call("POST", "/auth/login", {"username": "admin", "password": ADMIN_PW})
assert s == 200, f"cannot log in as admin: {s} {r}"
if r["user"]["must_change_password"]:
    must_reset = True
    s, r = admin.call("PUT", "/auth/password", {"old_password": ADMIN_PW, "new_password": TMP_PW})
    assert s == 200, (s, r)

    import atexit

    def restore_admin():
        """Put the admin back to its original password even if the run crashes."""
        try:
            me = admin.call("GET", "/auth/me")[1]["user"]
            admin.call("POST", f"/panel/users/{me['id']}/password", {"password": ADMIN_PW})
        except Exception as e:
            print("could not restore the admin password:", e)

    atexit.register(restore_admin)

created_users, created_rules, created_acls, created_sources, created_restr = [], [], [], [], []

E2E_ACLS = {"keyword", "social", "media", "vip", "lanhost", "posting", "agent", "adsrx", "badport", "nowwin",
            "offwin", "tiktok", "snap", "allowed_sites", "rx_probe", "todayonly", "othersonly", "edgewin", "dom_today", "dom_other", "dom_edge", "bl_e2e-list", "bl_e2e-ssrf1", "bl_e2e-ssrf2"}
E2E_COMMENTS = {"no gambling", "social media blocked except from the LAN host", "no uploads to the media site",
                "vip may stream", "everyone else may not", "block a scraper", "ad servers", "a forbidden port",
                "blocked during the current window", "would block, but only later", "whitelist mode", "time: today only", "time: other days", "time: edge window",
                "block list: e2e-list", "block list: e2e-ssrf1", "block list: e2e-ssrf2"}


def cleanup():
    """Remove everything this script creates. Safe to run repeatedly, and only
    touches objects with this script's names, never anything else."""
    try:
        _, lst = admin.call("GET", "/squid/blocklists")
        for x in lst.get("sources", []):
            if x["name"].startswith("e2e-"):
                admin.call("DELETE", f"/squid/blocklists/{x['id']}?remove_rules=1")
        _, rl = admin.call("GET", "/squid/access/rules")
        for x in rl.get("rules", []):
            if x["comment"] in E2E_COMMENTS:
                admin.call("DELETE", f"/squid/access/rules/{x['id']}")
        _, al = admin.call("GET", "/squid/access/acls")
        for x in al.get("acls", []):
            if x["name"] in E2E_ACLS:
                admin.call("DELETE", f"/squid/access/acls/{x['id']}")
        for u in PASSWORDS:
            admin.call("DELETE", f"/squid/users/{u}")
        _, rs = admin.call("GET", "/squid/restrictions")
        for x in rs.get("restrictions", []):
            if x["name"].startswith("e2e_"):
                admin.call("DELETE", f"/squid/restrictions/{x['id']}")
    except Exception as e:  # never let cleanup hide the real failure
        print("cleanup problem:", e)


import atexit
cleanup()
ORIG = conf()          # the baseline is taken after any leftovers are gone
atexit.register(cleanup)

# ------------------------------------------------------------------ the policy view
print("== effective policy of the real squid.conf")
s, pol = admin.call("GET", "/squid/access/policy")
active = [l for l in ORIG.splitlines() if l.strip().startswith("http_access ")]
check("policy lists every http_access line, in order", s == 200 and len(pol["rules"]) >= len(active), f"{len(pol['rules'])} vs {len(active)}")
srcs = [x["source"] for x in pol["rules"]]
check("rules are attributed to their owners", {"stock", "blacklist", "proxy_auth"} <= set(srcs), str(set(srcs)))
check("the last rule is 'deny all'", pol["rules"][-1]["raw"] == "http_access deny all")
check("built-in ACLs are understood", all(k in pol["acls"] for k in ("localhost", "manager", "all", "to_localhost")))

# ------------------------------------------------------------- set up the fixtures
print("== create proxy users, ACLs and rules through the API")
for u, p in PASSWORDS.items():
    s, r = admin.call("POST", "/squid/users", {"username": u, "password": p})
    check(f"proxy user {u}", s == 200, str(r))
    created_users.append(u)

now = datetime.datetime.now()
m = now.hour * 60 + now.minute
fmt = lambda x: f"{x // 60:02d}:{x % 60:02d}"
win_now = f"SMTWHFA {fmt(max(0, m - 30))}-{fmt(min(1439, m + 30))}"
win_off = f"SMTWHFA {fmt(m + 60)}-{fmt(m + 120)}" if m + 120 < 1439 else f"SMTWHFA {fmt(max(0, m - 120))}-{fmt(max(1, m - 60))}"
print("    time windows: now ->", win_now, "| not now ->", win_off)

acl_defs = {
    "keyword": ("url_regex", ["casino"]),
    "social":  ("dstdomain", ["social-e2e.example"]),
    "media":   ("dstdomain", ["media-e2e.example"]),
    "vip":     ("proxy_auth", ["alice"]),
    "lanhost": ("src", [WSL_IP]),
    "posting": ("method", ["POST"]),
    "agent":   ("browser", ["E2EBot"]),
    "adsrx":   ("dstdom_regex", ["^ads[0-9]+[.]"]),
    "badport": ("port", ["8888"]),
    "nowwin":  ("time", [win_now]),
    "offwin":  ("time", [win_off]),
    "tiktok":  ("dstdomain", ["tiktok-e2e.example"]),
    "snap":    ("dstdomain", ["snap-e2e.example"]),
}
ids = {}
for name, (typ, vals) in acl_defs.items():
    s, r = admin.call("POST", "/squid/access/acls", {"name": name, "type": typ, "values": vals})
    if s != 200:
        check(f"ACL {name}", False, str(r))
        continue
    ids[name] = r["acl"]["id"]
    created_acls.append(ids[name])
check("all 13 ACLs created", len(ids) == len(acl_defs))


def rule(action, terms, comment=""):
    body = {"action": action, "comment": comment, "terms": [{"acl_id": ids[t.lstrip('!')], "negate": t.startswith('!')} for t in terms]}
    s, r = admin.call("POST", "/squid/access/rules", body)
    if s == 200:
        created_rules.append(r["rule"]["id"])
    return s, r


rule_specs = [
    ("deny", ["keyword"], "no gambling"),
    ("deny", ["social", "!lanhost"], "social media blocked except from the LAN host"),
    ("deny", ["posting", "media"], "no uploads to the media site"),
    ("allow", ["media", "vip"], "vip may stream"),
    ("deny", ["media"], "everyone else may not"),
    ("deny", ["agent"], "block a scraper"),
    ("deny", ["adsrx"], "ad servers"),
    ("deny", ["badport"], "a forbidden port"),
    ("deny", ["nowwin", "tiktok"], "blocked during the current window"),
    ("deny", ["offwin", "snap"], "would block, but only later"),
]
for action, terms, comment in rule_specs:
    s, r = rule(action, terms, comment)
    check(f"rule: {comment}", s == 200 and r.get("reloaded") is True, str(r))

c = conf()
check("squid parses the generated configuration", parse_ok())
check("squid survived", squid_up())
check("the block sits before the stock allow rules", c.index("access rules (managed") < c.index("http_access allow localhost"))
check("proxy_auth condition was moved last in its rule", "http_access allow sqa_media sqa_vip" in c, "expected 'allow sqa_media sqa_vip'")

# ------------------------------------------------------------------ the oracle
print("== ORACLE: the panel's prediction vs what real squid does")
M = [  # name, src, url, method, user, ua
    ("plain site from localhost", "local", "http://oracle-plain.example/", "GET", None, None),
    ("gambling keyword (case-insensitive)", "local", "http://oracle-shop.example/CaSiNo-bonus", "GET", None, None),
    ("social media from localhost", "local", "http://social-e2e.example/", "GET", None, None),
    ("social media from the exempt LAN host", "lan", "http://social-e2e.example/", "GET", "alice", None),
    ("media GET, ordinary user (rule 5)", "local", "http://media-e2e.example/", "GET", None, None),
    ("media POST (rule 3)", "local", "http://media-e2e.example/", "POST", None, None),
    ("media as vip from the LAN", "lan", "http://media-e2e.example/", "GET", "alice", None),
    ("media as non-vip user from the LAN", "lan", "http://media-e2e.example/", "GET", "bob", None),
    ("media from the LAN, no login", "lan", "http://media-e2e.example/", "GET", None, None),
    ("blocked User-Agent", "local", "http://oracle-plain.example/", "GET", None, "E2EBot/1.0"),
    ("other User-Agent", "local", "http://oracle-plain.example/", "GET", None, "Mozilla/5.0"),
    ("ad-server regex on the host", "local", "http://ads42.oracle.example/", "GET", None, None),
    ("host that only looks like an ad server", "local", "http://news.oracle.example/", "GET", None, None),
    ("forbidden port (also outside Safe_ports? no: 8888 is safe)", "local", "http://oracle-plain.example:8888/", "GET", None, None),
    ("unsafe port, stock rule", "local", "http://oracle-plain.example:25/", "GET", None, None),
    ("tiktok while the window is active", "local", "http://tiktok-e2e.example/", "GET", None, None),
    ("snap outside its window", "local", "http://snap-e2e.example/", "GET", None, None),
    ("CONNECT to a normal https site", "local", "https://oracle-plain.example/", "CONNECT", None, None),
    ("CONNECT to the social media site", "local", "https://social-e2e.example/", "CONNECT", None, None),
    ("CONNECT to a non-SSL port (stock)", "local", "https://oracle-plain.example:8443/", "CONNECT", None, None),
    ("LAN client, no login, plain site", "lan", "http://oracle-plain.example/", "GET", None, None),
    ("LAN client with login, plain site", "lan", "http://oracle-plain.example/", "GET", "bob", None),
    ("LAN client with login, blocked keyword", "lan", "http://oracle-shop.example/casino", "GET", "bob", None),
    ("local client, blacklisted stock path", "local", "http://oracle-plain.example/robots.txt", "GET", None, None),
]
agree, disagree, unknown = 0, [], []
for name, src, url, method, user, ua in M:
    v = predicted(admin, src, url, method, user, ua)
    real, code = actual(src, url, method, user, ua)
    if v["decision"] == "unknown":
        unknown.append(name)
    if v["decision"] == real:
        agree += 1
        why = ""
        if v.get("rule_index"):
            why = f" (rule #{v['rule_index']})"
        print(f"    ok   {name}: {real}{why}")
    else:
        disagree.append((name, v["decision"], real, code, v["rule_index"]))
        print(f"    DIFF {name}: panel says {v['decision']} (rule #{v['rule_index']}), squid did {real} (HTTP {code})")
check(f"the panel agrees with real squid on all {len(M)} requests", not disagree, str(disagree))
check("the panel never had to answer 'unknown' here", not unknown, str(unknown))

# ------------------------------------------- time ACL semantics vs real squid
print("== time ACLs: weekday letters and window edges, checked against real squid")
LETTERS = "MTWHFAS"  # python weekday(): Mon=0 ... Sun=6  ->  squid: M T W H F A S
today_letter = LETTERS[datetime.date.today().weekday()]
other_letters = "".join(c for c in "SMTWHFA" if c != today_letter)
for name, typ, vals in (("todayonly", "time", [f"{today_letter} 00:00-23:59"]), ("othersonly", "time", [f"{other_letters} 00:00-23:59"]),
                        ("dom_today", "dstdomain", ["day-today-e2e.example"]), ("dom_other", "dstdomain", ["day-other-e2e.example"])):
    s_, r_ = admin.call("POST", "/squid/access/acls", {"name": name, "type": typ, "values": vals})
    ids[name] = r_["acl"]["id"] if s_ == 200 else 0
s_, _ = rule("deny", ["todayonly", "dom_today"], "time: today only")
s2_, _ = rule("deny", ["othersonly", "dom_other"], "time: other days")
check(f"day-letter rules created (today is {today_letter})", s_ == 200 and s2_ == 200)
d1 = wait_for(lambda: actual("local", "http://day-today-e2e.example/")[0] == "deny", 8)
check(f"a window for today's letter '{today_letter}' matches today on real squid", d1)
check("a window for every OTHER letter does not match today on real squid",
      actual("local", "http://day-other-e2e.example/")[0] == "allow")
for host in ("day-today-e2e.example", "day-other-e2e.example"):
    check(f"the tester agrees for {host}", predicted(admin, "local", f"http://{host}/")["decision"] == actual("local", f"http://{host}/")[0])

# Window edges. Take a window [m, m+1] (both minutes inclusive if squid and the
# panel agree) and probe during minute m, m+1 and m+2.
def minute_now():
    n = datetime.datetime.now()
    return n.hour * 60 + n.minute


def wait_for_minute(target):
    while minute_now() < target:
        time.sleep(1)
    time.sleep(2)


if datetime.datetime.now().second > 45:          # avoid a rollover between creating the rule and the first probe
    time.sleep(16)
m0 = minute_now()
if m0 + 2 < 1439:
    s_, r_ = admin.call("POST", "/squid/access/acls", {"name": "edgewin", "type": "time", "values": [f"SMTWHFA {fmt(m0)}-{fmt(m0 + 1)}"]})
    ids["edgewin"] = r_["acl"]["id"]
    s_, r_ = admin.call("POST", "/squid/access/acls", {"name": "dom_edge", "type": "dstdomain", "values": ["edge-e2e.example"]})
    ids["dom_edge"] = r_["acl"]["id"]
    rule("deny", ["edgewin", "dom_edge"], "time: edge window")
    time.sleep(1)
    for label, target, expect in (("first minute of the window", m0, "deny"), ("last minute of the window", m0 + 1, "deny"),
                                  ("one minute after the window", m0 + 2, "allow")):
        wait_for_minute(target)
        a_before = minute_now()
        real = actual("local", "http://edge-e2e.example/")[0]
        pred = predicted(admin, "local", "http://edge-e2e.example/")["decision"]
        stable = minute_now() == a_before == target
        print(f"    minute {fmt(target)} ({label}): squid={real} panel={pred}")
        if stable:
            check(f"real squid: {label} -> {expect}", real == expect, real)
            check(f"panel prediction matches real squid: {label}", pred == real, f"panel={pred} squid={real}")
else:
    print("    (skipped the edge-minute probe: too close to midnight)")

# ---------------------------------------------------- reorder / disable change squid
print("== reordering and disabling really change behaviour")
s, rules = admin.call("GET", "/squid/access/rules")
by_comment = {x["comment"]: x for x in rules["rules"]}
allow_vip, deny_others = by_comment["vip may stream"], by_comment["everyone else may not"]
before = actual("lan", "http://media-e2e.example/", "GET", "alice")[0]
check("vip is allowed while 'allow' precedes 'deny'", before == "allow", before)
order = [x["id"] for x in rules["rules"]]
swapped = order[:]
i, j = swapped.index(allow_vip["id"]), swapped.index(deny_others["id"])
swapped[i], swapped[j] = swapped[j], swapped[i]
s, r = admin.call("PUT", "/squid/access/order", {"ids": swapped})
check("reorder applied and reloaded", s == 200 and r.get("reloaded") is True, str(r))
after = wait_for(lambda: actual("lan", "http://media-e2e.example/", "GET", "alice")[0] == "deny", 6)
check("after swapping, the same vip request is DENIED by real squid", after)
v = predicted(admin, "lan", "http://media-e2e.example/", "GET", "alice")
check("the tester follows the new order", v["decision"] == "deny", str(v["decision"]))
s, r = admin.call("PUT", "/squid/access/order", {"ids": order})
check("order restored", s == 200)
off = admin.call("PUT", f"/squid/access/rules/{deny_others['id']}", {"enabled": False})
check("rule disabled", off[0] == 200 and off[1].get("reloaded") is True, str(off))
# With the catch-all deny off, the "vip may stream" rule is still first in line
# for the media site: an anonymous request is asked to log in (407) instead of
# being refused, and a signed-in non-vip user falls through to the stock allows.
check("with the deny switched off, an anonymous user is asked to log in instead of being refused",
      wait_for(lambda: actual("local", "http://media-e2e.example/")[0] == "auth_required", 6))
check("…and a signed-in non-vip user now gets through",
      actual("lan", "http://media-e2e.example/", "GET", "bob")[0] == "allow")
v = predicted(admin, "lan", "http://media-e2e.example/", "GET", "bob")
check("the tester predicted exactly that", v["decision"] == "allow", str(v["decision"]))
admin.call("PUT", f"/squid/access/rules/{deny_others['id']}", {"enabled": True})

print("== validation and safety")
s, r = admin.call("POST", "/squid/access/acls", {"name": "bad1", "type": "url_regex", "values": ["(?i)x"]})
check("PCRE-only syntax refused", s == 422, str(r))
s, r = admin.call("POST", "/squid/access/acls", {"name": "bad2", "type": "port", "values": ["99999"]})
check("bad port refused", s == 422, str(r))
s, r = admin.call("DELETE", f"/squid/access/acls/{ids['keyword']}")
check("an ACL used by a rule cannot be deleted (409)", s == 409, str(r))
s, r = admin.call("POST", "/squid/access/rules", {"action": "deny", "comment": "x", "terms": [{"acl_id": 99999}]})
check("a rule with an unknown ACL refused", s == 422, str(r))
authless = admin.call("POST", "/squid/access/acls", {"name": "rx_probe", "type": "url_regex", "values": ["a[b"]})
check("an invalid regular expression refused", authless[0] == 422, str(authless[1]))

# -------------------------------------------------------------- fixed block order
print("== the order of panel blocks is fixed")
s, r = admin.call("POST", "/squid/restrictions", {"name": "e2e_restr", "domains": ["restr-e2e.example"], "days": list("SMTWHFA"),
                                                  "start_time": "00:00", "end_time": "23:59", "exempt_cidrs": []})
check("time restriction created", s == 200, str(r))
if s == 200:
    created_restr.append(r["restriction"]["id"])
for label, action in (("after a restriction was added", lambda: None),
                      ("after the rules were regenerated again", lambda: admin.call("PUT", f"/squid/access/rules/{deny_others['id']}", {"comment": "everyone else may not"}))):
    action()
    c = conf()
    a, b, allow = c.find("time-restrictions (managed"), c.find("access rules (managed"), c.find("http_access allow localhost")
    check(f"restrictions < access rules < stock allow ({label})", 0 <= a < b < allow, f"{a} {b} {allow}")
res = wait_for(lambda: actual("local", "http://restr-e2e.example/")[0] == "deny", 8)
check("a time restriction is enforced next to the user rules", bool(res), str(actual("local", "http://restr-e2e.example/")))
v = predicted(admin, "local", "http://restr-e2e.example/")
check("the tester attributes it to the time restriction",
      v["decision"] == "deny" and any(t["rule"]["source"] == "time_restriction" and t["result"] == "match" for t in v["trace"]),
      str(v["decision"]))
admin.call("DELETE", f"/squid/restrictions/{created_restr.pop()}")

# ------------------------------------------------------------- keyword + whitelist
print("== whitelist mode")
s, r = admin.call("POST", "/squid/access/acls", {"name": "allowed_sites", "type": "dstdomain", "values": ["example.org"]})
wl = r["acl"]["id"]; created_acls.append(wl); ids["allowed_sites"] = wl
s, r = rule("deny", ["!allowed_sites"], "whitelist mode")
check("whitelist rule added", s == 200, str(r))
check("a listed site is reachable", actual("local", "http://example.org/")[0] == "allow")
check("every other site is blocked, even from localhost", wait_for(lambda: actual("local", "http://oracle-plain.example/")[0] == "deny", 6))
v = predicted(admin, "local", "http://oracle-plain.example/")
check("the tester agrees", v["decision"] == "deny")
admin.call("DELETE", f"/squid/access/rules/{created_rules.pop()}")
check("removing the rule lifts it", wait_for(lambda: actual("local", "http://oracle-plain.example/")[0] == "allow", 6))

# ------------------------------------------------------------------ block lists
print("== block lists")
listdir = "/tmp/e2e-lists"
os.makedirs(listdir, exist_ok=True)
big = "".join(f"0.0.0.0 filler{i}.bulk-e2e.example\n" for i in range(3000))
open(f"{listdir}/list.txt", "w").write(
    "# e2e list\n127.0.0.1 localhost\n0.0.0.0 listed-e2e.example\n0.0.0.0 sub.listed-e2e.example\n||banner-e2e.example^\n" + big)
server = subprocess.Popen(["python3", "-m", "http.server", "8099", "--bind", WSL_IP, "--directory", listdir],
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
time.sleep(1)
try:
    s, r = admin.call("POST", "/squid/blocklists", {"name": "e2e-list", "url": f"http://{WSL_IP}:8099/list.txt", "interval_hours": 24})
    check("block list created and downloaded", s == 200 and r["source"]["entry_count"] > 3000 and r["source"]["last_status"].startswith("ok"), str(r)[:300])
    src = r["source"]; created_sources.append(src["id"])
    check("its ACL and deny rule exist", src["acl_name"] == "bl_e2e-list" and "bl_e2e-list" in conf())
    check("squid parses a config with a ~3000-entry list", parse_ok() and squid_up())
    check("a listed domain is blocked by real squid", wait_for(lambda: actual("local", "http://listed-e2e.example/")[0] == "deny", 8))
    check("its subdomain is blocked too (the parent entry covers it)", actual("local", "http://x.sub.listed-e2e.example/")[0] == "deny")
    check("an adblock-style entry is blocked", actual("local", "http://banner-e2e.example/")[0] == "deny")
    check("an unlisted domain is not", actual("local", "http://unlisted-e2e.example/")[0] == "allow")
    v = predicted(admin, "local", "http://listed-e2e.example/")
    check("the tester explains it by the list ACL", v["decision"] == "deny" and any(t["acl"] == "sqa_bl_e2e-list" for tr in v["trace"] for t in tr["terms"]), str(v["decision"]))
    fname = f"/etc/squid/blocklists/{src['id']}.txt"
    check("the list file is world-readable for squid", oct(os.stat(fname).st_mode & 0o777) == "0o644")

    with open(f"{listdir}/list.txt", "a") as f:
        f.write("0.0.0.0 added-later-e2e.example\n")
    s, r = admin.call("POST", f"/squid/blocklists/{src['id']}/refresh")
    check("refresh picks up the new entry", s == 200 and r["changed"] is True, str(r))
    check("…and it is blocked", wait_for(lambda: actual("local", "http://added-later-e2e.example/")[0] == "deny", 8))
    s, r = admin.call("POST", f"/squid/blocklists/{src['id']}/refresh")
    check("an unchanged list reports no change", s == 200 and r["changed"] is False, str(r))

    # the download must never reach internal services
    s, r = admin.call("POST", "/squid/blocklists", {"name": "e2e-ssrf1", "url": "http://127.0.0.1:8099/list.txt", "add_rule": False})
    st = r.get("source", {})
    if st:
        created_sources.append(st["id"])
    check("loopback URL is refused (SSRF)", s == 200 and st.get("last_status", "").startswith("error") and "refusing" in st.get("last_status", ""), str(r)[:250])
    s, r = admin.call("POST", "/squid/blocklists", {"name": "e2e-ssrf2", "url": "http://169.254.169.254/latest/meta-data/", "add_rule": False})
    st = r.get("source", {})
    if st:
        created_sources.append(st["id"])
    check("cloud-metadata URL is refused (SSRF)", s == 200 and "refusing" in st.get("last_status", ""), str(r)[:250])
    s, r = admin.call("POST", "/squid/blocklists", {"name": "e2e-badurl", "url": "file:///etc/passwd"})
    check("non-http URL rejected", s == 422, str(r))

    s, r = admin.call("DELETE", f"/squid/blocklists/{src['id']}")
    check("deleting a list used by a rule needs an explicit choice (409)", s == 409, str(r))
    s, r = admin.call("DELETE", f"/squid/blocklists/{src['id']}?remove_rules=1")
    check("deleted together with its rule", s == 200 and r.get("reloaded") is True, str(r))
    created_sources.remove(src["id"])
    check("its file is gone and the domain is reachable again", not os.path.exists(fname) and wait_for(lambda: actual("local", "http://listed-e2e.example/")[0] == "allow", 8))
finally:
    server.terminate()

# ----------------------------------------------------------------------- cleanup
print("== cleanup")
cleanup()
time.sleep(1)
check("squid.conf is byte-for-byte what it was before this test", conf() == ORIG, "differs")
check("no block list files left behind", not [f for f in os.listdir("/etc/squid/blocklists")], str(os.listdir("/etc/squid/blocklists")))
check("no proxy users left behind", not any(u in open("/etc/squid/passwd").read() for u in created_users))
check("squid up and parsing", squid_up() and parse_ok())

s, a = admin.call("GET", "/audit?limit=300")
acts = {(e["action"], e["status"]) for e in a["entries"]}
check("audit has the access-rule, order and block list changes",
      {("POST /squid/access/rules", 200), ("PUT /squid/access/order", 200), ("POST /squid/blocklists", 200), ("POST /squid/blocklists/:id/refresh", 200)} <= acts)

if must_reset:
    # Restored by the atexit hook after the report, so a crash cannot skip it.
    print("(the admin password is restored to its original one-time value on exit)")

print()
print("ALL PASSED" if not fails else "FAILED: " + ", ".join(fails))
sys.exit(1 if fails else 0)
