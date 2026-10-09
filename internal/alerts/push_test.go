package alerts

import (
	"path/filepath"
	"testing"

	"whereis.cam/internal/storage"
)

func TestPushServiceLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := storage.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}
	defer db.Close()

	ps, err := NewPushService(db, "admin@whereis.cam")
	if err != nil {
		t.Fatalf("failed to create PushService: %v", err)
	}

	pubKey := ps.VAPIDPublicKey()
	if pubKey == "" {
		t.Errorf("expected non-empty VAPID public key")
	}

	// Subscribe
	endpoint := "https://push.example.com/sub/123"
	if err := ps.Subscribe(endpoint, "test-p256dh", "test-auth"); err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	if len(ps.ActiveSubscriptions()) != 1 {
		t.Errorf("expected 1 active subscription, got %d", len(ps.ActiveSubscriptions()))
	}

	// Unsubscribe
	if err := ps.Unsubscribe(endpoint); err != nil {
		t.Fatalf("failed to unsubscribe: %v", err)
	}

	if len(ps.ActiveSubscriptions()) != 0 {
		t.Errorf("expected 0 active subscriptions, got %d", len(ps.ActiveSubscriptions()))
	}
}
