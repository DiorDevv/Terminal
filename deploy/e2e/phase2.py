#!/usr/bin/env python3
"""Phase 2 end-to-end: real backend running as the unprivileged `squidadmin`
user, real squid, real systemd + sudo.

Run on the machine that hosts squid + the installed panel (as root):

    E2E_ORIG_PW='<admin password>' python3 deploy/e2e/phase2.py

It logs in as `admin`, so E2E_ORIG_PW must be the admin's *current* password.
The script temporarily changes it, and at the end resets `admin` back to
E2E_ORIG_PW with "must change at next login" set. If you run it right after a
fresh install, that is the one-time password from `journalctl -u squidadmin`.
Note: it deliberately fails logins, so wait ~1 minute between runs (per-IP
login rate limit). Expect "ALL PASSED" / 62 checks.
"""
import base64, http.cookiejar, json, os, socket, subprocess, sys, time, urllib.error, urllib.request

HOST, PORT = "localhost", 8080
API = f"http://{HOST}:{PORT}/api"
ORIG_PW = os.environ["E2E_ORIG_PW"]
TMP_PW = "e2e-temporary-Pass-42"
fails = []


class Client:
    """One browser: its own cookie jar."""

    def __init__(self, origin="http://localhost:5173"):
        self.jar = http.cookiejar.CookieJar()
        self.op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))
        self.origin = origin

    def call(self, method, path, body=None, headers=None, csrf=True):
        req = urllib.request.Request(API + path, method=method)
        req.add_header("Content-Type", "application/json")
        req.add_header("Origin", self.origin)
        if csrf:
            req.add_header("X-Requested-With", "squidadmin")
        for k, v in (headers or {}).items():
            req.add_header(k, v)
        data = json.dumps(body).encode() if body is not None else None
        try:
            with self.op.open(req, data, timeout=90) as r:
                return r.status, json.loads(r.read() or b"{}")
        except urllib.error.HTTPError as e:
            raw = e.read()
            try:
                return e.code, json.loads(raw or b"{}")
            except ValueError:
                return e.code, {"raw": raw.decode(errors="replace")}

    @property
    def token(self):
        for c in self.jar:
            if c.name == "squidadmin_session":
                return c.value
        return None


def check(name, cond, detail=""):
    print(("PASS  " if cond else "FAIL  ") + name + (f"   [{detail}]" if detail and not cond else ""))
    if not cond:
        fails.append(name)


def sh(cmd):
    return subprocess.run(cmd, shell=True, capture_output=True, text=True).stdout.strip()


def proxy_code(url="http://example.org/"):
    return sh(f"curl -s -m 10 -o /dev/null -w '%{{http_code}}' -x http://127.0.0.1:3128 {url}")


def squid_up():
    return sh("systemctl is-active squid") in ("active", "deactivating")


def conf():
    return open("/etc/squid/squid.conf").read()


def ws_handshake(token, origin, path="/api/squid/logs/stream", read_frame=False):
    s = socket.create_connection((HOST, PORT), timeout=10)
    lines = [f"GET {path} HTTP/1.1", f"Host: {HOST}:{PORT}", "Upgrade: websocket", "Connection: Upgrade",
             "Sec-WebSocket-Key: " + base64.b64encode(os.urandom(16)).decode(), "Sec-WebSocket-Version: 13"]
    if origin:
        lines.append("Origin: " + origin)
    if token:
        lines.append("Cookie: squidadmin_session=" + token)
    s.sendall(("\r\n".join(lines) + "\r\n\r\n").encode())
    buf = b""
    while b"\r\n\r\n" not in buf:
        chunk = s.recv(4096)
        if not chunk:
            break
        buf += chunk
    status = int(buf.split(b" ", 2)[1]) if buf else 0
    frame = None
    if read_frame and status == 101:
        subprocess.run(["curl", "-s", "-m", "5", "-o", "/dev/null", "-x", "http://127.0.0.1:3128", "http://example.org/ws-probe"])
        s.settimeout(8)
        try:
            data = buf.split(b"\r\n\r\n", 1)[1]
            while len(data) < 2:
                data += s.recv(4096)
            ln = data[1] & 0x7F
            while len(data) < 2 + ln:
                data += s.recv(4096)
            frame = data[2:2 + ln].decode(errors="replace")
        except socket.timeout:
            frame = None
    s.close()
    return status, frame


