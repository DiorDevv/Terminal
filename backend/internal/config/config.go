package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port string
	// BindAddr is the interface to listen on; empty means all of them. Set it to
	// 127.0.0.1 when a reverse proxy in front of the panel terminates HTTPS.
	BindAddr        string
	DBPath          string
	SquidConfPath   string
	SquidAccessLog  string
	SquidBin        string
	BlacklistPath   string
	BlacklistACL    string
	PasswdPath      string
	AuthACL         string
	HtpasswdBin     string
	BasicAuthHelper string

	// AdminUsername/AdminPassword bootstrap the first panel account, only
	// when the panel has no users yet. If AdminPassword is empty or weak a
	// random one is generated and printed once.
	AdminUsername string
	AdminPassword string

	// FrontendOrigin is a comma-separated list of browser origins allowed to
	// call the API (CORS, CSRF and WebSocket checks).
	FrontendOrigin string
	// CookieSecure forces the Secure flag on the session cookie. It is also
	// set automatically for requests that arrive over HTTPS.
	CookieSecure bool
	// TrustedProxies are the reverse proxies whose X-Forwarded-For is
	// believed (comma-separated IPs/CIDRs). Loopback by default.
	TrustedProxies string

	// BlocklistDir holds the downloaded block lists; squid reads them by path.
	BlocklistDir string
	// BlocklistAllowLoopback lets lists be fetched from 127.0.0.1 (a list
	// served on this host). Cloud metadata / link-local addresses stay blocked.
	BlocklistAllowLoopback bool

	// SquidLogDir is squid's log directory (listed and tailed by the panel);
	// it defaults to the directory of SquidAccessLog.
	SquidLogDir string
	// SquidManagerURL is squid's cache manager "info" page, read for live data.
	SquidManagerURL string
	// StatsRetentionDays is how long hourly statistics are kept.
	StatsRetentionDays int
	// AlertCheckSeconds is how often alert conditions are evaluated.
	AlertCheckSeconds int
	// MonitorDiskPaths are the filesystems whose free space is watched
	// (comma-separated); by default the log directory and the panel's data.
	MonitorDiskPaths string
	// AlertAllowLoopback lets alert webhooks and SMTP reach this host.
	AlertAllowLoopback bool
	// PolicyCheckSeconds is how often account expiry and daily quotas are
	// re-evaluated (a quota is enforced at most this long after it is reached).
	PolicyCheckSeconds int

	SquidService string
	SystemctlBin string
	// SquidManager is "auto", "systemd" or "direct".
	SquidManager string
	// UseSudo runs the few privileged squid/systemctl commands through
	// `sudo -n`, so the panel itself can run as an unprivileged user.
	UseSudo bool
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func Load() Config {
	return Config{
		Port:                   envOr("PORT", "8080"),
		BindAddr:               os.Getenv("BIND_ADDR"),
		DBPath:                 envOr("DB_PATH", "./data/squidadmin.db"),
		SquidConfPath:          envOr("SQUID_CONF_PATH", "/etc/squid/squid.conf"),
		SquidAccessLog:         envOr("SQUID_ACCESS_LOG", "/var/log/squid/access.log"),
		SquidBin:               envOr("SQUID_BIN", "squid"),
		BlacklistPath:          envOr("SQUID_BLACKLIST_PATH", "/etc/squid/blocked_sites.txt"),
		BlacklistACL:           envOr("SQUID_BLACKLIST_ACL", "blocked_sites"),
		PasswdPath:             envOr("SQUID_PASSWD_PATH", "/etc/squid/passwd"),
		AuthACL:                envOr("SQUID_AUTH_ACL", "authenticated_users"),
		HtpasswdBin:            envOr("HTPASSWD_BIN", "htpasswd"),
		BasicAuthHelper:        envOr("SQUID_BASIC_AUTH_HELPER", "/usr/lib/squid/basic_ncsa_auth"),
		AdminUsername:          envOr("ADMIN_USERNAME", "admin"),
		AdminPassword:          os.Getenv("ADMIN_PASSWORD"),
		FrontendOrigin:         envOr("FRONTEND_ORIGIN", "http://localhost:5173"),
		CookieSecure:           envBool("COOKIE_SECURE", false),
		TrustedProxies:         envOr("TRUSTED_PROXIES", "127.0.0.1,::1"),
		BlocklistDir:           envOr("SQUID_BLOCKLIST_DIR", "/etc/squid/blocklists"),
		BlocklistAllowLoopback: envBool("BLOCKLIST_ALLOW_LOOPBACK", false),
		SquidLogDir:            os.Getenv("SQUID_LOG_DIR"),
		SquidManagerURL:        envOr("SQUID_MANAGER_URL", "http://127.0.0.1:3128/squid-internal-mgr/info"),
		StatsRetentionDays:     envInt("STATS_RETENTION_DAYS", 90),
		AlertCheckSeconds:      envInt("ALERT_CHECK_SECONDS", 60),
		MonitorDiskPaths:       os.Getenv("MONITOR_DISK_PATHS"),
		AlertAllowLoopback:     envBool("ALERT_ALLOW_LOOPBACK", false),
		PolicyCheckSeconds:     envInt("POLICY_CHECK_SECONDS", 30),
		SquidService:           envOr("SQUID_SERVICE", "squid"),
		SystemctlBin:           envOr("SYSTEMCTL_BIN", "systemctl"),
		SquidManager:           envOr("SQUID_MANAGER", "auto"),
		UseSudo:                envBool("SQUID_USE_SUDO", false),
	}
}
