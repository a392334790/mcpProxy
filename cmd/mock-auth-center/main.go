package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mcpProxy/internal/mockauth"
)

func main() {
	cfg, err := mockauth.LoadConfig()
	if err != nil {
		log.Fatalf("load mock auth config: %v", err)
	}
	handler := mockauth.NewServer(cfg)
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("mock auth center listening on http://%s", cfg.ListenAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen and serve: %v", err)
	}
}
