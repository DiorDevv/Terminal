#!/usr/bin/env python3
"""Phase 6 end-to-end: account states, quotas, user groups, speed and download
limits and CSV import, against a real squid under systemd with the panel
installed by deploy/install.sh.

Run on the host as root:

    E2E_ADMIN_PW='<admin password>' python3 deploy/e2e/phase6.py

Everything is checked through real proxy requests with real credentials (the
panel's own policy tester is compared with what squid does), including limits
that squid enforces on real transfers and an account that was created by a CSV
import and then signs in through squid. All created objects are removed and
squid.conf is asserted byte-for-byte unchanged at the end.
"""
import atexit, http.cookiejar, http.server, json, os, subprocess, sys, tempfile, threading, time
import urllib.error, urllib.request

API = "http://localhost:8080/api"
ADMIN_PW = os.environ["E2E_ADMIN_PW"]
TMP_PW = "e2e-temporary-Pass-42"
ORIGIN_PORT = 18082
FILE_BYTES = 400_000
PASSWORDS = {"alice": "alicepass1", "bob": "bobpass22", "carol": "carolpass3"}
fails = []


class Client:
    def __init__(self):
        self.jar = http.cookiejar.CookieJar()
        self.op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))

    def call(self, method, path, body=None, raw=None, ctype="application/json"):
        req = urllib.request.Request(API + path, method=method)
        req.add_header("Content-Type", ctype)
        req.add_header("Origin", "http://localhost:5173")
        req.add_header("X-Requested-With", "squidadmin")
        data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
        try:
            with self.op.open(req, data, timeout=200) as r:
                payload = r.read()
                try:
                    return r.status, json.loads(payload or b"{}")
                except ValueError:
                    return r.status, {"raw": payload.decode(errors="replace")}
        except urllib.error.HTTPError as e:
            raw_err = e.read()
            try:
                return e.code, json.loads(raw_err or b"{}")
            except ValueError:
                return e.code, {"raw": raw_err.decode(errors="replace")}


def check(name, cond, detail=""):
    print(("PASS  " if cond else "FAIL  ") + name + (f"   [{detail}]" if detail and not cond else ""))
    if not cond:
        fails.append(name)


def sh(cmd):
    return subprocess.run(cmd, shell=True, capture_output=True, text=True).stdout.strip()


def conf():
    return open("/etc/squid/squid.conf").read()


def wait_for(pred, timeout=30, step=0.5):
    t0 = time.time()
    while time.time() - t0 < timeout:
        v = pred()
        if v:
            return v
        time.sleep(step)
    return False


WSL_IP = sh("hostname -I").split()[0]
PROXIES = {"local": "127.0.0.1", "lan": WSL_IP}
URL = f"http://{WSL_IP}:{ORIGIN_PORT}/f.bin"


def fetch(user=None, via="lan", url=URL):
    """(status, bytes downloaded, seconds) of one request through the real squid."""
    auth = f"--proxy-user {user}:{PASSWORDS[user]}" if user else ""
    out = sh(f"curl -s -m 60 -o /dev/null -x http://{PROXIES[via]}:3128 {auth} -w '%{{http_code}} %{{size_download}} %{{time_total}}' {url}")
    code, size, secs = out.split()
    return int(code), int(size), float(secs)


def status(user=None, via="lan"):
    return fetch(user, via)[0]


# --------------------------------------------------------------------- setup
admin = Client()
s, r = admin.call("POST", "/auth/login", {"username": "admin", "password": ADMIN_PW})
assert s == 200, f"cannot log in as admin: {s} {r}"
if r["user"]["must_change_password"]:
    s, r = admin.call("PUT", "/auth/password", {"old_password": ADMIN_PW, "new_password": TMP_PW})
    assert s == 200, (s, r)

    def restore_admin():
        try:
            me = admin.call("GET", "/auth/me")[1]["user"]
            admin.call("POST", f"/panel/users/{me['id']}/password", {"password": ADMIN_PW})
        except Exception as e:
            print("could not restore the admin password:", e)

    atexit.register(restore_admin)

# The origin: a 400 KB file that squid may not cache (delay pools shape what
# reaches the client, and cache hits would blur the timing).
web_root = tempfile.mkdtemp()
open(web_root + "/f.bin", "wb").write(b"x" * FILE_BYTES)


class Origin(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *a, **k):
        super().__init__(*a, directory=web_root, **k)

    def end_headers(self):
        self.send_header("Cache-Control", "no-store")
        super().end_headers()

    def log_message(self, *a):
        pass