# --------------------------------------------------------------------------- 0
print("== the panel runs unprivileged")
pid = sh("pgrep -f '^/opt/squidadmin/bin/squidadmin-backend'").split("\n")[0]
owner = sh(f"ps -o user= -p {pid}")
print("    backend pid", pid, "runs as:", owner)
check("backend is NOT root", owner and owner != "root", owner)

# --------------------------------------------------------------------------- 1
print("== first login: password change is forced")
admin = Client()
s, r = admin.call("POST", "/auth/login", {"username": "admin", "password": ORIG_PW})
check("login with the one-time password", s == 200 and r["user"]["must_change_password"] is True, str(r))
check("session cookie set, token not in body", admin.token and admin.token not in json.dumps(r))

s, r = admin.call("GET", "/squid/blacklist")
check("API locked until password is changed", s == 403 and r.get("code") == "password_change_required", str(r))
s, r = admin.call("PUT", "/auth/password", {"old_password": ORIG_PW, "new_password": "admin123"})
check("weak/default new password rejected", s == 422, str(r))
s, r = admin.call("PUT", "/auth/password", {"old_password": ORIG_PW, "new_password": TMP_PW})
check("password changed", s == 200, str(r))
s, r = admin.call("GET", "/squid/blacklist")
check("API open after the change", s == 200 and r["domains"] == [], str(r))

# --------------------------------------------------------------------------- 2
print("== service control through sudo (as the unprivileged user)")
s, info = admin.call("GET", "/squid/service")
check("service info via systemctl show", s == 200 and info["manager"] == "systemd" and info["running"], str(info))
check("version + uptime + memory", info["version"].startswith("7.") and info["started_at"] > 0 and info["memory_bytes"] > 0)

# --------------------------------------------------------------------------- 3
print("== managing squid files as a non-root user")
s, r = admin.call("POST", "/squid/blacklist", {"domain": "blocked-e2e.example"})
check("blacklist add + reload (sudo systemctl reload)", s == 200 and r.get("reloaded") is True, str(r))
check("list file updated on disk", ".blocked-e2e.example" in open("/etc/squid/blocked_sites.txt").read())
# `systemctl reload` returns in ~40ms but squid finishes reconfiguring ~70ms
# later (measured), so poll briefly instead of racing it.
code = "?"
for _ in range(40):
    code = proxy_code("http://blocked-e2e.example/")
    if code == "403":
        break
    time.sleep(0.05)
check("squid enforces it", code == "403", code)
s, r = admin.call("DELETE", "/squid/blacklist/blocked-e2e.example")
check("blacklist remove", s == 200 and r.get("reloaded") is True, str(r))
check("list file cleaned", "blocked-e2e" not in open("/etc/squid/blocked_sites.txt").read())

s, r = admin.call("POST", "/squid/restrictions", {"name": "e2e_all_day", "domains": ["example.org"], "days": list("SMTWHFA"),
                                                  "start_time": "00:00", "end_time": "23:59", "exempt_cidrs": []})
check("restriction created + reloaded", s == 200 and r.get("reloaded") is True, str(r))
check("restriction really blocks (403)", proxy_code() == "403", proxy_code())
rid = r["restriction"]["id"] if s == 200 else 0
admin.call("DELETE", f"/squid/restrictions/{rid}")

s, r = admin.call("POST", "/squid/users", {"username": "e2e_proxy", "password": "proxy-pass-1"})
check("proxy user added (htpasswd as non-root)", s == 200 and "e2e_proxy" in open("/etc/squid/passwd").read(), str(r))
check("passwd file still unreadable by others", oct(os.stat("/etc/squid/passwd").st_mode & 0o777) == "0o640",
      oct(os.stat("/etc/squid/passwd").st_mode & 0o777))
