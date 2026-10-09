package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"whereis.cam/internal/alerts"
	"path/filepath"
	"time"

	"whereis.cam/internal/homeassistant"
	"whereis.cam/internal/pub"
)

func createMockServer(camState string, camInZones []string) (*httptest.Server, *Server, error) {
	mockHA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/states" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]homeassistant.EntityState{
				{
					EntityID: "person.cam",
					State:    camState,
					Attributes: map[string]interface{}{
						"friendly_name": "cam",
						"in_zones": func() []interface{} {
							res := make([]interface{}, len(camInZones))
							for i, z := range camInZones {
								res[i] = z
							}
							return res
						}(),
					},
					LastChanged: time.Now().Add(-20 * time.Minute),
				},
				{
					EntityID: "zone.the_vic",
					State:    "0",
					Attributes: map[string]interface{}{
						"friendly_name": "The Vic",
						"icon":          "mdi:glass-mug-variant",
						"latitude":      -37.7668,
						"longitude":     144.9589,
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	hassClient := homeassistant.NewClient(mockHA.URL, "dummy-token", 5*time.Second)
	detector := pub.NewDetector("person.cam", nil, nil)

	srv, err := NewServer(hassClient, detector, nil, nil)
	return mockHA, srv, err
}

func TestHealthHandler(t *testing.T) {
	mockHA, srv, err := createMockServer("home", []string{"zone.home"})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer mockHA.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("expected status ok body, got %s", rec.Body.String())
	}
}

func TestStaticCSS(t *testing.T) {
	mockHA, srv, err := createMockServer("home", []string{"zone.home"})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer mockHA.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "--bg-main") {
		t.Errorf("expected CSS content with variables, got %s", rec.Body.String()[:100])
	}
	for _, expected := range []string{
		"--burnt-orange",
		"--mustard-yellow",
		"--mission-brown-dark",
		"--font-chunky-serif",
		"--font-art-deco-sans",
		".amber-glass",
		".grain-overlay",
		"radius-bullnose",
	} {
		if !strings.Contains(rec.Body.String(), expected) {
			t.Errorf("expected CSS to contain %q", expected)
		}
	}
}

func TestStatusAPIWhenNotAtPub(t *testing.T) {
	mockHA, srv, err := createMockServer("home", []string{"zone.home"})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer mockHA.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var status pub.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	if status.AtPub {
		t.Errorf("expected at_pub to be false")
	}
	if status.Headline != "Cam's not at a pub. Weird." {
		t.Errorf("expected headline \"Cam's not at a pub. Weird.\", got %q", status.Headline)
	}
}

func TestStatusAPIWhenAtPub(t *testing.T) {
	mockHA, srv, err := createMockServer("The Vic", []string{"zone.the_vic"})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer mockHA.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var status pub.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	if !status.AtPub {
		t.Errorf("expected at_pub to be true")
	}
	if status.Pub == nil || status.Pub.Name != "The Vic" {
		t.Errorf("expected pub The Vic, got %v", status.Pub)
	}
	if status.Directions == nil || !strings.Contains(status.Directions.OpenStreetMap, "openstreetmap.org") {
		t.Errorf("expected OSM directions link")
	}
}

func TestIndexHTMLRender(t *testing.T) {
	mockHA, srv, err := createMockServer("The Vic", []string{"zone.the_vic"})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer mockHA.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, expected := range []string{
		"The Vic",
		"whereis.cam",
		"calistoga-latin.woff2",
		"josefinsans-latin.woff2",
		"grain-overlay",
		"amber-glass",
		"intel-modal",
		"Cam is at the pub!",
		"coaster-frame",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("expected HTML to contain %q", expected)
		}
	}
}

func TestIndexHTMLRenderWhenNotAtPub(t *testing.T) {
	mockHA, srv, err := createMockServer("home", []string{"zone.home"})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer mockHA.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, expected := range []string{
		"whereis.cam",
		"grain-overlay",
		"amber-glass",
		"intel-modal",
		"Radar Scanning",
		"coaster-frame",
		"Scanning 1 Local Pubs",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("expected HTML to contain %q", expected)
		}
	}
}

func TestSubscribeAndUnsubscribeEndpoints(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "subscribers.json")
	alertsSvc, err := alerts.NewService(filePath, "test-key", "alerts@whereis.cam", "https://whereis.cam")
	if err != nil {
		t.Fatalf("failed to create alerts service: %v", err)
	}

	mockHA, srv, err := createMockServer("not_home", []string{})
	if err != nil {
		t.Fatalf("createMockServer failed: %v", err)
	}
	defer mockHA.Close()

	srv.alertsService = alertsSvc

	// 1. POST /api/alerts/subscribe with valid email
	body := strings.NewReader(`{"email":"mate@example.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/alerts/subscribe", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	subs := alertsSvc.ActiveSubscribers()
	if len(subs) != 1 {
		t.Fatalf("expected 1 active subscriber, got %d", len(subs))
	}
	token := subs[0].Token

	// 2. GET /unsubscribe?token=...
	unsubReq := httptest.NewRequest(http.MethodGet, "/unsubscribe?token="+token, nil)
	unsubRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(unsubRec, unsubReq)

	if unsubRec.Code != http.StatusOK {
		t.Errorf("expected 200 OK from unsubscribe, got %d", unsubRec.Code)
	}
	if !strings.Contains(unsubRec.Body.String(), "successfully unsubscribed") {
		t.Errorf("expected unsub confirmation message in body")
	}

	if alertsSvc.CountActive() != 0 {
		t.Errorf("expected 0 active subscribers after unsubscribe, got %d", alertsSvc.CountActive())
	}
}
