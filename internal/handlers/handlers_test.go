package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whereis.cam/internal/alerts"
	"whereis.cam/internal/homeassistant"
	"whereis.cam/internal/netutil"
	"whereis.cam/internal/pub"
	"whereis.cam/internal/storage"
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

	tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("test_whereis_%d.db", time.Now().UnixNano()))
	store, _ := storage.Open(tmpDir)

	srv, err := NewServer(hassClient, detector, nil, nil, store, "cam")
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
	db, err := storage.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}
	defer db.Close()

	alertsSvc, err := alerts.NewService(db, "test-key", "alerts@whereis.cam", "https://whereis.cam")
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

func loginAdmin(t *testing.T, srv *Server) *http.Cookie {
	t.Helper()
	form := strings.NewReader("password=cam")
	req := httptest.NewRequest(http.MethodPost, "/dashboard/login", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	for _, c := range rec.Result().Cookies() {
		if c.Name == "cam_session" && c.Value != "" {
			return c
		}
	}
	t.Fatalf("login failed to return cam_session cookie")
	return nil
}

func TestDashboardAuth(t *testing.T) {
	mockHA, srv, err := createMockServer("home", []string{"zone.home"})
	if err != nil {
		t.Fatalf("createMockServer failed: %v", err)
	}
	defer mockHA.Close()

	// 1. GET /dashboard without auth renders login form
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Saloon Passcode") {
		t.Errorf("expected login form containing Saloon Passcode")
	}

	// 2. POST /dashboard/login with bad password
	badForm := strings.NewReader("password=wrongpass")
	badReq := httptest.NewRequest(http.MethodPost, "/dashboard/login", badForm)
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(badRec, badReq)

	if badRec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", badRec.Code)
	}
	if !strings.Contains(badRec.Body.String(), "Incorrect saloon passcode") {
		t.Errorf("expected error message in body")
	}

	// 3. POST /dashboard/login with valid password
	sessionCookie := loginAdmin(t, srv)

	// 4. GET /dashboard with valid cookie renders dashboard
	dashReq := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	dashReq.AddCookie(sessionCookie)
	dashRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(dashRec, dashReq)

	if dashRec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", dashRec.Code)
	}
	if !strings.Contains(dashRec.Body.String(), "Cam's Command Saloon") {
		t.Errorf("expected dashboard body, got %s", dashRec.Body.String())
	}

	// 5. POST /dashboard/logout clears session
	logoutReq := httptest.NewRequest(http.MethodPost, "/dashboard/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	logoutRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", logoutRec.Code)
	}

	// 6. Verify session is now invalidated
	dashReq2 := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	dashReq2.AddCookie(sessionCookie)
	dashRec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(dashRec2, dashReq2)

	if !strings.Contains(dashRec2.Body.String(), "Saloon Passcode") {
		t.Errorf("expected login page after logout")
	}

	// 7. Remote-User from an untrusted peer must be ignored
	spoofReq := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	spoofReq.Header.Set("Remote-User", "cam")
	spoofRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(spoofRec, spoofReq)

	if strings.Contains(spoofRec.Body.String(), "Cam's Command Saloon") {
		t.Errorf("Remote-User header from untrusted peer must not grant dashboard access")
	}

	// 8. Remote-User via a trusted Authelia proxy authenticates without cookie
	// (httptest requests come from 192.0.2.1)
	tp, err := netutil.ParseTrustedProxies([]string{"192.0.2.0/24"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies failed: %v", err)
	}
	srv.SetTrustedProxies(tp)

	autheliaReq := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	autheliaReq.Header.Set("Remote-User", "cam")
	autheliaRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(autheliaRec, autheliaReq)

	if autheliaRec.Code != http.StatusOK {
		t.Errorf("expected 200 OK with Authelia Remote-User header, got %d", autheliaRec.Code)
	}
	if !strings.Contains(autheliaRec.Body.String(), "Cam's Command Saloon") {
		t.Errorf("expected dashboard body for Authelia user")
	}
}

