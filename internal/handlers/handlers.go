package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"whereis.cam/internal/alerts"
	"whereis.cam/internal/homeassistant"
	"whereis.cam/internal/netutil"
	"whereis.cam/internal/pub"
	"whereis.cam/internal/storage"
	"whereis.cam/web"
)

// Server handles HTTP traffic for whereis.cam.
type Server struct {
	hassClient    *homeassistant.Client
	detector      *pub.Detector
	alertsService *alerts.Service
	pushService   *alerts.PushService
	store         *storage.DB
	adminPassword string
	tmpl          *template.Template
	dashTmpl      *template.Template
	loginTmpl     *template.Template
	mux           *http.ServeMux

	trustedProxies *netutil.TrustedProxies

	mu            sync.Mutex
	lastPubState  bool
	lastPubName   string
	sessions      map[string]time.Time
	loginFailures map[string]*loginFailure
}

const (
	maxLoginFailures   = 5
	loginFailureWindow = 15 * time.Minute
	subscribersPerPage = 100
)

type loginFailure struct {
	count int
	first time.Time
}

// NewServer initializes templates, routes, and dependencies.
func NewServer(hassClient *homeassistant.Client, detector *pub.Detector, alertsService *alerts.Service, pushService *alerts.PushService, store *storage.DB, adminPassword string) (*Server, error) {
	tmpl, err := template.ParseFS(web.FS, "templates/index.html")
	if err != nil {
		return nil, err
	}

	dashTmpl, err := template.ParseFS(web.FS, "templates/dashboard.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse dashboard template: %w", err)
	}

	loginTmpl, err := template.ParseFS(web.FS, "templates/login.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse login template: %w", err)
	}

	s := &Server{
		hassClient:    hassClient,
		detector:      detector,
		alertsService: alertsService,
		pushService:   pushService,
		store:         store,
		adminPassword: adminPassword,
		tmpl:          tmpl,
		dashTmpl:      dashTmpl,
		loginTmpl:     loginTmpl,
		mux:           http.NewServeMux(),
		sessions:      make(map[string]time.Time),
		loginFailures: make(map[string]*loginFailure),
	}

	s.routes()
	return s, nil
}

// SetTrustedProxies configures which reverse proxies may authenticate users via
// the Remote-User header and report client IPs. By default none are trusted.
func (s *Server) SetTrustedProxies(tp *netutil.TrustedProxies) {
	s.trustedProxies = tp
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
		etags := staticETags(staticSubFS)
		s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Asset URLs aren't versioned, so make browsers revalidate on every use;
			// unchanged files cost a 304 thanks to the content-hash ETag.
			w.Header().Set("Cache-Control", "no-cache")
			if tag, ok := etags[r.URL.Path]; ok {
				w.Header().Set("ETag", tag)
			}
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

	// Cam's Dashboard & Admin API
	s.mux.HandleFunc("GET /dashboard", s.handleDashboard)
	s.mux.HandleFunc("POST /dashboard/login", s.handleDashboardLogin)
	s.mux.HandleFunc("POST /dashboard/logout", s.handleDashboardLogout)
	s.mux.HandleFunc("POST /api/admin/override", s.handleAdminOverride)
	s.mux.HandleFunc("POST /api/admin/test-alert", s.handleAdminTestAlert)
	s.mux.HandleFunc("POST /api/admin/test-push", s.handleAdminTestPush)
	s.mux.HandleFunc("POST /api/admin/subscribers/unsubscribe", s.handleAdminDeactivateSub)

	// Health check
	s.mux.HandleFunc("GET /health", s.handleHealth)
}