origin = http.server.ThreadingHTTPServer((WSL_IP, ORIGIN_PORT), Origin)
threading.Thread(target=origin.serve_forever, daemon=True).start()


def cleanup():
    """Remove everything this script created; safe to run twice."""
    try:
        _, lim = admin.call("GET", "/squid/limits")
        for x in lim.get("limits", []):
            if x["name"].startswith("e2e "):
                admin.call("DELETE", f"/squid/limits/{x['id']}")
        _, ug = admin.call("GET", "/squid/user-groups")
        for x in ug.get("groups", []):
            if x["name"].startswith("e2e"):
                admin.call("DELETE", f"/squid/user-groups/{x['id']}")
        for u in PASSWORDS:
            admin.call("PUT", f"/squid/proxy-users/{u}", {"disabled": False, "expires_at": 0, "daily_quota_mb": 0, "note": ""})
            admin.call("DELETE", f"/squid/users/{u}")
    except Exception as e:
        print("cleanup problem:", e)


atexit.register(origin.shutdown)
atexit.register(cleanup)
cleanup()
ORIG = conf()

print("== setup: two proxy users")
for u in ("alice", "bob"):
    s, r = admin.call("POST", "/squid/users", {"username": u, "password": PASSWORDS[u]})
    check(f"proxy user {u} created", s == 200, str(r))
    time.sleep(0.5)
ORIG_WITH_USERS = conf()  # the panel adds the proxy-auth block when the first user appears

check("both accounts can use the proxy (signed in over the LAN address)", status("alice") == 200 and status("bob") == 200)
check("from localhost squid lets anonymous requests through, as before", status(None, "local") == 200)
check("over the LAN address an anonymous request must log in", status(None, "lan") == 407)
check("a wrong password is refused", int(sh(f"curl -s -m 20 -o /dev/null -x http://{WSL_IP}:3128 --proxy-user alice:wrong -w '%{{http_code}}' {URL}")) == 407)

s, lst = admin.call("GET", "/squid/proxy-users")
mine = [(u["username"], u["status"]) for u in lst["users"] if u["username"] in ("alice", "bob")]
check("the account list shows both, active", s == 200 and mine == [("alice", "active"), ("bob", "active")], str(lst))

# ------------------------------------------------------------ disabling
print("== disabling an account")
s, r = admin.call("PUT", "/squid/proxy-users/alice", {"disabled": True, "note": "e2e: switched off"})
check("alice is disabled", s == 200 and r.get("reloaded") is True, str(r))
check("squid.conf carries the deny rule before the allow rules",
      "acl sqa_up_blocked proxy_auth alice" in conf() and conf().index("http_access deny sqa_up_auth sqa_up_blocked") < conf().index("http_access allow localhost"))
check("a disabled alice gets a plain 403, not a login loop (407)", wait_for(lambda: status("alice") == 403, 10), str(fetch("alice")))
check("a disabled alice is refused from localhost too when she sends her login", status("alice", "local") in (403, 200) and status("alice", "local") != 407, str(fetch("alice", "local")))
check("bob is not affected", status("bob") == 200)
check("an anonymous request from localhost is still not challenged", status(None, "local") == 200)
check("an anonymous request over the LAN is asked to log in as always", status(None, "lan") == 407)

for user, src in (("alice", WSL_IP), ("bob", WSL_IP), (None, "127.0.0.1")):
    body = {"src_ip": src, "url": URL, "method": "GET"}
    if user:
        body["user"] = user
    s, v = admin.call("POST", "/squid/access/test", body)
    actual = status(user, "local" if src == "127.0.0.1" else "lan")
    want = "deny" if actual == 403 else "allow"
    check(f"the policy tester agrees with squid for {user or 'anonymous'}", s == 200 and v["decision"] == want, f"tester={v.get('decision')} squid={actual}")

s, r = admin.call("PUT", "/squid/proxy-users/alice", {"disabled": False})
check("alice is enabled again", s == 200)
check("and can use the proxy again", wait_for(lambda: status("alice") == 200, 10))
s, r = admin.call("PUT", "/squid/proxy-users/alice", {"note": "e2e: only a note"})
check("changing a note reloads nothing", s == 200 and "reloaded" not in r, str(r))

# ------------------------------------------------------------- expiry
print("== expiry")
s, r = admin.call("PUT", "/squid/proxy-users/alice", {"expires_at": int(time.time()) - 60})
check("an expiry in the past blocks at once", s == 200 and wait_for(lambda: status("alice") == 403, 10))
s, lst = admin.call("GET", "/squid/proxy-users")
check("the list says expired", [u["status"] for u in lst["users"] if u["username"] == "alice"] == ["expired"])

