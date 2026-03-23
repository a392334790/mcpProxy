package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mcpProxy/internal/config"
	"mcpProxy/internal/proxy"
	"mcpProxy/internal/session"
	"mcpProxy/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	store, err := storage.NewFileTokenStore(cfg.TokenFile)
	if err != nil {
		log.Fatalf("init token store: %v", err)
	}

	manager, err := session.NewManager(cfg, store)
	if err != nil {
		log.Fatalf("init session manager: %v", err)
	}

	handler := proxy.NewServer(cfg, manager)
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown server: %v", err)
		}
	}()

	log.Printf("mcp auth proxy listening on http://%s", cfg.ListenAddr)
	log.Printf("proxy upstream mcp endpoint: %s", cfg.UpstreamMCPURL)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen and serve: %v", err)
	}
	log.Printf("server stopped")
}