// staticETags hashes every embedded static file so responses can be revalidated.
func staticETags(fsys fs.FS) map[string]string {
	etags := make(map[string]string)
	_ = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(data)
		etags[path] = `"` + hex.EncodeToString(sum[:8]) + `"`
		return nil
	})
	return etags
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
	var trackedPubs []pub.PubSummary
	if err == nil && s.detector != nil {
		normalStatus := s.detector.Detect(states)
		trackedPubs = normalStatus.TrackedPubs
	}

	// Check manual override or secret pint mode
	if s.store != nil {
		override, oErr := s.store.GetOverrideState()
		if oErr == nil && override.Mode != "" && override.Mode != "none" {
			now := time.Now()
			switch override.Mode {
			case "secret_pint_stealth":
				return pub.Status{
					AtPub:       false,
					Headline:    "Cam's not at a pub. Weird.",
					Subtext:     "Cam is off-duty right now. Checked his regular locals: all clear.",
					TrackedPubs: trackedPubs,
					CheckedAt:   now,
				}, nil
			case "secret_pint_cheeky":
				return pub.Status{
					AtPub:       false,
					Headline:    "Cam's having a secret pint! 🤫",
					Subtext:     "He's definitely at a pub, but he's sworn us to secrecy. No coordinates for you today, mate. Pull up a stool... if you can find him!",
					TrackedPubs: trackedPubs,
					CheckedAt:   now,
				}, nil
			case "manual_pub":
				arrivedAt := override.ArrivedAt
				if arrivedAt == "" {
					arrivedAt = pub.FormatClockTime(now)
				}
				addr := override.PubAddress
				if addr == "" {
					addr = pub.ResolvePubAddress(override.PubName)
				}
				var lat, lon float64
				directions := pub.BuildDirectionsByAddress(override.PubName, addr)
				if known, ok := findTrackedPub(trackedPubs, override.PubName); ok && known.Latitude != 0 && known.Longitude != 0 {
					lat, lon = known.Latitude, known.Longitude
					directions = pub.BuildDirections(override.PubName, lat, lon)
				}
				return pub.Status{
					AtPub: true,
					Pub: &pub.Pub{
						Name:      override.PubName,
						Address:   addr,
						Latitude:  lat,
						Longitude: lon,
						ArrivedAt: arrivedAt,
						Duration:  "visiting now",
					},
					Headline:    "Cam is at the pub!",
					Subtext:     fmt.Sprintf("Pints are pouring at %s. Pull up a stool and join him!", override.PubName),
					Directions:  directions,
					TrackedPubs: trackedPubs,
					CheckedAt:   now,
				}, nil
			}
		}
	}

	if err != nil {
		return pub.Status{}, err
	}
	return s.detector.Detect(states), nil
}

// findTrackedPub looks up a known pub zone by name, case-insensitively.
func findTrackedPub(pubs []pub.PubSummary, name string) (pub.PubSummary, bool) {
	name = strings.TrimSpace(name)
	for _, p := range pubs {
		if strings.EqualFold(strings.TrimSpace(p.Name), name) {
			return p, true
		}
	}
	return pub.PubSummary{}, false
}

// resyncPubState aligns the alert transition state with what the detector
// currently sees, so ending an override doesn't re-announce an ongoing visit.
func (s *Server) resyncPubState(ctx context.Context) {
	if s.hassClient == nil || s.detector == nil {
		return
	}
	states, err := s.hassClient.FetchAllStates(ctx, false)
	if err != nil {
		log.Printf("could not re-sync pub state after override change: %v", err)
		return
	}
	status := s.detector.Detect(states)

	s.mu.Lock()
	defer s.mu.Unlock()
	if status.AtPub && status.Pub != nil {
		s.lastPubState = true
		s.lastPubName = status.Pub.Name
	} else {
		s.lastPubState = false
		s.lastPubName = ""
	}
}

