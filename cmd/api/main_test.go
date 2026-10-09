package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"whereis.cam/internal/config"
	"whereis.cam/internal/handlers"
	"whereis.cam/internal/homeassistant"
	"whereis.cam/internal/middleware"
	"whereis.cam/internal/pub"
)

func TestAppIntegration(t *testing.T) {
	// Setup mock Home Assistant with person.cam at Foreigner pub
	mockHA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"message":"API running."}`))
			return
		}
		if r.URL.Path == "/api/states" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]homeassistant.EntityState{
				{
					EntityID: "person.cam",
					State:    "Foreigner",
					Attributes: map[string]interface{}{
						"friendly_name": "cam",
						"in_zones":      []interface{}{"zone.foreigner", "zone.brunswick"},
						"latitude":      -37.7642,
						"longitude":     144.9545,
					},
					LastChanged: time.Now().Add(-30 * time.Minute),
				},
				{
					EntityID: "zone.foreigner",
					State:    "1",
					Attributes: map[string]interface{}{
						"friendly_name": "Foreigner",
						"icon":          "mdi:glass-mug-variant",
						"latitude":      -37.7642,
						"longitude":     144.9545,
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockHA.Close()

	os.Setenv("HASS_URL", mockHA.URL)
	os.Setenv("HASS_TOKEN", "integration-test-token")
	os.Setenv("PORT", "8999")
	defer os.Unsetenv("HASS_URL")
	defer os.Unsetenv("HASS_TOKEN")
	defer os.Unsetenv("PORT")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}

	hassClient := homeassistant.NewClient(cfg.HassURL, cfg.HassToken, cfg.CacheTTL)
	detector := pub.NewDetector(cfg.PersonEntity, cfg.PubZones, cfg.PubIconKeywords)

	server, err := handlers.NewServer(hassClient, detector, nil, nil)
	if err != nil {
		t.Fatalf("handlers.NewServer failed: %v", err)
	}

	handler := middleware.Recoverer(middleware.SecurityHeaders(server.Handler()))

	// Test 1: GET / (HTML page contains Foreigner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Foreigner") {
		t.Errorf("expected page to contain 'Foreigner'")
	}
	if !strings.Contains(body, "Cam is at the pub!") {
		t.Errorf("expected page to contain 'Cam is at the pub!'")
	}

	// Test 2: GET /api/status (JSON contains status and directions)
	recJSON := httptest.NewRecorder()
	reqJSON := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	handler.ServeHTTP(recJSON, reqJSON)

	if recJSON.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", recJSON.Code)
	}

	var status pub.Status
	if err := json.Unmarshal(recJSON.Body.Bytes(), &status); err != nil {
		t.Fatalf("failed to decode status: %v", err)
	}
	if !status.AtPub {
		t.Errorf("expected AtPub true")
	}
	if status.Pub == nil || status.Pub.Name != "Foreigner" {
		t.Errorf("expected pub Foreigner, got %v", status.Pub)
	}
	if status.Directions == nil || !strings.Contains(status.Directions.OpenStreetMap, "openstreetmap") {
		t.Errorf("expected OSM directions link")
	}
}
