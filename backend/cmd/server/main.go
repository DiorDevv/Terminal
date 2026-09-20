package main

import (
	"context"
	"crypto/rand"
	"errors"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"squidadmin/backend/internal/api"
	"squidadmin/backend/internal/api/handlers"
	"squidadmin/backend/internal/api/origin"
	"squidadmin/backend/internal/audit"
	"squidadmin/backend/internal/auth"
	"squidadmin/backend/internal/config"
	"squidadmin/backend/internal/db"
	"squidadmin/backend/internal/monitor"
	"squidadmin/backend/internal/squid"
	"squidadmin/backend/internal/web"
	"squidadmin/backend/internal/ws"
)

// legacyDefaultPassword is what earlier versions shipped as the built-in
// admin password. Any account still using it is forced to pick a new one.
const legacyDefaultPassword = "admin123"

// version is stamped at build time (scripts/build.sh: -X main.version=...).
var version = "dev"

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, using environment/defaults")
	}

	cfg := config.Load()

	conn, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer conn.Close()

	authSvc := auth.NewService(conn)
	bootstrapAdmin(authSvc, cfg)

	auditStore := audit.NewStore(conn)
	go pruneAuditLog(auditStore)

	squidMgr := squid.NewManager(cfg.SquidConfPath, cfg.SquidBin)
	squidMgr.ServiceName = cfg.SquidService
	squidMgr.SystemctlBin = cfg.SystemctlBin
	squidMgr.Sudo = cfg.UseSudo
	squidMgr.Systemd = useSystemd(cfg)
	squidMgr.VerifyDelay = 3 * time.Second
	squidMgr.SetHistory(squid.NewSQLHistory(conn))
	log.Printf("squid control: manager=%s sudo=%v", map[bool]string{true: "systemd", false: "direct"}[squidMgr.Systemd], squidMgr.Sudo)
	if err := squidMgr.EnsureBaseline(); err != nil {
		log.Printf("warning: could not record baseline squid.conf: %v", err)
	}

	blacklistMgr := squid.NewBlacklistManager(cfg.BlacklistPath, cfg.BlacklistACL, squidMgr)
	if err := blacklistMgr.EnsureDirectives(); err != nil {
		log.Printf("warning: could not ensure blacklist directives in squid.conf: %v", err)
	}

	userMgr := squid.NewUserManager(cfg.PasswdPath, cfg.AuthACL, cfg.BasicAuthHelper, cfg.HtpasswdBin, squidMgr)
	restrictionMgr := squid.NewRestrictionManager(conn, squidMgr)
	groupMgr := squid.NewGroupManager(conn)
	accessMgr := squid.NewAccessManager(conn, squidMgr, cfg.BlocklistDir)
	blocklists := squid.NewBlocklistService(conn, squidMgr, accessMgr, cfg.BlocklistDir, cfg.BlocklistAllowLoopback)

	// Rewrites the managed time-restriction block from the DB. This also
	// migrates blocks written by older versions (which sat after the allow
	// rules and therefore never fired) into the correct position.
	if err := restrictionMgr.Regenerate(); err != nil {
		log.Printf("warning: could not refresh time restrictions in squid.conf: %v", err)
	}

	// The user's access rules go in after the time restrictions (fixed order,
	// see denyStageAnchor), so they are regenerated last.
	if err := accessMgr.Regenerate(); err != nil {
		log.Printf("warning: could not refresh access rules in squid.conf: %v", err)
	}

	// Block lists refresh on their own schedule; a first pass shortly after
	// start catches lists that came due while the panel was down.
	go func() {
		time.Sleep(30 * time.Second)
		blocklists.RefreshDue()
		blocklists.Run(context.Background(), 10*time.Minute)
	}()

	// Statistics from access.log and alerting.
	logDir := cfg.SquidLogDir
	if logDir == "" {
		logDir = filepath.Dir(cfg.SquidAccessLog)
	}
	disks := splitList(cfg.MonitorDiskPaths)
	if len(disks) == 0 {
		disks = []string{logDir, filepath.Dir(cfg.DBPath)}
	}
	ingestor := monitor.NewIngestor(conn, cfg.SquidAccessLog)
	statsStore := monitor.NewStore(conn)
	alerts := monitor.NewAlerts(conn, &liveProbe{mgr: squidMgr, ingest: ingestor, disks: disks}, cfg.AlertAllowLoopback)
	stopMonitor := make(chan struct{})
	defer close(stopMonitor)
	go ingestor.Run(stopMonitor, 5*time.Second, time.Duration(cfg.StatsRetentionDays)*24*time.Hour)
	go alerts.Run(stopMonitor, time.Duration(cfg.AlertCheckSeconds)*time.Second)

	// Account settings, user groups and limits. Expiry and daily quotas are
	// re-evaluated on a timer; squid is reloaded only when the set of blocked
	// accounts (or a limit) actually changed.
	userPolicy := squid.NewUserPolicy(conn, squidMgr, userMgr, statsStore.UserBytesSince)
	if changed, err := userPolicy.Sync(); err != nil {
		log.Printf("warning: could not refresh user policy in squid.conf: %v", err)
	} else if changed {
		if err := squidMgr.Reconfigure(); err != nil {
			log.Printf("warning: reload after refreshing user policy failed: %v", err)
		}
	}
	go userPolicy.Run(stopMonitor, time.Duration(cfg.PolicyCheckSeconds)*time.Second, log.Printf)

	origins := origin.ParseList(cfg.FrontendOrigin)

	router := api.NewRouter(api.Deps{
		AuthSvc:             authSvc,
		Audit:               auditStore,
		AuthHandler:         handlers.NewAuthHandler(authSvc, auditStore, cfg.CookieSecure),
		PanelUsersHandler:   handlers.NewPanelUsersHandler(authSvc, auditStore),
		AuditHandler:        handlers.NewAuditHandler(auditStore),
		SquidHandler:        handlers.NewSquidHandler(squidMgr, cfg.SquidAccessLog, ws.NewUpgrader(origins)),
		BlacklistHandler:    handlers.NewBlacklistHandler(blacklistMgr, squidMgr),
		UsersHandler:        handlers.NewUsersHandler(userMgr, squidMgr).WithPolicy(userPolicy),
		UserPolicyHandler:   handlers.NewUserPolicyHandler(userPolicy, squidMgr),
		NetworkHandler:      handlers.NewNetworkHandler(squidMgr),
		RestrictionsHandler: handlers.NewRestrictionsHandler(restrictionMgr, squidMgr),
		GroupsHandler:       handlers.NewGroupsHandler(groupMgr, restrictionMgr, squidMgr),
		ServiceHandler:      handlers.NewServiceHandler(squidMgr),
		HistoryHandler:      handlers.NewHistoryHandler(squidMgr),
		SettingsHandler:     handlers.NewSettingsHandler(squidMgr),
		AccessHandler:       handlers.NewAccessHandler(accessMgr, squidMgr, squid.NewPolicyEnv(cfg.BlacklistACL)),
		BlocklistsHandler:   handlers.NewBlocklistsHandler(blocklists, squidMgr),
		MonitorHandler:      handlers.NewMonitorHandler(statsStore, ingestor, alerts, squidMgr, logDir, cfg.SquidManagerURL, disks),
		Web:                 web.Handler(),
		AllowedOrigins:      origins,
		TrustedProxies:      splitList(cfg.TrustedProxies),
	})

	// gin's Run has no timeouts at all, which lets a client hold a connection
	// open by sending its request one byte a minute. There is deliberately no
	// WriteTimeout: verified restarts and the live-log socket run for minutes.
	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.BindAddr, cfg.Port),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	// systemd stops the service with SIGTERM: finish in-flight requests (a
	// half-applied squid.conf change) before the process goes away.
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	log.Printf("squidadmin %s listening on %s (extra allowed origins: %s)", version, srv.Addr, strings.Join(origins, ", "))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server: %v", err)
	}
	<-ctx.Done()
	time.Sleep(200 * time.Millisecond) // let Shutdown finish before the deferred closes run
	log.Printf("stopped")
}

