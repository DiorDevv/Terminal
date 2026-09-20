#!/usr/bin/env python3
"""Phase 3 end-to-end: squid settings through the panel, against a real squid
running under systemd, with the panel installed by deploy/install.sh.

Run on the host as root:

    E2E_ADMIN_PW='<admin password>' python3 deploy/e2e/phase3.py

E2E_ADMIN_PW is the admin's current password. If that account still has a
one-time password (must change at first login) the script changes it for the
run and resets it to E2E_ADMIN_PW at the end. Everything the script changes in
squid.conf is undone at the end, and it asserts the file is then byte-for-byte
what it was before. Expect "ALL PASSED".
"""
import http.cookiejar, json, os, subprocess, sys, time, urllib.error, urllib.request

API = "http://localhost:8080/api"
ADMIN_PW = os.environ["E2E_ADMIN_PW"]
TMP_PW = "e2e-temporary-Pass-42"
PROXY = "http://127.0.0.1:3128"
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


def wait_for(pred, timeout=15, step=0.2):
    t0 = time.time()
    while time.time() - t0 < timeout:
        v = pred()
        if v:
            return v
        time.sleep(step)
    return False


def listening(port):
    return f":{port} " in sh("ss -ltn")


def via_header():
    return sh(f"curl -s -m 10 -D - -o /dev/null -x {PROXY} http://example.org/ | grep -i '^via:'")


def settings(cl):
    s, r = cl.call("GET", "/squid/settings")
    assert s == 200, (s, r)
    return {x["key"]: x for x in r["settings"]}


def put(cl, values=None, reset=None, dry=False):
    body = {}
    if values is not None:
        body["values"] = values
    if reset is not None:
        body["reset"] = reset
    return cl.call("PUT", "/squid/settings" + ("?dry_run=1" if dry else ""), body)


ORIG = conf()
admin = Client()
must_reset = False

# ---------------------------------------------------------------------- login
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

print("== read the current settings")
cur = settings(admin)
check("all settings listed", len(cur) >= 14, str(len(cur)))
check("http_port comes from the stock squid.conf", cur["http_port"]["source"] == "squid.conf" and cur["http_port"]["values"] == ["3128"], str(cur["http_port"]))
check("unset settings report their default", cur["cache_mem"]["source"] == "default" and cur["cache_mem"]["default"] == "256 MB", str(cur["cache_mem"]))
check("values are lists, never null", all(isinstance(v["values"], list) for v in cur.values()))

print("== validation")
s, r = put(admin, {"cache_mem": ["lots"], "via": ["maybe"], "http_port": ["70000"]})
check("bad values rejected with a message per field", s == 422 and set(r.get("fields", {})) == {"cache_mem", "via", "http_port"}, str(r))
check("squid.conf untouched by a rejected update", conf() == ORIG)
s, r = put(admin, {"never_direct": ["allow all"]})
check("never_direct without an upstream proxy rejected", s == 422 and "never_direct" in r.get("fields", {}), str(r))
s, r = put(admin, {"http_port": []})
check("http_port cannot be emptied", s == 422, str(r))

print("== dry run")
s, r = put(admin, {"cache_mem": ["64 MB"]}, dry=True)
check("dry run lists the change", s == 200 and r["dry_run"] and r["changes"] == [{"key": "cache_mem", "from": [], "to": ["64 MB"], "restart": False}], str(r))
check("dry run writes nothing", conf() == ORIG)

print("== settings that only need a reload")
before_via = via_header()
print("    Via header before:", before_via or "(none)")
s, r = put(admin, {
    "via": ["off"], "forwarded_for": ["delete"], "cache_mem": ["64 MB"],
    "visible_hostname": ["E2E-Proxy.lan"], "connect_timeout": ["45 seconds"],
    "dns_nameservers": ["1.1.1.1", "8.8.8.8"], "shutdown_lifetime": ["3 seconds"],
})
check("applied and reloaded", s == 200 and r["reloaded"] is True and not r["restart_required"], str(r))
check("all seven reported as changed", len(r.get("changed", [])) == 7, str(r.get("changed")))
c = conf()
check("panel block written, values normalised", "visible_hostname e2e-proxy.lan" in c and "connect_timeout 45 seconds" in c and "dns_nameservers 1.1.1.1 8.8.8.8" in c)
check("real squid still parses the file", parse_ok())
check("squid survived the reload", squid_up())
after_via = wait_for(lambda: not via_header(), timeout=5)
check("`via off` really removed the Via header", bool(before_via) and after_via, f"before={before_via!r}")
resolved = sh(f"curl -s -m 15 -o /dev/null -w '%{{http_code}}' -x {PROXY} http://example.org/")
print("    example.org through the proxy with the new DNS servers ->", resolved, "(informational: depends on this network)")

print("== unchanged values are not re-written")
s, r = put(admin, {"cache_mem": ["64 MB"], "via": ["off"]})
check("re-saving identical values changes nothing", s == 200 and r["changed"] == [] and "reloaded" not in r, str(r))

print("== http_port is applied by a reload")
check("3129 is not open yet", not listening(3129))
s, r = put(admin, {"http_port": ["3128", "3129"]})
check("http_port change reloaded (no restart needed)", s == 200 and r["reloaded"] is True and not r["restart_required"], str(r))
check("squid now listens on 3129 as well", wait_for(lambda: listening(3129), 8) and listening(3128))
check("stock http_port line was commented out, not duplicated", "\n# squidadmin-disabled: http_port 3128\n" in conf() and conf().count("\nhttp_port 3128\n") == 1)
s, r = put(admin, {"http_port": ["3128"]})
check("removing the extra port reloads too", s == 200 and r["reloaded"] is True, str(r))
check("3129 is closed again", wait_for(lambda: not listening(3129), 8) and listening(3128))

