package alerts

import (
	"path/filepath"
	"testing"

	"whereis.cam/internal/storage"
)

func TestNormalizeEmail(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"  CAM@EXAMPLE.COM  ", "cam@example.com"},
		{"test.person+tag@domain.co.uk", "test.person+tag@domain.co.uk"},
		{"user@whereis.cam", "user@whereis.cam"},
	}

	for _, c := range cases {
		got := storage.NormalizeEmail(c.input)
		if got != c.want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestSubscribeAndUnsubscribe(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := storage.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}
	defer db.Close()

	svc, err := NewService(db, "", "", "")
	if err != nil {
		t.Fatalf("failed to create Service: %v", err)
	}

	sub, isNew, err := svc.Subscribe("mate@example.com")
	if err != nil {
		t.Fatalf("unexpected error on subscribe: %v", err)
	}
	if !isNew {
		t.Errorf("expected isNew to be true")
	}
	if sub.Email != "mate@example.com" {
		t.Errorf("expected email mate@example.com, got %s", sub.Email)
	}

	subs := svc.ActiveSubscribers()
	if len(subs) != 1 {
		t.Fatalf("expected 1 active subscriber, got %d", len(subs))
	}

	// Unsubscribe
	if !svc.Unsubscribe(sub.Token) {
		t.Fatalf("expected unsubscribe to succeed")
	}

	subsAfter := svc.ActiveSubscribers()
	if len(subsAfter) != 0 {
		t.Fatalf("expected 0 active subscribers, got %d", len(subsAfter))
	}
}
