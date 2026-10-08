package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/chattergo/chattergo/internal/config"
	"github.com/chattergo/chattergo/internal/infra/repository/postgres"
	webhttp "github.com/chattergo/chattergo/internal/infra/web/http"
	ws "github.com/chattergo/chattergo/internal/infra/web/websocket"
	"github.com/chattergo/chattergo/internal/pkg/auth"
	pkgmw "github.com/chattergo/chattergo/internal/pkg/middleware"
	applog "github.com/chattergo/chattergo/internal/pkg/logger"
	"github.com/chattergo/chattergo/internal/usecase"
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func runMigrations(dsn, dir string) error {
	m, err := migrate.New("file://"+dir, dsn)
	if err != nil {
		return fmt.Errorf("migrate new: %w", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

func main() {
	log := applog.New(os.Getenv("LOG_LEVEL"))

	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	if err := runMigrations(cfg.DSN, cfg.MigrationsDir); err != nil {
		log.Error("migrations failed", "err", err)
		os.Exit(1)
	}
	log.Info("migrations applied")

	repo, err := postgres.New(cfg.DSN)
	if err != nil {
		log.Error("db connect", "err", err)
		os.Exit(1)
	}
	defer repo.Close()

	jwtMgr := auth.NewManager(cfg.JWTSecret, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
	authUC := usecase.NewAuth(postgres.NewUserRepo(repo), postgres.NewRefreshTokenRepo(repo), jwtMgr)

	hub := ws.NewHub(log)
	chatUC := usecase.NewChat(postgres.NewChatRepo(repo), postgres.NewUserRepo(repo), postgres.NewMessageRepo(repo), hub)
	hub.ChatUC = chatUC

	httpH := webhttp.NewHandler(authUC, chatUC, jwtMgr, log)

	r := chi.NewRouter()
	r.Use(chimw.Recoverer)
	r.Use(chimw.RealIP)
	r.Use(pkgmw.CORS(cfg.CorsAllowedOrigins))
	limiter := pkgmw.NewRateLimiter(20, 60)
	r.Use(limiter.Middleware)

	httpH.RegisterRoutes(r)
	r.Get("/ws", ws.UpgradeHandler(hub, jwtMgr))
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Serve the frontend static files at /.
	frontendDir := getenv("FRONTEND_DIR", "frontend")
	if st, err := os.Stat(frontendDir); err == nil && st.IsDir() {
		r.Handle("/*", http.StripPrefix("/", http.FileServer(http.Dir(frontendDir))))
	} else {
		log.Warn("frontend directory not found, skipping static serving", "dir", frontendDir)
	}

	hubCtx, hubStop := context.WithCancel(context.Background())
	go hub.Run(hubCtx)

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: r, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		log.Info("server listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Info("shutting down")

	shCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shCtx); err != nil {
		log.Error("http shutdown", "err", err)
	}
	hubStop()
	log.Info("bye")
}
