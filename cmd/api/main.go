package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
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
	"whereis.cam/internal/netutil"
	"whereis.cam/internal/pub"
	"whereis.cam/internal/storage"
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
	detector.SetMinDwellTime(cfg.MinDwellTime)

	store, err := storage.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to initialize SQLite storage: %v", err)
	}
	defer store.Close()

	// Auto-migrate legacy JSON files if database is freshly created
	store.ImportLegacyJSON(cfg.SubscribersPath, cfg.VAPIDKeysPath, cfg.PushSubsPath)

	alertsService, err := alerts.NewService(store, cfg.ResendAPIKey, cfg.ResendFrom, cfg.BaseURL)
	if err != nil {
		log.Printf("Warning: failed to initialize alerts service: %v", err)
	}

	pushService, err := alerts.NewPushService(store, cfg.ResendFrom)
	if err != nil {
		log.Printf("Warning: failed to initialize push service: %v", err)
	}

	server, err := handlers.NewServer(hassClient, detector, alertsService, pushService, store, cfg.AdminPassword)
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}

	trustedProxies, err := netutil.ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		log.Fatalf("Invalid TRUSTED_PROXIES: %v", err)
	}
	server.SetTrustedProxies(trustedProxies)
	if cfg.AdminPassword == "" {
		log.Printf("Warning: ADMIN_PASSWORD not set; dashboard password login is disabled")
	}

	// Start background watcher to broadcast email alerts autonomously
	server.StartBackgroundPoller(context.Background(), 30*time.Second)

	// Apply middleware stack
	handler := server.Handler()
	handler = middleware.HitTracker(store)(handler)
	handler = middleware.Recoverer(handler)
	handler = middleware.SecurityHeaders(handler)
	handler = middleware.Logging(handler)

	httpServer := &http.Server{
		Addr:         net.JoinHostPort(cfg.BindAddr, cfg.Port),
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
		log.Printf("Server listening on %s (tracking %s)", httpServer.Addr, cfg.PersonEntity)
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
