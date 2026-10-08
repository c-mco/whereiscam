package handlers

import (
	"encoding/json"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"time"

	"whereis.cam/internal/homeassistant"
	"whereis.cam/internal/pub"
	"whereis.cam/web"
)

// Server handles HTTP traffic for whereis.cam.
type Server struct {
	hassClient *homeassistant.Client
	detector   *pub.Detector
	tmpl       *template.Template
	mux        *http.ServeMux
}

// NewServer initializes templates, routes, and dependencies.
func NewServer(hassClient *homeassistant.Client, detector *pub.Detector) (*Server, error) {
	tmpl, err := template.ParseFS(web.FS, "templates/index.html")
	if err != nil {
		return nil, err
	}

	s := &Server{
		hassClient: hassClient,
		detector:   detector,
		tmpl:       tmpl,
		mux:        http.NewServeMux(),
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
		s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSubFS))))
	}

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

	// Health check
	s.mux.HandleFunc("GET /health", s.handleHealth)
}

func (s *Server) getStatus(r *http.Request) (pub.Status, error) {
	forceRefresh := r.URL.Query().Get("fresh") == "1" || r.URL.Query().Get("force") == "true"
	states, err := s.hassClient.FetchAllStates(r.Context(), forceRefresh)
	if err != nil {
		return pub.Status{}, err
	}
	return s.detector.Detect(states), nil
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
			"error":   "failed to query Home Assistant",
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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