print("== shutdown_lifetime really shortens stop")
t0 = time.time()
s, r = admin.call("POST", "/squid/service/stop")
stopped = wait_for(lambda: sh("systemctl is-active squid") == "inactive", 40, 0.25)
took = time.time() - t0
print(f"    stop took {took:.1f}s (was ~29s with the default 30 s)")
check("squid stops in well under the old 30s", bool(stopped) and took < 15, f"{took:.1f}s")
s, r = admin.call("POST", "/squid/service/start")
check("started again", s == 200 and wait_for(squid_up, 15))

print("== cache_dir needs a restart (and gets a verified one)")
cache = "/var/spool/squid/e2e-cache"
sh(f"rm -rf {cache}")
s, r = put(admin, {"cache_dir": [f"ufs {cache} 100 16 256"]})
check("saved but flagged restart_required, and NOT reloaded", s == 200 and r["restart_required"] is True and "reloaded" not in r, str(r))
s, info = admin.call("GET", "/squid/service")
check("service info shows the pending restart", info.get("restart_pending") is True, str(info))
check("nothing happened to the running squid yet", squid_up() and not os.path.isdir(cache))
t0 = time.time()
s, r = admin.call("POST", "/squid/settings/restart")
print(f"    verified restart took {time.time() - t0:.1f}s")
check("verified restart succeeded", s == 200 and r["service"]["running"], str(r))
check("squid created the cache directory on start", os.path.isdir(cache), cache)
s, info = admin.call("GET", "/squid/service")
check("pending flag cleared after the restart", info.get("restart_pending") is False, str(info))
good_conf = conf()

print("== a cache_dir squid cannot create is rolled back automatically")
bad_dir = "/var/e2e-nocreate/cache"
s, r = put(admin, {"cache_dir": [f"ufs {bad_dir} 100 16 256"]})
check("parses fine, saved, restart required", s == 200 and r["restart_required"], str(r))
check("(squid's parser really accepts it — only a restart reveals the problem)", parse_ok())
t0 = time.time()
s, r = admin.call("POST", "/squid/settings/restart")
print(f"    failed restart + rollback took {time.time() - t0:.1f}s ->", str(r)[:170])
check("restart reported failure and the rollback", s == 422 and "restored automatically" in r.get("error", ""), str(r))
check("squid is running again", wait_for(squid_up, 30))
check("config is back to the last good one", conf() == good_conf)
s, info = admin.call("GET", "/squid/service")
check("no restart pending any more", info.get("restart_pending") is False, str(info))
s, h = admin.call("GET", "/squid/history")
comments = [v["comment"] for v in h["versions"]]
check("history recorded settings changes and the rollback", any(c.startswith("settings:") for c in comments) and any("rollback" in c for c in comments), str(comments[:6]))

print("== upstream proxy")
s, r = put(admin, {"cache_peer": ["up.e2e.invalid parent 3128 0 no-query default"]})
check("cache_peer accepted and squid survives the reload", s == 200 and r["reloaded"] is True and squid_up(), str(r))
s, r = put(admin, {"cache_peer": ["up.e2e.invalid parent 3128 0 login=u:p"]})
check("credentials in cache_peer refused", s == 422 and "cache_peer" in r.get("fields", {}), str(r))
s, r = put(admin, {"never_direct": ["allow all"]})
check("never_direct now allowed (a peer exists)", s == 200, str(r))
s, r = put(admin, {"cache_peer": []})
check("…but the peer cannot be removed while it depends on it", s == 422 and "never_direct" in r.get("fields", {}), str(r))
s, r = put(admin, {}, reset=["never_direct", "cache_peer"])
check("reset both", s == 200 and r["reloaded"] is True, str(r))

print("== put everything back")
allkeys = list(cur.keys())
s, r = put(admin, reset=allkeys)
check("reset all settings", s == 200, str(r))
if r.get("restart_required"):
    s, r = admin.call("POST", "/squid/settings/restart")
    check("verified restart after removing cache_dir", s == 200, str(r))
elif r.get("reloaded") is not True:
    check("reload after reset", False, str(r))
check("squid.conf is byte-for-byte what it was before this test", conf() == ORIG,
      "differs" if conf() != ORIG else "")
check("squid up and parsing", squid_up() and parse_ok())
check("panel block gone", "squidadmin: settings" not in conf() and "squidadmin-disabled" not in conf())
sh(f"rm -rf {cache}")

print("== audit")
s, a = admin.call("GET", "/audit?limit=200")
acts = [(e["action"], e["status"]) for e in a["entries"]]
check("audit has the settings changes", ("PUT /squid/settings", 200) in acts and ("PUT /squid/settings", 422) in acts)
check("audit has the verified restarts", ("POST /squid/settings/restart", 200) in acts and ("POST /squid/settings/restart", 422) in acts)

if must_reset:
    # Restored by the atexit hook after the report, so a crash cannot skip it.
    print("(the admin password is restored to its original one-time value on exit)")

print()
print("ALL PASSED" if not fails else "FAILED: " + ", ".join(fails))
sys.exit(1 if fails else 0)
