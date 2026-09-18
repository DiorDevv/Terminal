package main

import (
	"log"

	"github.com/joho/godotenv"

	"squidadmin/backend/internal/api"
	"squidadmin/backend/internal/api/handlers"
	"squidadmin/backend/internal/auth"
	"squidadmin/backend/internal/config"
	"squidadmin/backend/internal/db"
	"squidadmin/backend/internal/squid"
)

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

	authSvc := auth.NewService(conn, cfg.JWTSecret)
	if err := authSvc.EnsureUser(cfg.AdminUsername, cfg.AdminPassword); err != nil {
		log.Fatalf("bootstrap admin user: %v", err)
	}

	squidMgr := squid.NewManager(cfg.SquidConfPath, cfg.SquidBin)

	blacklistMgr := squid.NewBlacklistManager(cfg.BlacklistPath, cfg.BlacklistACL, squidMgr)
	if err := blacklistMgr.EnsureDirectives(); err != nil {
		log.Printf("warning: could not ensure blacklist directives in squid.conf: %v", err)
	}

	userMgr := squid.NewUserManager(cfg.PasswdPath, cfg.AuthACL, cfg.BasicAuthHelper, cfg.HtpasswdBin, squidMgr)
	restrictionMgr := squid.NewRestrictionManager(conn, squidMgr)
	groupMgr := squid.NewGroupManager(conn)

	router := api.NewRouter(api.Deps{
		AuthSvc:             authSvc,
		AuthHandler:         handlers.NewAuthHandler(authSvc),
		SquidHandler:        handlers.NewSquidHandler(squidMgr, cfg.SquidAccessLog),
		BlacklistHandler:    handlers.NewBlacklistHandler(blacklistMgr, squidMgr),
		UsersHandler:        handlers.NewUsersHandler(userMgr, squidMgr),
		NetworkHandler:      handlers.NewNetworkHandler(squidMgr),
		RestrictionsHandler: handlers.NewRestrictionsHandler(restrictionMgr, squidMgr),
		GroupsHandler:       handlers.NewGroupsHandler(groupMgr, restrictionMgr, squidMgr),
		FrontendOrigin:      "http://localhost:5173",
	})

	log.Printf("squidadmin backend listening on :%s", cfg.Port)
	if err := router.Run(":" + cfg.Port); err != nil {
		log.Fatalf("server: %v", err)
	}
}
