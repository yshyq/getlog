package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"log-download-portal/internal/agent"
	"log-download-portal/internal/api"
	"log-download-portal/internal/audit"
	"log-download-portal/internal/auth"
	"log-download-portal/internal/config"
	"log-download-portal/internal/discovery"
	"log-download-portal/internal/web"
)

func main() {
	configPath := flag.String("config", envOr("PORTAL_CONFIG", "configs/portal.example.yaml"), "path to portal YAML config")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	resolver, err := discovery.NewResolver(ctx, cfg)
	if err != nil {
		log.Fatalf("create discovery resolver: %v", err)
	}
	go resolver.Start(ctx)

	sessions := auth.NewSessionManager(cfg.Auth)
	auditor := audit.NewJSONLogger(os.Stdout)
	agentClient := agent.NewClient(cfg.Agent, cfg.FileList, cfg.Download)

	handler := api.NewServer(api.Dependencies{
		Config:    cfg,
		Sessions:  sessions,
		Discovery: resolver,
		Agent:     agentClient,
		Auditor:   auditor,
		Assets:    web.Assets(),
	})

	srv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           handler,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout.Duration,
		IdleTimeout:       90 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		log.Printf("log portal listening on %s", cfg.Server.Listen)
		errs <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-errs:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server failed: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