func postLogin(srv *Server, password string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/dashboard/login", strings.NewReader("password="+password))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestLoginRateLimit(t *testing.T) {
	mockHA, srv, err := createMockServer("home", []string{"zone.home"})
	if err != nil {
		t.Fatalf("createMockServer failed: %v", err)
	}
	defer mockHA.Close()

	for i := 0; i < maxLoginFailures; i++ {
		if rec := postLogin(srv, "nope"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i+1, rec.Code)
		}
	}
	// Even the correct password is refused while locked out
	if rec := postLogin(srv, "cam"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 after %d failures, got %d", maxLoginFailures, rec.Code)
	}
}

func TestLoginDisabledWithoutPassword(t *testing.T) {
	mockHA, srv, err := createMockServer("home", []string{"zone.home"})
	if err != nil {
		t.Fatalf("createMockServer failed: %v", err)
	}
	defer mockHA.Close()
	srv.adminPassword = ""

	for _, pw := range []string{"", "cam"} {
		rec := postLogin(srv, pw)
		if rec.Code != http.StatusForbidden {
			t.Errorf("password %q: expected 403 when ADMIN_PASSWORD unset, got %d", pw, rec.Code)
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == "cam_session" && c.Value != "" {
				t.Errorf("password %q: session issued with login disabled", pw)
			}
		}
	}
}

