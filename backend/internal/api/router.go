package api

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/api/handlers"
	"squidadmin/backend/internal/audit"
	"squidadmin/backend/internal/auth"
)

type Deps struct {
	AuthSvc             *auth.Service
	Audit               *audit.Store
	AuthHandler         *handlers.AuthHandler
	PanelUsersHandler   *handlers.PanelUsersHandler
	AuditHandler        *handlers.AuditHandler
	SquidHandler        *handlers.SquidHandler
	BlacklistHandler    *handlers.BlacklistHandler
	UsersHandler        *handlers.UsersHandler
	NetworkHandler      *handlers.NetworkHandler
	RestrictionsHandler *handlers.RestrictionsHandler
	GroupsHandler       *handlers.GroupsHandler
	ServiceHandler      *handlers.ServiceHandler
	HistoryHandler      *handlers.HistoryHandler
	SettingsHandler     *handlers.SettingsHandler
	AccessHandler       *handlers.AccessHandler
	BlocklistsHandler   *handlers.BlocklistsHandler
	MonitorHandler      *handlers.MonitorHandler
	UserPolicyHandler   *handlers.UserPolicyHandler

	// AllowedOrigins are the browser origins allowed to call the API.
	AllowedOrigins []string
	// TrustedProxies are the reverse proxies whose X-Forwarded-For is
	// believed; everything else can't spoof the client IP.
	TrustedProxies []string
}