func (s *Server) checkPubTransition(status pub.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Suppress alerts if override is active
	if s.store != nil {
		override, err := s.store.GetOverrideState()
		if err == nil && override.Mode != "" && override.Mode != "none" {
			return
		}
	}

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
				arrivedAt = pub.FormatClockTime(time.Now())
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

	if err := s.pushService.Subscribe(payload.Endpoint, payload.Keys.P256DH, payload.Keys.Auth); err != nil {
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

// ── Cam's Dashboard & Admin Control Handlers ──

type dashboardViewData struct {
	HitStats               storage.HitStats
	Override               storage.OverrideState
	Subscribers            []storage.Subscriber
	ActiveSubscribersCount int
	TotalSubscribersCount  int
	SubscribersPage        int
	SubscribersPrevPage    int // 0 when on the first page
	SubscribersNextPage    int // 0 when on the last page
	PushSubscribersCount   int
	TrackedPubs            []pub.PubSummary
	PersonEntity           string
	RawPersonState         string
}

func (s *Server) isAuthenticated(r *http.Request) bool {
	// Authelia's Remote-User header is only believable when the request came
	// through a configured trusted proxy; anyone else could just send it.
	if r.Header.Get("Remote-User") != "" && s.trustedProxies.Trusts(r) {
		return true
	}

	cookie, err := r.Cookie("cam_session")
	if err != nil || cookie.Value == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.sessions[cookie.Value]
	if !ok || time.Now().After(expiry) {
		return false
	}
	return true
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthenticated(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = s.loginTmpl.Execute(w, map[string]interface{}{"Error": ""})
		return
	}

	var hitStats storage.HitStats
	var override storage.OverrideState
	var subs []storage.Subscriber
	activeSubsCount := 0
	totalSubsCount := 0
	pushSubsCount := 0

	page, _ := strconv.Atoi(r.URL.Query().Get("subs_page"))
	if page < 1 {
		page = 1
	}

	if s.store != nil {
		hitStats, _ = s.store.GetHitStats()
		override, _ = s.store.GetOverrideState()
		totalSubsCount, _ = s.store.CountSubscribers()
		subs, _ = s.store.AllSubscribers(subscribersPerPage, (page-1)*subscribersPerPage)
		activeList, _ := s.store.ActiveSubscribers()
		activeSubsCount = len(activeList)
		pushList, _ := s.store.ActivePushSubscriptions()
		pushSubsCount = len(pushList)
	}

	rawState := "unknown"
	var trackedPubs []pub.PubSummary
	personEntity := "person.cam"
	if s.detector != nil {
		personEntity = s.detector.PersonEntity()
	}

	if s.hassClient != nil {
		states, err := s.hassClient.FetchAllStates(r.Context(), false)
		if err == nil && s.detector != nil {
			detStatus := s.detector.Detect(states)
			trackedPubs = detStatus.TrackedPubs
			for _, st := range states {
				if strings.EqualFold(st.EntityID, personEntity) {
					rawState = st.State
					break
				}
			}
		}
	}

	data := dashboardViewData{
		HitStats:               hitStats,
		Override:               override,
		Subscribers:            subs,
		ActiveSubscribersCount: activeSubsCount,
		TotalSubscribersCount:  totalSubsCount,
		SubscribersPage:        page,
		SubscribersPrevPage:    page - 1,
		SubscribersNextPage:    nextPage(page, totalSubsCount),
		PushSubscribersCount:   pushSubsCount,
		TrackedPubs:            trackedPubs,
		PersonEntity:           personEntity,
		RawPersonState:         rawState,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.dashTmpl.Execute(w, data); err != nil {
		log.Printf("dashboard template execution error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func nextPage(page, total int) int {
	if page*subscribersPerPage < total {
		return page + 1
	}
	return 0
}

// loginLocked reports whether ip has used up its failed login attempts.
func (s *Server) loginLocked(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.loginFailures[ip]
	if !ok {
		return false
	}
	if time.Since(f.first) > loginFailureWindow {
		delete(s.loginFailures, ip)
		return false
	}
	return f.count >= maxLoginFailures
}

func (s *Server) recordLoginFailure(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, f := range s.loginFailures {
		if now.Sub(f.first) > loginFailureWindow {
			delete(s.loginFailures, k)
		}
	}
	f, ok := s.loginFailures[ip]
	if !ok {
		f = &loginFailure{first: now}
		s.loginFailures[ip] = f
	}
	f.count++
}

func (s *Server) renderLoginError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = s.loginTmpl.Execute(w, map[string]interface{}{"Error": msg})
}

func (s *Server) handleDashboardLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	if s.adminPassword == "" {
		s.renderLoginError(w, http.StatusForbidden, "Password login is disabled (ADMIN_PASSWORD not set)")
		return
	}

	ip := s.trustedProxies.ClientIP(r)
	if s.loginLocked(ip) {
		s.renderLoginError(w, http.StatusTooManyRequests, "Too many attempts. Try again in a few minutes.")
		return
	}

	pass := r.FormValue("password")
	if subtle.ConstantTimeCompare([]byte(pass), []byte(s.adminPassword)) != 1 {
		s.recordLoginFailure(ip)
		s.renderLoginError(w, http.StatusUnauthorized, "Incorrect saloon passcode")
		return
	}

	s.mu.Lock()
	delete(s.loginFailures, ip)
	s.mu.Unlock()

	tokenBytes := make([]byte, 16)
	_, _ = rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)

	s.mu.Lock()
	s.sessions[token] = time.Now().Add(7 * 24 * time.Hour)
	s.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "cam_session",
		Value:    token,
		Path:     "/",
		MaxAge:   7 * 86400,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleDashboardLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("cam_session"); err == nil {
		s.mu.Lock()
		delete(s.sessions, cookie.Value)
		s.mu.Unlock()
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "cam_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

type adminOverrideReq struct {
	Mode       string `json:"mode"`
	PubName    string `json:"pub_name"`
	PubAddress string `json:"pub_address"`
	ArrivedAt  string `json:"arrived_at"`
	Notify     bool   `json:"notify"`
}

func (s *Server) handleAdminOverride(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !s.isAuthenticated(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}

	var req adminOverrideReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Invalid request body"})
		return
	}

	if s.store == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Storage unavailable"})
		return
	}

	if req.Mode == "clear" || req.Mode == "none" {
		if err := s.store.ClearOverrideState(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		s.resyncPubState(r.Context())

		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Override cleared"})
		return
	}

	if req.Mode == "secret_pint_stealth" || req.Mode == "secret_pint_cheeky" {
		err := s.store.SetOverrideState(storage.OverrideState{
			Mode:      req.Mode,
			UpdatedAt: time.Now().UTC(),
		})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		s.mu.Lock()
		s.lastPubState = false
		s.lastPubName = ""
		s.mu.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Secret pint mode activated"})
		return
	}

	if req.Mode == "manual_pub" {
		if req.PubName == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Pub name is required"})
			return
		}
		arrivedAt := req.ArrivedAt
		if arrivedAt == "" {
			arrivedAt = pub.FormatClockTime(time.Now())
		}
		addr := req.PubAddress
		if addr == "" {
			addr = pub.ResolvePubAddress(req.PubName)
		}

		err := s.store.SetOverrideState(storage.OverrideState{
			Mode:       "manual_pub",
			PubName:    req.PubName,
			PubAddress: addr,
			ArrivedAt:  arrivedAt,
			UpdatedAt:  time.Now().UTC(),
		})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
			return
		}

		if req.Notify {
			mapURL := fmt.Sprintf("https://www.google.com/maps/search/?api=1&query=%s", url.QueryEscape(req.PubName+" Brunswick"))
			if s.alertsService != nil {
				go s.alertsService.SendPubAlert(req.PubName, arrivedAt, mapURL)
			}
			if s.pushService != nil {
				go s.pushService.BroadcastPubAlert(req.PubName, arrivedAt, "https://whereis.cam")
			}
		}

		s.mu.Lock()
		s.lastPubState = true
		s.lastPubName = req.PubName
		s.mu.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Manual pub override activated"})
		return
	}

	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unknown mode"})
}

type adminTestAlertReq struct {
	To  string `json:"to"`
	Pub string `json:"pub"`
}

func (s *Server) handleAdminTestAlert(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !s.isAuthenticated(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}

	var req adminTestAlertReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Invalid request body"})
		return
	}

	if req.To == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Recipient email required"})
		return
	}

	pubName := req.Pub
	if pubName == "" {
		pubName = "The Grandview (Test Alert)"
	}

	if s.alertsService == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Alerts service not configured"})
		return
	}

	subject := fmt.Sprintf("★ [TEST] Cam is at %s!", pubName)
	htmlBody := fmt.Sprintf(`<p>This is a safe test alert sent to %s from Cam's Dashboard.</p>`, html.EscapeString(req.To))
	if err := s.alertsService.SendEmail(req.To, subject, htmlBody); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Test email sent"})
}

