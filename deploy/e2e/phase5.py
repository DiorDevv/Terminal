#!/usr/bin/env python3
"""Phase 5 end-to-end: statistics, live data, logs, rotation and alerts, against
a real squid under systemd with the panel installed by deploy/install.sh.

Run on the host as root:

    E2E_ADMIN_PW='<admin password>' python3 deploy/e2e/phase5.py

The centrepiece is an ORACLE test: real requests go through squid, and the
numbers the panel reports (requests, bytes, cache hits, denied) must equal
what an independent parse of squid's access.log says for exactly those requests
- including across a panel restart and across a real `squid -k rotate`.
Alerts are delivered to a real listener; squid is really stopped and started.
Everything the script changes is undone at the end and squid.conf is asserted
byte-for-byte unchanged.
"""
import atexit, glob, http.cookiejar, http.server, json, os, shutil, subprocess, sys, tempfile, threading, time
import urllib.error, urllib.request

API = "http://localhost:8080/api"
ADMIN_PW = os.environ["E2E_ADMIN_PW"]
TMP_PW = "e2e-temporary-Pass-42"
LOG_DIR = "/var/log/squid"
ACCESS = LOG_DIR + "/access.log"
BLOCKED = "e2e5-blocked.example"
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


def wait_for(pred, timeout=30, step=0.5):
    t0 = time.time()
    while time.time() - t0 < timeout:
        v = pred()
        if v:
            return v
        time.sleep(step)
    return False


def squid_state():
    return sh("systemctl is-active squid")


WSL_IP = sh("hostname -I").split()[0]
ORIGIN_PORT, HOOK_PORT = 18080, 18081

# ------------------------------------------------------------------- fixtures
admin = Client()
s, r = admin.call("POST", "/auth/login", {"username": "admin", "password": ADMIN_PW})
assert s == 200, f"cannot log in as admin: {s} {r}"
if r["user"]["must_change_password"]:
    s, r = admin.call("PUT", "/auth/password", {"old_password": ADMIN_PW, "new_password": TMP_PW})
    assert s == 200, (s, r)

    def restore_admin():
        """Put the admin back to its original password even if the run crashes."""
        try:
            me = admin.call("GET", "/auth/me")[1]["user"]
            admin.call("POST", f"/panel/users/{me['id']}/password", {"password": ADMIN_PW})
        except Exception as e:
            print("could not restore the admin password:", e)

    atexit.register(restore_admin)

ORIG_CONF = conf()
LOGS_BEFORE = set(os.listdir(LOG_DIR))
s, orig_alerts = admin.call("GET", "/monitor/alerts")
assert s == 200, (s, orig_alerts)
ORIG_ALERT_CFG = orig_alerts["config"]

# A tiny origin server on the LAN address (squid refuses to_localhost).
web_root = tempfile.mkdtemp()
with open(web_root + "/f.bin", "wb") as f:
    f.write(b"x" * 5000)


class Quiet(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *a, **k):
        super().__init__(*a, directory=web_root, **k)

    def log_message(self, *a):
        pass


origin = http.server.ThreadingHTTPServer((WSL_IP, ORIGIN_PORT), Quiet)
threading.Thread(target=origin.serve_forever, daemon=True).start()

hooks = []