func NewRouter(d Deps) *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	if err := r.SetTrustedProxies(d.TrustedProxies); err != nil {
		log.Printf("warning: invalid trusted proxies %v: %v", d.TrustedProxies, err)
	}

	r.Use(SecurityHeaders(), LimitBody(MaxBodyBytes), CORS(d.AllowedOrigins), CSRF(d.AllowedOrigins))

	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	loginLimiter := NewLoginRateLimiter(10, time.Minute)
	r.POST("/api/auth/login", loginLimiter.Middleware(), d.AuthHandler.Login)

	// Everything below needs a valid session. Audit sits right after auth so
	// even refused requests (403) are recorded against the acting user; the
	// password gate and rate limit follow.
	authed := r.Group("/api",
		SessionAuth(d.AuthSvc),
		Audit(d.Audit),
		RequirePasswordChange("/api/auth/me", "/api/auth/logout", "/api/auth/password"),
		NewAPIRateLimiter(600, time.Minute).Middleware(),
	)

	// Any signed-in user, whatever the role.
	authed.GET("/auth/me", d.AuthHandler.Me)
	authed.POST("/auth/logout", d.AuthHandler.Logout)
	authed.PUT("/auth/password", d.AuthHandler.ChangePassword)
	authed.GET("/auth/sessions", d.AuthHandler.Sessions)
	authed.DELETE("/auth/sessions/:id", d.AuthHandler.RevokeSession)

	// viewer: read-only overview.
	viewer := authed.Group("", RequireRole(auth.RoleViewer))
	{
		viewer.GET("/squid/status", d.SquidHandler.Status)
		viewer.GET("/squid/service", d.ServiceHandler.Info)
		viewer.GET("/squid/lan-access", d.NetworkHandler.GetLANAccess)
		viewer.GET("/squid/blacklist", d.BlacklistHandler.List)
		viewer.GET("/squid/users", d.UsersHandler.List)
		viewer.GET("/squid/restrictions", d.RestrictionsHandler.List)
		viewer.GET("/squid/groups", d.GroupsHandler.List)

		viewer.GET("/squid/access/types", d.AccessHandler.Types)
		viewer.GET("/squid/access/acls", d.AccessHandler.ListACLs)
		viewer.GET("/squid/access/rules", d.AccessHandler.ListRules)
		viewer.GET("/squid/access/policy", d.AccessHandler.Policy)
		viewer.GET("/squid/blocklists", d.BlocklistsHandler.List)
	}

	// operator: everyday proxy management.
	operator := authed.Group("", RequireRole(auth.RoleOperator))
	{
		operator.GET("/squid/logs/stream", d.SquidHandler.StreamLogs)

		// Account settings show usage per person, hence operator and up.
		operator.GET("/squid/proxy-users", d.UserPolicyHandler.Users)
		operator.PUT("/squid/proxy-users/:username", d.UserPolicyHandler.UpdateUser)
		operator.GET("/squid/proxy-users-export", d.UserPolicyHandler.Export)
		operator.POST("/squid/proxy-users-import", d.UserPolicyHandler.Import)
		operator.GET("/squid/user-groups", d.UserPolicyHandler.Groups)
		operator.POST("/squid/user-groups", d.UserPolicyHandler.CreateGroup)
		operator.DELETE("/squid/user-groups/:id", d.UserPolicyHandler.DeleteGroup)
		operator.PUT("/squid/user-groups/:id/members", d.UserPolicyHandler.SetMembers)
		operator.GET("/squid/limits", d.UserPolicyHandler.Limits)
		operator.POST("/squid/limits", d.UserPolicyHandler.CreateLimit)
		operator.PUT("/squid/limits/:id", d.UserPolicyHandler.UpdateLimit)
		operator.DELETE("/squid/limits/:id", d.UserPolicyHandler.DeleteLimit)

		// Statistics and logs show who visited what, hence operator and up.
		operator.GET("/monitor/summary", d.MonitorHandler.Summary)
		operator.GET("/monitor/top", d.MonitorHandler.Top)
		operator.GET("/monitor/denied", d.MonitorHandler.Denied)
		operator.GET("/monitor/live", d.MonitorHandler.Live)
		operator.GET("/monitor/logs", d.MonitorHandler.Logs)
		operator.GET("/monitor/logs/:name", d.MonitorHandler.LogTail)
		// Viewing the settings is fine for operators; changing them is admin-only.
		operator.GET("/squid/settings", d.SettingsHandler.Get)
		operator.POST("/squid/reconfigure", d.SquidHandler.Reconfigure)
		// The handler enforces a stricter role for stop/restart/enable/disable.
		operator.POST("/squid/service/:action", d.ServiceHandler.Action)

		operator.POST("/squid/blacklist", d.BlacklistHandler.Add)
		operator.DELETE("/squid/blacklist/:domain", d.BlacklistHandler.Remove)

		operator.POST("/squid/users", d.UsersHandler.Add)
		operator.DELETE("/squid/users/:username", d.UsersHandler.Remove)

		operator.PUT("/squid/lan-access", d.NetworkHandler.SetLANAccess)

		operator.POST("/squid/restrictions", d.RestrictionsHandler.Create)
		operator.DELETE("/squid/restrictions/:id", d.RestrictionsHandler.Delete)

		operator.POST("/squid/access/acls", d.AccessHandler.CreateACL)
		operator.PUT("/squid/access/acls/:id", d.AccessHandler.UpdateACL)
		operator.DELETE("/squid/access/acls/:id", d.AccessHandler.DeleteACL)
		operator.POST("/squid/access/rules", d.AccessHandler.CreateRule)
		operator.PUT("/squid/access/rules/:id", d.AccessHandler.UpdateRule)
		operator.DELETE("/squid/access/rules/:id", d.AccessHandler.DeleteRule)
		operator.PUT("/squid/access/order", d.AccessHandler.Reorder)
		operator.POST("/squid/access/test", d.AccessHandler.Test)

		operator.POST("/squid/blocklists", d.BlocklistsHandler.Create)
		operator.PUT("/squid/blocklists/:id", d.BlocklistsHandler.Update)
		operator.DELETE("/squid/blocklists/:id", d.BlocklistsHandler.Delete)
		operator.POST("/squid/blocklists/:id/refresh", d.BlocklistsHandler.Refresh)

		operator.POST("/squid/groups", d.GroupsHandler.Create)
		operator.DELETE("/squid/groups/:id", d.GroupsHandler.Delete)
	}

	// admin: raw config (may contain secrets and can run helpers), history,
	// panel accounts and the audit log.
	admin := authed.Group("", RequireRole(auth.RoleAdmin))
	{
		admin.GET("/squid/config", d.SquidHandler.GetConfig)
		admin.PUT("/squid/config", d.SquidHandler.UpdateConfig)

		admin.PUT("/squid/settings", d.SettingsHandler.Update)
		admin.POST("/squid/settings/restart", d.SettingsHandler.Restart)

		admin.GET("/squid/history", d.HistoryHandler.List)
		admin.GET("/squid/history/:id", d.HistoryHandler.Get)
		admin.POST("/squid/history/:id/restore", d.HistoryHandler.Restore)

		admin.GET("/panel/users", d.PanelUsersHandler.List)
		admin.POST("/panel/users", d.PanelUsersHandler.Create)
		admin.PUT("/panel/users/:id", d.PanelUsersHandler.Update)
		admin.POST("/panel/users/:id/password", d.PanelUsersHandler.ResetPassword)
		admin.DELETE("/panel/users/:id", d.PanelUsersHandler.Delete)

		admin.GET("/audit", d.AuditHandler.List)

		admin.POST("/monitor/logs/rotate", d.MonitorHandler.Rotate)
		admin.GET("/monitor/alerts", d.MonitorHandler.Alerts)
		admin.PUT("/monitor/alerts", d.MonitorHandler.SetAlertConfig)
		admin.POST("/monitor/alerts/test", d.MonitorHandler.TestAlert)
		admin.POST("/monitor/alerts/check", d.MonitorHandler.CheckAlerts)
	}

	return r
}
