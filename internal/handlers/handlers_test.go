package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

	srv, err := NewServer(hassClient, detector)
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
	if !strings.Contains(body, "The Vic") {
		t.Errorf("expected HTML to contain pub name 'The Vic'")
	}
	if !strings.Contains(body, "whereis.cam") {
		t.Errorf("expected HTML to contain brand")
	}
}