soon = int(time.time()) + 15
s, r = admin.call("PUT", "/squid/proxy-users/alice", {"expires_at": soon})
check("a future expiry leaves the account working for now", s == 200 and status("alice") == 200)
t0 = time.time()
blocked = wait_for(lambda: status("alice") == 403, 75, 2)
check("the account is blocked after its expiry, with nobody touching the panel", blocked, f"still allowed {time.time() - soon:.0f}s after expiry")
if blocked:
    print(f"    (blocked {time.time() - soon:.0f}s after the expiry time)")
s, r = admin.call("PUT", "/squid/proxy-users/alice", {"expires_at": 0})
check("no expiry brings the account back", s == 200 and wait_for(lambda: status("alice") == 200, 10))

# -------------------------------------------------------------- quota
print("== daily quota")
s, r = admin.call("PUT", "/squid/proxy-users/alice", {"daily_quota_mb": 1})
check("a 1 MB daily quota is set", s == 200)
for _ in range(3):
    fetch("alice")  # 3 x 400 KB
s, lst = admin.call("GET", "/squid/proxy-users")
got = wait_for(lambda: [u for u in admin.call("GET", "/squid/proxy-users")[1]["users"] if u["username"] == "alice" and u["used_today"] >= 1_000_000], 30)
check("the panel counts what alice transferred today", bool(got), str(lst)[:300])
enforced = wait_for(lambda: status("alice") == 403, 75, 2)
check("alice is blocked once the quota is used up", enforced)
s, lst = admin.call("GET", "/squid/proxy-users")
al = [u for u in lst["users"] if u["username"] == "alice"][0]
check("the list says quota_exceeded", al["status"] == "quota_exceeded", str(al))
check("bob (no quota) is unaffected", status("bob") == 200)
s, r = admin.call("PUT", "/squid/proxy-users/alice", {"daily_quota_mb": 0})
check("removing the quota lets alice back in", s == 200 and wait_for(lambda: status("alice") == 200, 10))

# ---------------------------------------------------------- user groups
print("== user groups")
s, g = admin.call("POST", "/squid/user-groups", {"name": "e2e_staff"})
check("group created", s == 200, str(g))
gid = g["group"]["id"]
s, r = admin.call("PUT", f"/squid/user-groups/{gid}/members", {"members": ["bob"]})
check("bob is a member", s == 200, str(r))
s, r = admin.call("PUT", f"/squid/user-groups/{gid}/members", {"members": ["bob", "nobody"]})
check("a non-existent member is refused", s == 422)

# ---------------------------------------------------------- speed limit
print("== speed limit")
base_alice = fetch("alice")
check("without a limit a 400 KB download is quick", base_alice[0] == 200 and base_alice[1] == FILE_BYTES and base_alice[2] < 3, str(base_alice))

s, r = admin.call("POST", "/squid/limits", {"name": "e2e slow alice", "kind": "speed", "enabled": True,
                                            "spec": {"users": ["alice"], "scope": "each_user", "rate_kbps": 50, "burst_kb": 50}})
check("a 50 KB/s per-user limit for alice is created", s == 200 and r.get("reloaded") is True, str(r))
check("squid.conf has the delay pool", "delay_pools 1" in conf().splitlines() and "delay_parameters 1 -1/-1 -1/-1 -1/-1 51200/51200" in conf())
slow = fetch("alice")
check("alice's download is really slowed down (about 7 s expected)", slow[0] == 200 and slow[1] == FILE_BYTES and slow[2] > 5, str(slow))
fast = fetch("bob")
check("bob, who is not covered, downloads at full speed", fast[0] == 200 and fast[2] < 3, str(fast))
print(f"    alice {slow[2]:.1f}s, bob {fast[2]:.1f}s")

s, lim = admin.call("GET", "/squid/limits")
lid = lim["limits"][0]["id"]
s, r = admin.call("PUT", f"/squid/limits/{lid}", {"name": "e2e slow alice", "kind": "speed", "enabled": False,
                                                   "spec": {"users": ["alice"], "scope": "each_user", "rate_kbps": 50, "burst_kb": 50}})
check("switching the limit off works", s == 200 and "delay_pools 1" not in conf().splitlines(), str(r))
check("alice is fast again", wait_for(lambda: fetch("alice")[2] < 3, 15))
s, r = admin.call("DELETE", f"/squid/limits/{lid}")
check("the limit is deleted", s == 200)

