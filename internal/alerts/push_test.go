package alerts

import (
	"path/filepath"
	"testing"
)

func TestPushServiceLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	subsPath := filepath.Join(tmpDir, "push_subs.json")
	keysPath := filepath.Join(tmpDir, "vapid_keys.json")

	ps, err := NewPushService(subsPath, keysPath, "admin@whereis.cam")
	if err != nil {
		t.Fatalf("failed to create PushService: %v", err)
	}

	pubKey := ps.VAPIDPublicKey()
	if pubKey == "" {
		t.Errorf("expected non-empty VAPID public key")
	}

	// 1. Subscribe
	sub := PushSubscription{
		Endpoint: "https://push.example.com/sub/123",
		P256DH:   "test-p256dh-key",
		Auth:     "test-auth-secret",
	}

	if err := ps.Subscribe(sub); err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	if len(ps.ActiveSubscriptions()) != 1 {
		t.Errorf("expected 1 active subscription, got %d", len(ps.ActiveSubscriptions()))
	}

	// 2. Unsubscribe
	if err := ps.Unsubscribe(sub.Endpoint); err != nil {
		t.Fatalf("failed to unsubscribe: %v", err)
	}

	if len(ps.ActiveSubscriptions()) != 0 {
		t.Errorf("expected 0 active subscriptions, got %d", len(ps.ActiveSubscriptions()))
	}

	// 3. Persistence: reload with existing VAPID keys
	ps2, err := NewPushService(subsPath, keysPath, "admin@whereis.cam")
	if err != nil {
		t.Fatalf("failed to reload PushService: %v", err)
	}

	if ps2.VAPIDPublicKey() != pubKey {
		t.Errorf("expected persistent VAPID key to match: got %s, want %s", ps2.VAPIDPublicKey(), pubKey)
	}
}
