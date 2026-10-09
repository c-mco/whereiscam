package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"whereis.cam/internal/alerts"
	"whereis.cam/internal/homeassistant"
	"whereis.cam/internal/pub"
	"whereis.cam/web"
)

// Server handles HTTP traffic for whereis.cam.
type Server struct {
	hassClient    *homeassistant.Client
	detector      *pub.Detector
	alertsService *alerts.Service
	pushService   *alerts.PushService
	tmpl          *template.Template
	mux           *http.ServeMux

	mu           sync.Mutex
	lastPubState bool
	lastPubName  string
}

// NewServer initializes templates, routes, and dependencies.
func NewServer(hassClient *homeassistant.Client, detector *pub.Detector, alertsService *alerts.Service, pushService *alerts.PushService) (*Server, error) {
	tmpl, err := template.ParseFS(web.FS, "templates/index.html")
	if err != nil {
		return nil, err
	}

	s := &Server{
		hassClient:    hassClient,
		detector:      detector,
		alertsService: alertsService,
		pushService:   pushService,
		tmpl:          tmpl,
		mux:           http.NewServeMux(),
	}

	s.routes()
	return s, nil
}

// Handler returns the HTTP handler for the server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	// Static assets from embedded FS
	staticSubFS, err := fs.Sub(web.FS, "static")
	if err == nil {
		fsServer := http.FileServer(http.FS(staticSubFS))
		s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=604800, stale-while-revalidate=86400")
			fsServer.ServeHTTP(w, r)
		})))
	}

	// Service Worker & PWA Manifest from root
	s.mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		data, err := fs.ReadFile(web.FS, "static/sw.js")
		if err != nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		_, _ = w.Write(data)
	})

	s.mux.HandleFunc("GET /manifest.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		data, err := fs.ReadFile(web.FS, "static/manifest.json")
		if err != nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		_, _ = w.Write(data)
	})

	// Favicon shortcut
	s.mux.HandleFunc("GET /favicon.svg", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = "/static/favicon.svg"
		http.Redirect(w, r, "/static/favicon.svg", http.StatusMovedPermanently)
	})
	s.mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = "/static/favicon.svg"
		http.Redirect(w, r, "/static/favicon.svg", http.StatusMovedPermanently)
	})

	// Main web page
	s.mux.HandleFunc("GET /{$}", s.handleIndex)

	// JSON API
	s.mux.HandleFunc("GET /api/status", s.handleStatusAPI)

	// Email Alerts API
	s.mux.HandleFunc("POST /api/alerts/subscribe", s.handleSubscribeAPI)
	s.mux.HandleFunc("GET /unsubscribe", s.handleUnsubscribe)

	// Web Push API
	s.mux.HandleFunc("GET /api/push/vapid-public-key", s.handlePushVapidKey)
	s.mux.HandleFunc("POST /api/push/subscribe", s.handlePushSubscribe)
	s.mux.HandleFunc("POST /api/push/unsubscribe", s.handlePushUnsubscribe)

	// Health check
	s.mux.HandleFunc("GET /health", s.handleHealth)
}

func (s *Server) getStatus(r *http.Request) (pub.Status, error) {
	if r.URL.Query().Get("mock") == "pub" {
		status := pub.Status{
			AtPub:    true,
			Headline: "Cam is at the pub!",
			Subtext:  "Pints are flowing at The Grandview. Come pull up a stool!",
			Pub: &pub.Pub{
				Name:      "The Grandview",
				EntityID:  "zone.the_grandview",
				Latitude:  -37.766332,
				Longitude: 144.948903,
				Address:   "47 Pearson St, Brunswick West",
				Duration:  "42 mins",
				ArrivedAt: "6:42 PM AEDT",
			},
			Directions: pub.BuildDirections("The Grandview", -37.766332, 144.948903),
			TrackedPubs: []pub.PubSummary{
				{Name: "CoConspirators", Address: "377 Victoria St, Brunswick", Latitude: -37.766072, Longitude: 144.956689, MapURL: "https://www.google.com/maps/search/?api=1&query=-37.766072,144.956689"},
				{Name: "Foreigner", Address: "102-104 Victoria St, Brunswick", Latitude: -37.764280, Longitude: 144.954560, MapURL: "https://www.google.com/maps/search/?api=1&query=-37.764280,144.954560"},
				{Name: "The Grandview", Address: "47 Pearson St, Brunswick West", Latitude: -37.766332, Longitude: 144.948903, MapURL: "https://www.google.com/maps/search/?api=1&query=-37.766332,144.948903"},
				{Name: "The Union", Address: "109 Union St, Brunswick", Latitude: -37.773434, Longitude: 144.953131, MapURL: "https://www.google.com/maps/search/?api=1&query=-37.773434,144.953131"},
				{Name: "The Vic", Address: "280 Victoria St, Brunswick", Latitude: -37.766832, Longitude: 144.958952, MapURL: "https://www.google.com/maps/search/?api=1&query=-37.766832,144.958952"},
			},
			CheckedAt: time.Now(),
		}
		return status, nil
	}

	forceRefresh := r.URL.Query().Get("fresh") == "1" || r.URL.Query().Get("force") == "true"
	states, err := s.hassClient.FetchAllStates(r.Context(), forceRefresh)
	if err != nil {
		return pub.Status{}, err
	}
	status := s.detector.Detect(states)
	return status, nil
}

