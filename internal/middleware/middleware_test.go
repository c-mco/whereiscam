package middleware

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"whereis.cam/internal/storage"
)

func TestSecurityHeaders(t *testing.T) {
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("expected X-Frame-Options DENY, got %q", rec.Header().Get("X-Frame-Options"))
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("expected X-Content-Type-Options nosniff, got %q", rec.Header().Get("X-Content-Type-Options"))
	}
}

func TestRecoverer(t *testing.T) {
	handler := Recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("unexpected crash")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
}

func TestHitTracker(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := storage.Open(filepath.Join(tmpDir, "tracker.db"))
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	middleware := HitTracker(db)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Header.Set("User-Agent", "CamTestAgent/1.0")
	req.Header.Set("CF-Connecting-IP", "99.88.77.66")
	handler.ServeHTTP(rec, req)

	// Wait briefly for background goroutine
	time.Sleep(50 * time.Millisecond)

	stats, err := db.GetHitStats()
	if err != nil {
		t.Fatalf("failed to get hit stats: %v", err)
	}
	if stats.TotalHits != 1 {
		t.Errorf("expected 1 hit recorded, got %d", stats.TotalHits)
	}
	if len(stats.RecentHits) > 0 && stats.RecentHits[0].IP != "99.88.77.66" {
		t.Errorf("expected IP 99.88.77.66, got %q", stats.RecentHits[0].IP)
	}

	// Test ignored route like /static/style.css
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	handler.ServeHTTP(rec2, req2)
	time.Sleep(50 * time.Millisecond)

	statsAfter, _ := db.GetHitStats()
	if statsAfter.TotalHits != 1 {
		t.Errorf("expected still 1 hit after static asset request, got %d", statsAfter.TotalHits)
	}
}

func TestHitTrackerSkipsAPIAndErrors(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "tracker_skip.db"))
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	handler := HitTracker(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wp-login.php" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{"/api/status", "/wp-login.php"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	time.Sleep(50 * time.Millisecond)

	stats, _ := db.GetHitStats()
	if stats.TotalHits != 0 {
		t.Errorf("expected API polls and 404s to be skipped, got %d hits", stats.TotalHits)
	}
}
