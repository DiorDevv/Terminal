package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/api/handlers"
	"squidadmin/backend/internal/auth"
)

type Deps struct {
	AuthSvc             *auth.Service
	AuthHandler         *handlers.AuthHandler
	SquidHandler        *handlers.SquidHandler
	BlacklistHandler    *handlers.BlacklistHandler
	UsersHandler        *handlers.UsersHandler
	NetworkHandler      *handlers.NetworkHandler
	RestrictionsHandler *handlers.RestrictionsHandler
	GroupsHandler       *handlers.GroupsHandler
	FrontendOrigin      string
}

func NewRouter(d Deps) *gin.Engine {
	r := gin.Default()

	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", d.FrontendOrigin)
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	loginLimiter := NewLoginRateLimiter(10, time.Minute)
	r.POST("/api/auth/login", loginLimiter.Middleware(), d.AuthHandler.Login)

	protected := r.Group("/api")
	protected.Use(RequireAuth(d.AuthSvc))
	{
		protected.PUT("/auth/password", d.AuthHandler.ChangePassword)
		protected.GET("/squid/config", d.SquidHandler.GetConfig)
		protected.PUT("/squid/config", d.SquidHandler.UpdateConfig)
		protected.POST("/squid/reconfigure", d.SquidHandler.Reconfigure)
		protected.GET("/squid/status", d.SquidHandler.Status)
		protected.GET("/squid/logs/stream", d.SquidHandler.StreamLogs)

		protected.GET("/squid/blacklist", d.BlacklistHandler.List)
		protected.POST("/squid/blacklist", d.BlacklistHandler.Add)
		protected.DELETE("/squid/blacklist/:domain", d.BlacklistHandler.Remove)

		protected.GET("/squid/users", d.UsersHandler.List)
		protected.POST("/squid/users", d.UsersHandler.Add)
		protected.DELETE("/squid/users/:username", d.UsersHandler.Remove)

		protected.GET("/squid/lan-access", d.NetworkHandler.GetLANAccess)
		protected.PUT("/squid/lan-access", d.NetworkHandler.SetLANAccess)

		protected.GET("/squid/restrictions", d.RestrictionsHandler.List)
		protected.POST("/squid/restrictions", d.RestrictionsHandler.Create)
		protected.DELETE("/squid/restrictions/:id", d.RestrictionsHandler.Delete)

		protected.GET("/squid/groups", d.GroupsHandler.List)
		protected.POST("/squid/groups", d.GroupsHandler.Create)
		protected.DELETE("/squid/groups/:id", d.GroupsHandler.Delete)
	}

	return r
}