func TestSecretPintModes(t *testing.T) {
	// Start with Cam at The Vic pub
	mockHA, srv, err := createMockServer("The Vic", []string{"zone.the_vic"})
	if err != nil {
		t.Fatalf("createMockServer failed: %v", err)
	}
	defer mockHA.Close()

	cookie := loginAdmin(t, srv)

	// Verify normal state is AtPub
	statusReq := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	statusRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(statusRec, statusReq)

	var st pub.Status
	_ = json.Unmarshal(statusRec.Body.Bytes(), &st)
	if !st.AtPub {
		t.Fatalf("expected initial state to be AtPub=true")
	}

	// 1. Activate Secret Pint STEALTH Mode
	stealthBody := strings.NewReader(`{"mode":"secret_pint_stealth"}`)
	overrideReq := httptest.NewRequest(http.MethodPost, "/api/admin/override", stealthBody)
	overrideReq.Header.Set("Content-Type", "application/json")
	overrideReq.AddCookie(cookie)
	overrideRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(overrideRec, overrideReq)

	if overrideRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", overrideRec.Code)
	}

	// Public API status must now show not at pub!
	statusRec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(statusRec2, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var stStealth pub.Status
	_ = json.Unmarshal(statusRec2.Body.Bytes(), &stStealth)
	if stStealth.AtPub {
		t.Errorf("expected AtPub false in secret_pint_stealth mode")
	}
	if !strings.Contains(stStealth.Headline, "Cam's not at a pub") {
		t.Errorf("unexpected stealth headline: %q", stStealth.Headline)
	}

	// Public HTML page must show not at pub
	indexRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(indexRec, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(indexRec.Body.String(), "Cam is having a pint at") {
		t.Errorf("public page should hide pub in stealth mode")
	}

	// 2. Activate Secret Pint CHEEKY Mode
	cheekyBody := strings.NewReader(`{"mode":"secret_pint_cheeky"}`)
	cheekyReq := httptest.NewRequest(http.MethodPost, "/api/admin/override", cheekyBody)
	cheekyReq.Header.Set("Content-Type", "application/json")
	cheekyReq.AddCookie(cookie)
	cheekyRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(cheekyRec, cheekyReq)

	if cheekyRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", cheekyRec.Code)
	}

	// Public API status must show cheeky headline & secrecy subtext!
	statusRec3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(statusRec3, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var stCheeky pub.Status
	_ = json.Unmarshal(statusRec3.Body.Bytes(), &stCheeky)
	if stCheeky.AtPub {
		t.Errorf("expected AtPub false (location hidden) in cheeky mode")
	}
	if !strings.Contains(stCheeky.Headline, "secret pint") {
		t.Errorf("expected cheeky headline mentioning secret pint, got %q", stCheeky.Headline)
	}
	if !strings.Contains(stCheeky.Subtext, "sworn us to secrecy") {
		t.Errorf("expected cheeky subtext with secrecy quip, got %q", stCheeky.Subtext)
	}

	// 3. Clear Override and resume normal radar
	clearBody := strings.NewReader(`{"mode":"clear"}`)
	clearReq := httptest.NewRequest(http.MethodPost, "/api/admin/override", clearBody)
	clearReq.Header.Set("Content-Type", "application/json")
	clearReq.AddCookie(cookie)
	clearRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(clearRec, clearReq)

	statusRec4 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(statusRec4, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var stCleared pub.Status
	_ = json.Unmarshal(statusRec4.Body.Bytes(), &stCleared)
	if !stCleared.AtPub {
		t.Errorf("expected AtPub to resume true after clearing override")
	}
}

func TestManualPubOverride(t *testing.T) {
	// Start with Cam at home
	mockHA, srv, err := createMockServer("home", []string{"zone.home"})
	if err != nil {
		t.Fatalf("createMockServer failed: %v", err)
	}
	defer mockHA.Close()

	cookie := loginAdmin(t, srv)

	// Admin sets manual pub
	payload := `{"mode":"manual_pub","pub_name":"The Retreat Hotel","pub_address":"280 Sydney Rd, Brunswick","arrived_at":"5:45 PM","notify":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/override", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Public API status must show The Retreat Hotel
	statusRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(statusRec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var st pub.Status
	_ = json.Unmarshal(statusRec.Body.Bytes(), &st)

	if !st.AtPub {
		t.Fatalf("expected AtPub true in manual pub override")
	}
	if st.Pub == nil || st.Pub.Name != "The Retreat Hotel" {
		t.Errorf("expected pub name 'The Retreat Hotel', got %v", st.Pub)
	}
	if st.Pub.Address != "280 Sydney Rd, Brunswick" {
		t.Errorf("expected address '280 Sydney Rd, Brunswick', got %q", st.Pub.Address)
	}
	if st.Pub.ArrivedAt != "5:45 PM" {
		t.Errorf("expected arrived_at '5:45 PM', got %q", st.Pub.ArrivedAt)
	}

	// Public HTML must contain hero name The Retreat Hotel
	indexRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(indexRec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(indexRec.Body.String(), "The Retreat Hotel") {
		t.Errorf("expected index page to contain 'The Retreat Hotel'")
	}

	// Unknown pub: no coordinates, so directions must not point at 0,0
	if st.Directions == nil || strings.Contains(st.Directions.GoogleMaps, "0.000000") {
		t.Errorf("expected address-based directions for unknown pub, got %+v", st.Directions)
	}

	// Known pub zone: coordinates come from the tracked zone
	vicReq := httptest.NewRequest(http.MethodPost, "/api/admin/override", strings.NewReader(`{"mode":"manual_pub","pub_name":"the vic"}`))
	vicReq.AddCookie(cookie)
	srv.Handler().ServeHTTP(httptest.NewRecorder(), vicReq)

	vicRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(vicRec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var vic pub.Status
	_ = json.Unmarshal(vicRec.Body.Bytes(), &vic)
	if vic.Pub == nil || vic.Pub.Latitude != -37.7668 || vic.Pub.Longitude != 144.9589 {
		t.Errorf("expected The Vic's zone coordinates, got %+v", vic.Pub)
	}
	if vic.Pub != nil && !strings.HasSuffix(vic.Pub.ArrivedAt, "AEDT") && !strings.HasSuffix(vic.Pub.ArrivedAt, "AEST") {
		t.Errorf("expected default arrival time in Melbourne time, got %q", vic.Pub.ArrivedAt)
	}
}

func TestClearOverrideResyncsPubState(t *testing.T) {
	// Cam is genuinely at The Vic
	mockHA, srv, err := createMockServer("The Vic", []string{"zone.the_vic"})
	if err != nil {
		t.Fatalf("createMockServer failed: %v", err)
	}
	defer mockHA.Close()

	cookie := loginAdmin(t, srv)
	for _, body := range []string{`{"mode":"secret_pint_stealth"}`, `{"mode":"clear"}`} {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/override", strings.NewReader(body))
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", body, rec.Code)
		}
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !srv.lastPubState || srv.lastPubName != "The Vic" {
		t.Errorf("expected pub state re-synced to The Vic after clear, got %v %q", srv.lastPubState, srv.lastPubName)
	}
}

func TestAdminTools(t *testing.T) {
	mockHA, srv, err := createMockServer("home", []string{"zone.home"})
	if err != nil {
		t.Fatalf("createMockServer failed: %v", err)
	}
	defer mockHA.Close()

	alertsSvc, _ := alerts.NewService(srv.store, "", "", "https://whereis.cam")
	srv.alertsService = alertsSvc

	pushSvc, _ := alerts.NewPushService(srv.store, "cam@whereis.cam")
	srv.pushService = pushSvc

	cookie := loginAdmin(t, srv)

	// 1. Test Alert Email API
	alertBody := strings.NewReader(`{"to":"cam@camcamcam.cam","pub":"The Grandview"}`)
	alertReq := httptest.NewRequest(http.MethodPost, "/api/admin/test-alert", alertBody)
	alertReq.Header.Set("Content-Type", "application/json")
	alertReq.AddCookie(cookie)
	alertRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(alertRec, alertReq)

	if alertRec.Code != http.StatusOK {
		t.Errorf("expected 200 OK from test-alert, got %d (body: %s)", alertRec.Code, alertRec.Body.String())
	}

	// 2. Deactivate Subscriber API
	sub, _, _ := srv.store.SubscribeEmail("delete-me@example.com")
	deactBody := strings.NewReader(fmt.Sprintf(`{"id":%d}`, sub.ID))
	deactReq := httptest.NewRequest(http.MethodPost, "/api/admin/subscribers/unsubscribe", deactBody)
	deactReq.Header.Set("Content-Type", "application/json")
	deactReq.AddCookie(cookie)
	deactRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(deactRec, deactReq)

	if deactRec.Code != http.StatusOK {
		t.Errorf("expected 200 OK from deactivate, got %d", deactRec.Code)
	}

	active, _ := srv.store.ActiveSubscribers()
	if len(active) != 0 {
		t.Errorf("expected 0 active subscribers after admin deactivation, got %d", len(active))
	}

	// 3. Test Push Notification API validation
	pushBody := strings.NewReader(`{"endpoint":"","keys":{"p256dh":"","auth":""}}`)
	pushReq := httptest.NewRequest(http.MethodPost, "/api/admin/test-push", pushBody)
	pushReq.Header.Set("Content-Type", "application/json")
	pushReq.AddCookie(cookie)
	pushRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(pushRec, pushReq)

	if pushRec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty push endpoint, got %d", pushRec.Code)
	}

	// 4. Test push must refuse non-push-service endpoints (SSRF)
	ssrfBody := strings.NewReader(`{"endpoint":"http://10.0.0.5:8123/api/states","keys":{"p256dh":"x","auth":"y"}}`)
	ssrfReq := httptest.NewRequest(http.MethodPost, "/api/admin/test-push", ssrfBody)
	ssrfReq.AddCookie(cookie)
	ssrfRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(ssrfRec, ssrfReq)
	if ssrfRec.Code != http.StatusBadRequest && ssrfRec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected internal endpoint to be refused, got %d", ssrfRec.Code)
	}
}
