package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStorageLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// 1. Subscribe Email
	sub, isNew, err := db.SubscribeEmail("Mate@Example.com")
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if !isNew {
		t.Errorf("expected isNew true")
	}
	if sub.Email != "mate@example.com" {
		t.Errorf("expected normalized email mate@example.com, got %s", sub.Email)
	}
	if len(sub.Token) != 32 {
		t.Errorf("expected 32-char hex token, got %s", sub.Token)
	}

	// Active subscribers count
	active, err := db.ActiveSubscribers()
	if err != nil || len(active) != 1 {
		t.Fatalf("expected 1 active subscriber, got %d (err: %v)", len(active), err)
	}

	// 2. Unsubscribe Email
	ok := db.UnsubscribeEmail(sub.Token)
	if !ok {
		t.Errorf("expected unsubscribe to succeed")
	}
	activeAfter, _ := db.ActiveSubscribers()
	if len(activeAfter) != 0 {
		t.Errorf("expected 0 active subscribers after unsub, got %d", len(activeAfter))
	}

	// Re-subscribe
	sub2, isReNew, err := db.SubscribeEmail("mate@example.com")
	if err != nil || !isReNew || !sub2.Active {
		t.Errorf("expected successful resubscription, got sub: %v, err: %v", sub2, err)
	}

	// 3. Push Subscriptions
	endpoint := "https://push.example.com/sub/abc"
	if err := db.SavePushSubscription(endpoint, "p256dh-key", "auth-key"); err != nil {
		t.Fatalf("failed to save push sub: %v", err)
	}

	pushSubs, err := db.ActivePushSubscriptions()
	if err != nil || len(pushSubs) != 1 {
		t.Fatalf("expected 1 active push sub, got %d (err: %v)", len(pushSubs), err)
	}

	if err := db.DeactivatePushSubscription(endpoint); err != nil {
		t.Fatalf("failed to deactivate push: %v", err)
	}
	pushSubsAfter, _ := db.ActivePushSubscriptions()
	if len(pushSubsAfter) != 0 {
		t.Errorf("expected 0 active push subs after deactivation, got %d", len(pushSubsAfter))
	}

	// 4. Settings
	if err := db.SetSetting("vapid_public_key", "test-pub-key"); err != nil {
		t.Fatalf("failed to set setting: %v", err)
	}
	val, err := db.GetSetting("vapid_public_key")
	if err != nil || val != "test-pub-key" {
		t.Errorf("expected setting test-pub-key, got %q (err: %v)", val, err)
	}
}

func TestLegacyJSONMigration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "migrated.db")

	subsJSON := filepath.Join(tmpDir, "subs.json")
	vapidJSON := filepath.Join(tmpDir, "vapid.json")
	pushJSON := filepath.Join(tmpDir, "push.json")

	_ = os.WriteFile(subsJSON, []byte(`[{"email":"legacy@example.com","token":"legtoken123","created_at":"2026-10-09T00:00:00Z","active":true}]`), 0644)
	_ = os.WriteFile(vapidJSON, []byte(`{"public_key":"leg-pub","private_key":"leg-priv"}`), 0644)
	_ = os.WriteFile(pushJSON, []byte(`[{"endpoint":"https://push.legacy.com/1","p256dh":"k","auth":"a","created_at":"2026-10-09T00:00:00Z","active":true}]`), 0644)

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	db.ImportLegacyJSON(subsJSON, vapidJSON, pushJSON)

	subs, _ := db.ActiveSubscribers()
	if len(subs) != 1 || subs[0].Email != "legacy@example.com" {
		t.Errorf("expected migrated subscriber, got %v", subs)
	}

	push, _ := db.ActivePushSubscriptions()
	if len(push) != 1 || push[0].Endpoint != "https://push.legacy.com/1" {
		t.Errorf("expected migrated push endpoint, got %v", push)
	}

	pubKey, _ := db.GetSetting("vapid_public_key")
	if pubKey != "leg-pub" {
		t.Errorf("expected migrated vapid key leg-pub, got %s", pubKey)
	}
}
