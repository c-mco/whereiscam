package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"whereis.cam/internal/alerts"
	"whereis.cam/internal/config"
	"whereis.cam/internal/handlers"
	"whereis.cam/internal/homeassistant"
	"whereis.cam/internal/middleware"
	"whereis.cam/internal/pub"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[whereis.cam] ")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	hassClient := homeassistant.NewClient(cfg.HassURL, cfg.HassToken, cfg.CacheTTL)
	detector := pub.NewDetector(cfg.PersonEntity, cfg.PubZones, cfg.PubIconKeywords)

	alertsService, err := alerts.NewService(cfg.SubscribersPath, cfg.ResendAPIKey, cfg.ResendFrom, cfg.BaseURL)
	if err != nil {
		log.Printf("Warning: failed to initialize alerts service: %v", err)
	}

	pushService, err := alerts.NewPushService(cfg.PushSubsPath, cfg.VAPIDKeysPath, cfg.ResendFrom)
	if err != nil {
		log.Printf("Warning: failed to initialize push service: %v", err)
	}

	server, err := handlers.NewServer(hassClient, detector, alertsService, pushService)
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}

	// Start background watcher to broadcast email alerts autonomously
	server.StartBackgroundPoller(context.Background(), 30*time.Second)

	// Apply middleware stack
	handler := server.Handler()
	handler = middleware.Recoverer(handler)
	handler = middleware.SecurityHeaders(handler)
	handler = middleware.Logging(handler)

	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Background connectivity verification
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := hassClient.CheckConnectivity(ctx); err != nil {
			log.Printf("Warning: initial Home Assistant connectivity check failed: %v", err)
		} else {
			log.Printf("Connected to Home Assistant at %s", cfg.HassURL)
		}
	}()

	// Graceful shutdown listener
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Server listening on http://localhost:%s (tracking %s)", cfg.Port, cfg.PersonEntity)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
	fmt.Println("Goodbye!")
}
