package homeassistant

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientFetchAllStates(t *testing.T) {
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)

		if r.Header.Get("Authorization") != "Bearer secret-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if r.URL.Path == "/api/states" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[
				{
					"entity_id": "person.cam",
					"state": "home",
					"attributes": {
						"friendly_name": "cam",
						"in_zones": ["zone.home"]
					}
				},
				{
					"entity_id": "zone.the_vic",
					"state": "0",
					"attributes": {
						"friendly_name": "The Vic",
						"icon": "mdi:glass-mug-variant",
						"latitude": -37.7668,
						"longitude": 144.9589
					}
				}
			]`))
			return
		}

		if r.URL.Path == "/api/" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"message":"API running."}`))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, "secret-token", 5*time.Second)

	// Test CheckConnectivity
	if err := client.CheckConnectivity(context.Background()); err != nil {
		t.Fatalf("CheckConnectivity failed: %v", err)
	}

	// 1st fetch: should hit server
	states, err := client.FetchAllStates(context.Background(), false)
	if err != nil {
		t.Fatalf("FetchAllStates failed: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("expected 2 states, got %d", len(states))
	}
	if states[0].FriendlyName() != "cam" {
		t.Errorf("expected 'cam', got %q", states[0].FriendlyName())
	}

	// 2nd fetch: should use cache (no extra server request)
	reqsBefore := atomic.LoadInt32(&requestCount)
	cachedStates, err := client.FetchAllStates(context.Background(), false)
	if err != nil {
		t.Fatalf("cached FetchAllStates failed: %v", err)
	}
	if len(cachedStates) != 2 {
		t.Fatalf("expected 2 cached states, got %d", len(cachedStates))
	}
	if atomic.LoadInt32(&requestCount) != reqsBefore {
		t.Errorf("expected cache hit without server call, requests increased")
	}

	// 3rd fetch: force refresh should hit server
	_, err = client.FetchAllStates(context.Background(), true)
	if err != nil {
		t.Fatalf("forced FetchAllStates failed: %v", err)
	}
	if atomic.LoadInt32(&requestCount) <= reqsBefore {
		t.Errorf("expected force refresh to increase request count")
	}
}

func TestClientUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("401: Unauthorized"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "wrong-token", 5*time.Second)
	_, err := client.FetchAllStates(context.Background(), false)
	if err == nil {
		t.Fatal("expected error on 401 Unauthorized, got nil")
	}
}