func (s *Server) checkPubTransition(status pub.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if status.AtPub && status.Pub != nil {
		// New arrival at pub or switched to a different pub
		if !s.lastPubState || s.lastPubName != status.Pub.Name {
			s.lastPubState = true
			s.lastPubName = status.Pub.Name
			mapURL := ""
			if status.Directions != nil {
				mapURL = status.Directions.GoogleMaps
			}

			arrivedAt := status.Pub.ArrivedAt
			if arrivedAt == "" {
				arrivedAt = time.Now().Format("3:04 PM")
			}

			// 1. Dispatch Email Alerts
			if s.alertsService != nil {
				go s.alertsService.SendPubAlert(status.Pub.Name, arrivedAt, mapURL)
			}

			// 2. Dispatch Web Push Alerts (Debounced with 3-hour cooldown)
			if s.pushService != nil {
				go s.pushService.BroadcastPubAlert(status.Pub.Name, arrivedAt, "https://whereis.cam")
			}
		}
	} else {
		s.lastPubState = false
		s.lastPubName = ""
	}
}

// StartBackgroundPoller periodically checks status to trigger email & push alerts autonomously.
func (s *Server) StartBackgroundPoller(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				states, err := s.hassClient.FetchAllStates(ctx, false)
				if err != nil {
					continue
				}
				status := s.detector.Detect(states)
				s.checkPubTransition(status)
			}
		}
	}()
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	status, err := s.getStatus(r)
	if err != nil {
		log.Printf("error getting status: %v", err)
		status = pub.Status{
			AtPub:     false,
			Headline:  "Cam's not at a pub. Weird.",
			Subtext:   "His tracker signal is momentarily unreachable. Check back soon.",
			CheckedAt: time.Now(),
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.Execute(w, status); err != nil {
		log.Printf("template execution error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (s *Server) handleStatusAPI(w http.ResponseWriter, r *http.Request) {
	status, err := s.getStatus(r)
	if err != nil {
		log.Printf("API status error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "pub beacon temporarily unavailable",
			"details": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		log.Printf("JSON encode error: %v", err)
	}
}

type subscribeReq struct {
	Email string `json:"email"`
}

func (s *Server) handleSubscribeAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.alertsService == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Alerts service is not configured.",
		})
		return
	}

	var req subscribeReq
	if r.Header.Get("Content-Type") == "application/json" {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Invalid JSON payload.",
			})
			return
		}
	} else {
		req.Email = r.FormValue("email")
	}

	sub, isNew, err := s.alertsService.Subscribe(req.Email)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	if isNew {
		s.alertsService.SendWelcomeEmail(sub)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "You're on the list! We'll alert you next time Cam hits the pub.",
	})
}

func (s *Server) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	success := false
	if s.alertsService != nil && token != "" {
		success = s.alertsService.Unsubscribe(token)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	statusMsg := "You've been successfully unsubscribed from Cam's pub alerts."
	if !success {
		statusMsg = "Could not find your subscription or this link has expired."
	}

	page := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Unsubscribed - whereis.cam</title>
  <link rel="stylesheet" href="/static/style.css">
  <style>
    body { display: flex; align-items: center; justify-content: center; min-height: 100vh; padding: 20px; box-sizing: border-box; }
    .unsub-box { max-width: 440px; margin: 0 auto; }
    .unsub-link { display: inline-block; margin-top: 20px; font-weight: bold; color: var(--burnt-orange); text-decoration: underline; }
  </style>
</head>
<body>
  <div class="saloon-container unsub-box">
    <div class="card">
      <div class="saloon-crest">
        <div class="crest-ornament top-ornament" aria-hidden="true"></div>
        <div class="crest-title-group">
          <span class="crest-star">&starf;</span>
          <span class="crest-title">whereis.cam</span>
          <span class="crest-star">&starf;</span>
        </div>
        <div class="crest-ornament bottom-ornament" aria-hidden="true"></div>
      </div>
      <div class="status-intro" style="margin-top: 15px;">PUB RADAR ALERTS</div>
      <h1 class="pub-hero-name" style="font-size: 1.8rem; margin: 10px 0;">%s</h1>
      <p class="pub-subtext">No hard feelings — you can resubscribe anytime if you change your mind.</p>
      <a href="/" class="unsub-link">&larr; Back to whereis.cam</a>
    </div>
  </div>
</body>
</html>`, statusMsg)

	_, _ = w.Write([]byte(page))
}

func (s *Server) handlePushVapidKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	pubKey := ""
	if s.pushService != nil {
		pubKey = s.pushService.VAPIDPublicKey()
	}
	_ = json.NewEncoder(w).Encode(map[string]string{
		"public_key": pubKey,
	})
}

type pushSubPayload struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256DH string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.pushService == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Push notifications not configured",
		})
		return
	}

	var payload pushSubPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid push subscription JSON",
		})
		return
	}

	sub := alerts.PushSubscription{
		Endpoint: payload.Endpoint,
		P256DH:   payload.Keys.P256DH,
		Auth:     payload.Keys.Auth,
	}

	if err := s.pushService.Subscribe(sub); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Push notifications enabled on this device!",
	})
}

type pushUnsubPayload struct {
	Endpoint string `json:"endpoint"`
}

func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.pushService == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	var payload pushUnsubPayload
	_ = json.NewDecoder(r.Body).Decode(&payload)
	_ = s.pushService.Unsubscribe(payload.Endpoint)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