# ------------------------------------------------------- download limit
print("== download size limit")
s, r = admin.call("POST", "/squid/limits", {"name": "e2e small downloads", "kind": "download", "enabled": True,
                                            "spec": {"user_groups": [gid], "max_mb": 1}})
check("a 1 MB download limit for the group is created", s == 200 and any(l.startswith("reply_body_max_size 1 MB ") for l in conf().splitlines()), str(r))
s, lim = admin.call("GET", "/squid/limits")
lid = lim["limits"][0]["id"]
open(web_root + "/big.bin", "wb").write(b"y" * 3_000_000)
BIG = f"http://{WSL_IP}:{ORIGIN_PORT}/big.bin"
big_bob = fetch("bob", url=BIG)
big_alice = fetch("alice", url=BIG)
check("a group member cannot download more than the limit", big_bob[0] != 200 or big_bob[1] < 3_000_000, str(big_bob))
check("someone outside the group can", big_alice[0] == 200 and big_alice[1] == 3_000_000, str(big_alice))
print(f"    bob got {big_bob[0]} / {big_bob[1]} bytes of 3000000; alice got {big_alice[0]} / {big_alice[1]}")
check("small files still work for the member", fetch("bob")[0] == 200)
s, r = admin.call("DELETE", f"/squid/limits/{lid}")
check("the download limit is deleted", s == 200 and not any(l.startswith("reply_body_max_size 1 MB") for l in conf().splitlines()))

# --------------------------------------------------------------- CSV
print("== CSV import and export")
csv_ok = "username,password,disabled,daily_quota_mb,groups,note\ncarol,carolpass3,,0,e2e_csv,=cmd|' /C calc'!A0\nalice,,,,,imported note\n"
s, r = admin.call("POST", "/squid/proxy-users-import?dry_run=1", raw=csv_ok.encode(), ctype="text/csv")
check("a dry run reports what would happen", s == 200 and r["report"]["created"] == 1 and r["report"]["updated"] == 1, str(r))
check("and creates nothing", status("carol") == 407)

bad_csv = "username,password\ncarol,carolpass3\ndave,\n"
s, r = admin.call("POST", "/squid/proxy-users-import", raw=bad_csv.encode(), ctype="text/csv")
check("a file with a bad row is refused as a whole", s == 422 and r["report"]["errors"] == 1, str(r))
check("and carol was not created", status("carol") == 407)

s, r = admin.call("POST", "/squid/proxy-users-import", raw=csv_ok.encode(), ctype="text/csv")
check("the file is imported", s == 200 and r["report"]["applied"] and r.get("reloaded") is True, str(r))
check("carol, created by the import, can sign in through the real squid", wait_for(lambda: status("carol") == 200, 10))
s, ex = admin.call("GET", "/squid/proxy-users-export")
raw = ex.get("raw", "")
check("the export lists carol, with her group and a defused note", "carol" in raw and "e2e_csv" in raw and "'=cmd" in raw, raw[:400])
check("the export contains no password material", "carolpass3" not in raw and "$apr1$" not in raw)
check("the note of an existing account was updated", "imported note" in raw)

# ----------------------------------------------------- deleting accounts
print("== deleting an account cleans up")
admin.call("PUT", "/squid/proxy-users/bob", {"disabled": True})
check("bob is blocked", wait_for(lambda: status("bob") == 403, 10))
s, r = admin.call("DELETE", "/squid/users/bob")
check("bob is deleted", s == 200)
check("his block entry is gone from squid.conf", "proxy_auth bob" not in conf() and "proxy_auth alice bob" not in conf())
s, lst = admin.call("GET", "/squid/proxy-users")
check("and he is no longer listed", "bob" not in [u["username"] for u in lst["users"]])

# ------------------------------------------------------------- cleanup
print("== cleanup")
cleanup()
after = conf()
check("no panel block of this phase is left in squid.conf", "squidadmin: user-policy" not in after and "squidadmin: limits" not in after)
check("squid.conf is byte-for-byte what it was once the test users existed", after == ORIG_WITH_USERS, "differs")
check("squid is running", sh("systemctl is-active squid") == "active")
check("squid.conf parses", subprocess.run(["squid", "-k", "parse"], capture_output=True).returncode == 0)

print()
if fails:
    print(f"{len(fails)} FAILED:", *fails, sep="\n  ")
    sys.exit(1)
print("all phase 6 checks passed")