type adminDeactivateSubReq struct {
	ID int64 `json:"id"`
}

func (s *Server) handleAdminDeactivateSub(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !s.isAuthenticated(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}

	var req adminDeactivateSubReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Invalid request body"})
		return
	}

	if s.store == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Storage unavailable"})
		return
	}

	if err := s.store.DeactivateEmailSubscriber(req.ID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

type adminTestPushReq struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256DH string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	Pub string `json:"pub"`
}

func (s *Server) handleAdminTestPush(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !s.isAuthenticated(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}

	var req adminTestPushReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Invalid request body"})
		return
	}

	if s.pushService == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Push notifications not configured"})
		return
	}

	if req.Endpoint == "" || req.Keys.P256DH == "" || req.Keys.Auth == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Missing push subscription endpoint or keys"})
		return
	}

	pubName := req.Pub
	if pubName == "" {
		pubName = "The Grandview (Test Push)"
	}

	payload := alerts.PushPayload{
		Title: fmt.Sprintf("★ [TEST] Cam is at %s!", pubName),
		Body:  fmt.Sprintf("First pot poured at %s. Push notification test successful!", pub.FormatClockTime(time.Now())),
		URL:   "https://whereis.cam",
	}

	if err := alerts.ValidatePushEndpoint(req.Endpoint); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	if err := s.pushService.SendDirect(req.Endpoint, req.Keys.P256DH, req.Keys.Auth, payload); err != nil {
		// Don't echo gateway/dial errors back; log them for the operator instead.
		log.Printf("test push failed: %v", err)
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Push gateway rejected the test notification (see server logs)"})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Test push sent to this device!"})
}
