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
	endpoint := "https://fcm.googleapis.com/fcm/send/sub-123"
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

func TestValidatePushEndpoint(t *testing.T) {
	valid := []string{
		"https://fcm.googleapis.com/fcm/send/abc",
		"https://updates.push.services.mozilla.com/wpush/v2/abc",
		"https://web.push.apple.com/abc",
		"https://wns2-by3p.notify.windows.com/w/?token=abc",
	}
	for _, e := range valid {
		if err := ValidatePushEndpoint(e); err != nil {
			t.Errorf("expected %q to be valid, got %v", e, err)
		}
	}

	invalid := []string{
		"",
		"http://fcm.googleapis.com/fcm/send/abc",
		"https://10.0.0.5:8123/api/states",
		"https://169.254.169.254/latest/meta-data",
		"https://fcm.googleapis.com:8443/fcm/send/abc",
		"https://evil.com/fcm.googleapis.com",
		"https://fcm.googleapis.com.evil.com/abc",
		"https://user@fcm.googleapis.com/abc",
	}
	for _, e := range invalid {
		if err := ValidatePushEndpoint(e); err == nil {
			t.Errorf("expected %q to be rejected", e)
		}
	}
}
