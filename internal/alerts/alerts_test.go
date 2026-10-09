package alerts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"TEST@example.com", "test@example.com", false},
		{"  user+tag@domain.org  ", "user+tag@domain.org", false},
		{"invalid-email", "", true},
		{"@nodomain", "", true},
		{"user@nodot", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		got, err := NormalizeEmail(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("NormalizeEmail(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSubscribeAndUnsubscribe(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "subscribers.json")

	svc, err := NewService(filePath, "test-key", "test@whereis.cam", "https://whereis.cam")
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	// 1. Subscribe new email
	sub, isNew, err := svc.Subscribe("mate@example.com")
	if err != nil {
		t.Fatalf("unexpected subscribe error: %v", err)
	}
	if !isNew {
		t.Errorf("expected isNew=true for first subscription")
	}
	if sub.Email != "mate@example.com" || sub.Token == "" || !sub.Active {
		t.Errorf("unexpected subscriber data: %+v", sub)
	}
	if svc.CountActive() != 1 {
		t.Errorf("expected 1 active subscriber, got %d", svc.CountActive())
	}

	// 2. Subscribe existing active email
	sub2, isNew2, err := svc.Subscribe("MATE@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isNew2 {
		t.Errorf("expected isNew=false for duplicate subscription")
	}
	if sub2.Token != sub.Token {
		t.Errorf("expected same token for duplicate subscription")
	}

	// 3. Unsubscribe
	if !svc.Unsubscribe(sub.Token) {
		t.Errorf("expected Unsubscribe to return true")
	}
	if svc.CountActive() != 0 {
		t.Errorf("expected 0 active subscribers after unsubscribe, got %d", svc.CountActive())
	}

	// 4. Persistence check: reload from disk
	svc2, err := NewService(filePath, "test-key", "test@whereis.cam", "https://whereis.cam")
	if err != nil {
		t.Fatalf("failed to reload service: %v", err)
	}
	if svc2.CountActive() != 0 {
		t.Errorf("expected 0 active after reload, got %d", svc2.CountActive())
	}

	// 5. Re-subscribe inactive email
	sub3, isNew3, err := svc2.Subscribe("mate@example.com")
	if err != nil {
		t.Fatalf("unexpected error re-subscribing: %v", err)
	}
	if isNew3 {
		t.Errorf("expected isNew=false when reactivating")
	}
	if !sub3.Active {
		t.Errorf("expected subscriber to be active again")
	}
	if svc2.CountActive() != 1 {
		t.Errorf("expected 1 active subscriber, got %d", svc2.CountActive())
	}
}

func TestSendEmailResendAPI(t *testing.T) {
	var capturedPayload map[string]interface{}
	var capturedAuth string

	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&capturedPayload)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": "msg_123"}`))
	}))
	defer mockResend.Close()

	svc := &Service{
		apiKey:     "re_test123",
		fromEmail:  "Cam <alerts@whereis.cam>",
		httpClient: mockResend.Client(),
	}

	// Override URL via transport rewrite
	transport := mockResend.Client().Transport
	svc.httpClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = mockResend.Listener.Addr().String()
		return transport.RoundTrip(req)
	})

	err := svc.SendEmail("friend@example.com", "Test Subject", "<p>Hello</p>")
	if err != nil {
		t.Fatalf("unexpected send error: %v", err)
	}

	if capturedAuth != "Bearer re_test123" {
		t.Errorf("unexpected auth header: %q", capturedAuth)
	}
	if capturedPayload["subject"] != "Test Subject" {
		t.Errorf("unexpected subject: %v", capturedPayload["subject"])
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