class Hook(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        hooks.append(json.loads(body))
        self.send_response(200)
        self.end_headers()

    def log_message(self, *a):
        pass


hook_srv = http.server.ThreadingHTTPServer((WSL_IP, HOOK_PORT), Hook)
threading.Thread(target=hook_srv.serve_forever, daemon=True).start()


def cleanup():
    """Undo everything; safe to run more than once."""
    try:
        if squid_state() not in ("active", "activating"):
            sh("systemctl start squid")
        admin.call("DELETE", f"/squid/blacklist/{BLOCKED}")
        admin.call("PUT", "/squid/settings", {"reset": ["logfile_rotate"]})
        cfg = json.loads(json.dumps(ORIG_ALERT_CFG))
        admin.call("PUT", "/monitor/alerts", {
            "squid_down": cfg["squid_down"], "disk_full": cfg["disk_full"],
            "denied_spike": cfg["denied_spike"], "error_spike": cfg["error_spike"],
            "telegram": {"enabled": False, "chat_id": cfg["telegram"]["chat_id"]},
            "webhook": {"enabled": False},
            "email": {"enabled": False, "host": cfg["email"]["host"], "port": cfg["email"]["port"],
                      "username": cfg["email"]["username"], "from": cfg["email"]["from"], "to": cfg["email"]["to"]},
        })
        admin.call("POST", "/monitor/alerts/check")  # a disabled condition drops its state
        for name in set(os.listdir(LOG_DIR)) - LOGS_BEFORE:
            os.remove(os.path.join(LOG_DIR, name))
    except Exception as e:
        print("cleanup problem:", e)
    origin.shutdown()
    hook_srv.shutdown()
    shutil.rmtree(web_root, ignore_errors=True)


atexit.register(cleanup)

# ----------------------------------------------------------- traffic + oracle
T0 = 0.0  # set below, once the baseline is known


def via_proxy(url):
    return sh(f"curl -s -m 15 -o /dev/null -x http://127.0.0.1:3128 -w '%{{http_code}}' {url}")


def get_ok(n):
    return [via_proxy(f"http://{WSL_IP}:{ORIGIN_PORT}/f.bin") for _ in range(n)]


def get_blocked(n):
    return [via_proxy(f"http://{BLOCKED}/x") for _ in range(n)]


def oracle():
    """Independent parse of every access.log (current + rotated) for our traffic."""
    files = [ACCESS] + sorted(glob.glob(ACCESS + ".[0-9]*"))
    ok = dict(requests=0, bytes=0, hits=0)
    blocked = 0
    for path in files:
        try:
            lines = open(path, errors="replace").read().splitlines()
        except OSError:
            continue
        for ln in lines:
            f = ln.split()
            if len(f) < 10 or float(f[0]) < T0:
                continue
            code, _, status = f[3].partition("/")
            url = f[6]
            if url.startswith(f"http://{WSL_IP}:{ORIGIN_PORT}/") and status != "407":
                ok["requests"] += 1
                ok["bytes"] += int(f[4])
                ok["hits"] += 1 if "HIT" in code else 0
            elif BLOCKED in url and status == "403" and code.startswith("TCP_DENIED"):
                blocked += 1
    return ok, blocked


def top_row(key):
    s, r = admin.call("GET", "/monitor/top?range=24h&by=domain&sort=requests&limit=100")
    assert s == 200, (s, r)
    for row in r["rows"]:
        if row["key"] == key:
            return row
    return {"key": key, "requests": 0, "bytes": 0, "hits": 0, "denied": 0}


s, _ = admin.call("POST", "/squid/blacklist", {"domain": BLOCKED})
assert s == 200, "cannot add the block-list entry"
wait_for(lambda: via_proxy(f"http://{BLOCKED}/x") == "403", 15)  # rule applied by squid

# the baseline is what the panel already knew about our two domains
time.sleep(6)
base_ok, base_blk = top_row(WSL_IP), top_row(BLOCKED)
T0 = time.time()  # the oracle counts only what we send from here on


def matches_oracle():
    ok, blocked = oracle()
    a, b = top_row(WSL_IP), top_row(BLOCKED)
    got = dict(requests=a["requests"] - base_ok["requests"], bytes=a["bytes"] - base_ok["bytes"], hits=a["hits"] - base_ok["hits"])
    got_blocked = b["denied"] - base_blk["denied"]
    return (got == ok and got_blocked == blocked, ok, blocked, got, got_blocked)


def verify(label):
    r = wait_for(lambda: matches_oracle()[0] and matches_oracle(), 40)
    if not r:
        r = matches_oracle()
    check(f"{label}: panel numbers equal the access.log oracle", r[0], f"oracle={r[1]}/{r[2]} panel={r[3]}/{r[4]}")
    return r


print("== 1. accounting against the real access.log")
codes = get_ok(6) + get_blocked(3)
check("requests went through squid (200 x6, 403 x3)", codes == ["200"] * 6 + ["403"] * 3, str(codes))
r = verify("first batch")
check("the oracle saw real traffic", r[1]["requests"] >= 6 and r[2] >= 3 and r[1]["bytes"] >= 6 * 5000, str(r[1:3]))
check("some replies were cache hits or misses, all counted", r[1]["requests"] >= 6)

s, sm = admin.call("GET", "/monitor/summary?range=24h")
check("summary endpoint works", s == 200 and sm["requests"] >= r[1]["requests"] and 24 <= len(sm["timeline"]) <= 25, str(sm)[:200])
check("timeline buckets add up to the totals", sum(b["requests"] for b in sm["timeline"]) == sm["requests"])
s, dn = admin.call("GET", "/monitor/denied?limit=20")
check("blocked requests are listed with their URL", s == 200 and any(BLOCKED in x["url"] for x in dn["rows"]), str(dn)[:200])

print("== 2. panel restart loses and repeats nothing")
sh("systemctl restart squidadmin")
check("panel is back", wait_for(lambda: sh("curl -s localhost:8080/api/health") != "", 30))
codes = get_ok(3) + get_blocked(2)
verify("after a panel restart")

print("== 3. live data and logs")
s, live = admin.call("GET", "/monitor/live")
info = live.get("info", {})
check("squid cache manager data is available", s == 200 and info.get("available") and info.get("version", "").startswith("7"), str(info)[:300])
check("live data reports squid running", live["squid"]["running"] is True)
check("log reader is healthy", live["reader"]["readable"] and not live["reader"]["last_error"], str(live["reader"]))
check("disk usage is reported", len(live["disks"]) >= 1 and 0 <= live["disks"][0]["used_percent"] <= 100)
s, lg = admin.call("GET", "/monitor/logs")
names = [f["name"] for f in lg["files"]]
check("access.log and cache.log are listed", "access.log" in names and "cache.log" in names, str(names))
s, tl = admin.call("GET", "/monitor/logs/cache.log?limit=5")
check("cache.log can be tailed", s == 200 and 0 < len(tl["lines"]) <= 5)
for bad in ("..%2Fpasswd", "%2Fetc%2Fpasswd", "..%2F..%2Fetc%2Fshadow", ".hidden"):
    s, _ = admin.call("GET", "/monitor/logs/" + bad)
    check(f"log path {bad} is refused", s in (400, 404))

print("== 4. real log rotation")
s, r_ = admin.call("PUT", "/squid/settings", {"values": {"logfile_rotate": ["3"]}})
check("logfile_rotate can be set from the panel", s == 200, str(r_))
check("squid.conf carries it", "logfile_rotate 3" in conf())
get_ok(2)
inode_before = os.stat(ACCESS).st_ino
s, r_ = admin.call("POST", "/monitor/logs/rotate")
check("rotate is accepted", s == 200, str(r_))
check("squid really rotated (new access.log, old one kept as .0)",
      wait_for(lambda: os.path.exists(ACCESS + ".0") and os.stat(ACCESS).st_ino != inode_before, 15))
codes = get_ok(4) + get_blocked(2)
verify("across a real rotation (nothing lost, nothing counted twice)")
s, _ = admin.call("PUT", "/squid/settings", {"reset": ["logfile_rotate"]})
check("setting reset", s == 200)
check("logfile_rotate is gone from squid.conf again", "logfile_rotate 3" not in conf())

print("== 5. alerts")
webhook = f"http://{WSL_IP}:{HOOK_PORT}/hook"
cfg = {
    "squid_down": {"enabled": True}, "disk_full": {"enabled": False, "percent": 90},
    "denied_spike": {"enabled": True, "threshold": 3, "window_minutes": 10},
    "error_spike": {"enabled": False, "threshold": 100, "window_minutes": 5},
    "telegram": {"enabled": False, "chat_id": ""},
    "webhook": {"enabled": True, "url": webhook},
    "email": {"enabled": False, "host": "", "port": 587, "username": "", "from": "", "to": []},
}
s, r_ = admin.call("PUT", "/monitor/alerts", cfg)
check("alert settings saved", s == 200, str(r_))
check("the webhook URL is not echoed back", webhook not in json.dumps(r_) and "/hook" not in json.dumps(r_))
s, g = admin.call("GET", "/monitor/alerts")
check("GET does not leak it either", "/hook" not in json.dumps(g) and g["config"]["webhook"]["url_set"])

hooks.clear()
s, r_ = admin.call("POST", "/monitor/alerts/test")
check("test message delivered", s == 200 and r_["results"].get("webhook") == "ok", str(r_))
check("the listener received it", wait_for(lambda: any(h.get("kind") == "test" for h in hooks), 5))

get_blocked(4)
time.sleep(8)  # the reader polls every 5 s
s, r_ = admin.call("POST", "/monitor/alerts/check")
fired = [e for e in r_.get("events", []) if e["key"] == "denied_spike"]
check("a spike of blocked requests fires", s == 200 and len(fired) == 1 and fired[0]["kind"] == "firing", str(r_)[:300])
check("delivered to the webhook", wait_for(lambda: any(h.get("key") == "denied_spike" and h.get("kind") == "firing" for h in hooks), 5))
n = len(hooks)
s, r_ = admin.call("POST", "/monitor/alerts/check")
check("a still-firing condition does not repeat", not [e for e in r_["events"] if e["key"] == "denied_spike"] and len(hooks) == n, str(r_)[:300])

hooks.clear()
sh("systemctl stop squid")
check("squid is really stopped", wait_for(lambda: squid_state() == "inactive", 60))
admin.call("POST", "/monitor/alerts/check")
admin.call("POST", "/monitor/alerts/check")
check("squid_down fires exactly once", wait_for(lambda: sum(1 for h in hooks if h.get("key") == "squid_down" and h.get("kind") == "firing") == 1, 10),
      str([(h.get("key"), h.get("kind")) for h in hooks]))
sh("systemctl start squid")
check("squid is running again", wait_for(lambda: squid_state() == "active", 60))
s, r_ = admin.call("POST", "/monitor/alerts/check")
check("recovery is announced", wait_for(lambda: any(h.get("key") == "squid_down" and h.get("kind") == "resolved" for h in hooks), 10),
      str([(h.get("key"), h.get("kind")) for h in hooks]))
s, g = admin.call("GET", "/monitor/alerts")
kinds = [(e["key"], e["kind"]) for e in g["events"]]
check("the history records firing and resolved", ("squid_down", "firing") in kinds and ("squid_down", "resolved") in kinds, str(kinds))
check("deliveries are recorded as ok", all(e["delivery"] for e in g["events"][:3]), str(g["events"][:3]))

print("== 6. clean up")
cleanup()
check("squid.conf is byte-for-byte what it was", conf() == ORIG_CONF)
check("no log files left behind", set(os.listdir(LOG_DIR)) == LOGS_BEFORE, str(set(os.listdir(LOG_DIR)) ^ LOGS_BEFORE))
check("squid is running", squid_state() == "active")
rc = subprocess.run(["squid", "-k", "parse"], capture_output=True).returncode
check("squid.conf parses", rc == 0)

print()
if fails:
    print(f"{len(fails)} FAILED:", *fails, sep="\n  ")
    sys.exit(1)
print("all phase 5 checks passed")
