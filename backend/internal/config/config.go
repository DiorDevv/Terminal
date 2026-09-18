package config

import "os"

type Config struct {
	Port            string
	JWTSecret       string
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
	AdminUsername   string
	AdminPassword   string
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func Load() Config {
	return Config{
		Port:            envOr("PORT", "8080"),
		JWTSecret:       envOr("JWT_SECRET", "dev-secret-change-me"),
		DBPath:          envOr("DB_PATH", "./data/squidadmin.db"),
		SquidConfPath:   envOr("SQUID_CONF_PATH", "/etc/squid/squid.conf"),
		SquidAccessLog:  envOr("SQUID_ACCESS_LOG", "/var/log/squid/access.log"),
		SquidBin:        envOr("SQUID_BIN", "squid"),
		BlacklistPath:   envOr("SQUID_BLACKLIST_PATH", "/etc/squid/blocked_sites.txt"),
		BlacklistACL:    envOr("SQUID_BLACKLIST_ACL", "blocked_sites"),
		PasswdPath:      envOr("SQUID_PASSWD_PATH", "/etc/squid/passwd"),
		AuthACL:         envOr("SQUID_AUTH_ACL", "authenticated_users"),
		HtpasswdBin:     envOr("HTPASSWD_BIN", "htpasswd"),
		BasicAuthHelper: envOr("SQUID_BASIC_AUTH_HELPER", "/usr/lib/squid/basic_ncsa_auth"),
		AdminUsername:   envOr("ADMIN_USERNAME", "admin"),
		AdminPassword:   envOr("ADMIN_PASSWORD", "admin123"),
	}
}