ps_hits = sh("ps aux | grep -c '[p]roxy-pass-1'")
check("password never appears in the process list", ps_hits == "0", ps_hits)
admin.call("DELETE", "/squid/users/e2e_proxy")

s, r = admin.call("PUT", "/squid/lan-access", {"allowed": True})
check("LAN toggle on", s == 200 and r.get("reloaded") is True, str(r))
s, r = admin.call("PUT", "/squid/lan-access", {"allowed": False})
check("LAN toggle off", s == 200 and r.get("reloaded") is True, str(r))

# --------------------------------------------------------------------------- 4
print("== rollback still works without root (sudo systemctl start after squid dies)")
good = conf()
bad = good + "\ncache_dir ufs /proc/nonexistent/cache 100 16 256\n"
s, r = admin.call("PUT", "/squid/config", {"content": bad})
check("bad-at-runtime config passes the parse check", s == 200, str(r))
s, r = admin.call("POST", "/squid/reconfigure")
print("    ->", s, str(r)[:160])
time.sleep(3)
check("reload reported failure + rollback", s == 422 and "restored automatically" in r.get("error", ""), str(r))
check("squid is running after rollback", squid_up())
check("config restored", conf() == good)

# --------------------------------------------------------------------------- 5
print("== stop / start / restart via sudo (non-blocking)")
t0 = time.time()
s, r = admin.call("POST", "/squid/service/stop")
check("stop accepted quickly", s == 200 and time.time() - t0 < 10, f"{s} {time.time()-t0:.1f}s")
for _ in range(60):
    if not squid_up():
        break
    time.sleep(1)
check("squid stopped", not squid_up())
s, r = admin.call("POST", "/squid/service/start")
check("start via sudo", s == 200 and r["service"]["running"], str(r))
time.sleep(2)
check("squid running again", squid_up())

# --------------------------------------------------------------------------- 6
print("== roles, end to end")
s, r = admin.call("POST", "/panel/users", {"username": "e2e_viewer", "password": "viewer-pass-123", "role": "viewer"})
check("admin creates a viewer", s == 200 and r["user"]["must_change_password"] is True, str(r))
s, r = admin.call("POST", "/panel/users", {"username": "e2e_oper", "password": "operator-pass-123", "role": "operator"})
check("admin creates an operator", s == 200, str(r))
oper_id = r["user"]["id"] if s == 200 else 0

viewer, oper = Client(), Client()
viewer.call("POST", "/auth/login", {"username": "e2e_viewer", "password": "viewer-pass-123"})
viewer.call("PUT", "/auth/password", {"old_password": "viewer-pass-123", "new_password": "viewer-NEW-pass-9"})
oper.call("POST", "/auth/login", {"username": "e2e_oper", "password": "operator-pass-123"})
oper.call("PUT", "/auth/password", {"old_password": "operator-pass-123", "new_password": "operator-NEW-pass-9"})

for who, cl, method, path, body, want in [
    ("viewer", viewer, "GET", "/squid/blacklist", None, 200),
    ("viewer", viewer, "POST", "/squid/blacklist", {"domain": "x.example"}, 403),
    ("viewer", viewer, "GET", "/squid/config", None, 403),
    ("operator", oper, "POST", "/squid/blacklist", {"domain": "op-added.example"}, 200),
    ("operator", oper, "DELETE", "/squid/blacklist/op-added.example", None, 200),
    ("operator", oper, "PUT", "/squid/config", {"content": "x"}, 403),
    ("operator", oper, "POST", "/squid/service/stop", None, 403),
    ("operator", oper, "POST", "/squid/service/restart", None, 403),
    ("operator", oper, "GET", "/panel/users", None, 403),
    ("operator", oper, "GET", "/audit", None, 403),
]:
    s, r = cl.call(method, path, body)
    check(f"{who} {method} {path} -> {want}", s == want, f"{s} {r}")