// bootstrapAdmin creates the first admin on a fresh installation and forces
// a password change on any existing account that still has a known default.
// There is deliberately no built-in default password: without a strong
// ADMIN_PASSWORD a random one is generated and shown once.
func bootstrapAdmin(svc *auth.Service, cfg config.Config) {
	password := cfg.AdminPassword
	mustChange := false
	generated := false

	if password == "" || password == legacyDefaultPassword ||
		auth.ValidatePassword(cfg.AdminUsername, password) != nil {
		password = randomPassword(20)
		mustChange = true
		generated = true
	}

	created, err := svc.Bootstrap(cfg.AdminUsername, password, mustChange)
	if err != nil {
		log.Fatalf("bootstrap admin user: %v", err)
	}
	if created && generated {
		log.Printf("================================================================")
		log.Printf(" First start: admin account created")
		log.Printf("   username: %s", cfg.AdminUsername)
		log.Printf("   password: %s", password)
		log.Printf(" This password is shown only once and must be changed at first login.")
		log.Printf("================================================================")
	}

	known := []string{legacyDefaultPassword}
	if cfg.AdminPassword != "" {
		known = append(known, cfg.AdminPassword)
	}
	if n, err := svc.FlagWeakPasswords(known...); err != nil {
		log.Printf("warning: could not check for default passwords: %v", err)
	} else if n > 0 {
		log.Printf("%d account(s) still use a default password and must change it at next login", n)
	}
}

func randomPassword(n int) string {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, n)
	for i := range out {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			log.Fatalf("generate password: %v", err)
		}
		out[i] = alphabet[idx.Int64()]
	}
	return string(out)
}

// useSystemd resolves SQUID_MANAGER (auto/systemd/direct) to a yes/no.
func useSystemd(cfg config.Config) bool {
	switch strings.ToLower(cfg.SquidManager) {
	case "systemd":
		return true
	case "direct":
		return false
	default:
		return squid.SystemdAvailable(cfg.SystemctlBin)
	}
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func pruneAuditLog(store *audit.Store) {
	for {
		if err := store.Prune(180 * 24 * time.Hour); err != nil {
			log.Printf("warning: audit prune failed: %v", err)
		}
		time.Sleep(24 * time.Hour)
	}
}