print("== CSRF / origin on the real server")
s, _ = admin.call("POST", "/squid/blacklist", {"domain": "csrf.example"}, csrf=False)
check("no CSRF header -> 403", s == 403)
evil = Client(origin="https://evil.example"); evil.jar = admin.jar
evil.op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(admin.jar))
s, _ = evil.call("POST", "/squid/blacklist", {"domain": "csrf.example"})
check("stolen-cookie request from a foreign Origin -> 403", s == 403, str(s))
check("nothing was added", "csrf.example" not in open("/etc/squid/blocked_sites.txt").read())

print("== WebSocket (live logs)")
st, _ = ws_handshake(None, "http://localhost:5173")
check("no session -> refused", st == 401, st)
st, _ = ws_handshake(admin.token, "https://evil.example")
check("foreign origin -> refused (cross-site WebSocket hijacking)", st == 403, st)
st, _ = ws_handshake(viewer.token, "http://localhost:5173")
check("viewer may not stream logs", st == 403, st)
st, frame = ws_handshake(oper.token, "http://localhost:5173", read_frame=True)
check("operator streams logs from the allowed origin", st == 101, st)
check("...and the unprivileged user can read access.log", frame is not None and "ws-probe" in frame, repr(frame))
st, _ = ws_handshake(None, "http://localhost:5173", path="/api/squid/logs/stream?token=" + admin.token)
check("a token in the URL (no cookie) no longer authenticates", st == 401, st)
st, _ = ws_handshake(admin.token, "http://localhost:5173", path="/api/squid/logs/stream")
check("the cookie does", st == 101, st)

print("== sessions, disable, reset")
s, r = admin.call("GET", "/auth/sessions")
check("session list shows this device", s == 200 and any(x["current"] for x in r["sessions"]), str(r))
s, r = admin.call("PUT", f"/panel/users/{oper_id}", {"disabled": True})
check("admin disables the operator", s == 200 and r["user"]["disabled"] is True, str(r))
s, _ = oper.call("GET", "/squid/blacklist")
check("operator's live session is dead immediately", s == 401, s)
s, _ = Client().call("POST", "/auth/login", {"username": "e2e_oper", "password": "operator-NEW-pass-9"})
check("disabled account cannot log in", s == 401, s)

print("== audit log")
s, r = admin.call("GET", "/audit?limit=200")
acts = [(e["username"], e["action"], e["status"]) for e in r["entries"]]
check("audit has the blacklist add by the operator", ("e2e_oper", "POST /squid/blacklist", 200) in acts)
check("audit has the refused config write (403)", ("e2e_oper", "PUT /squid/config", 403) in acts)
check("audit has failed + successful logins", any(a[1] == "LOGIN" and a[2] == 401 for a in acts) and any(a[1] == "LOGIN" and a[2] == 200 for a in acts))
blob = json.dumps(r)
check("no password appears in the audit log", not any(p in blob for p in [ORIG_PW, TMP_PW, "proxy-pass-1", "viewer-pass-123", "operator-pass-123"]))

print("== account lockout")
victim = Client()
for _ in range(5):
    victim.call("POST", "/auth/login", {"username": "e2e_viewer", "password": "wrong-password-!"})
s, r = Client().call("POST", "/auth/login", {"username": "e2e_viewer", "password": "viewer-NEW-pass-9"})
check("locked after 5 failures (even with the right password)", s == 429, f"{s} {r}")

# ------------------------------------------------------------------ cleanup
print("== cleanup: remove test accounts, put the admin back to its first-start state")
_, users = admin.call("GET", "/panel/users")
for u in users["users"]:
    if u["username"].startswith("e2e_"):
        admin.call("DELETE", f"/panel/users/{u['id']}")
me = admin.call("GET", "/auth/me")[1]["user"]
s, _ = admin.call("POST", f"/panel/users/{me['id']}/password", {"password": ORIG_PW})
check("admin reset to the original one-time password (must change at next login)", s == 200)
s, r = Client().call("POST", "/auth/login", {"username": "admin", "password": ORIG_PW})
check("original password works again and forces a change", s == 200 and r["user"]["must_change_password"], str(r))

print()
print("ALL PASSED" if not fails else "FAILED: " + ", ".join(fails))
sys.exit(1 if fails else 0)
