package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestHitsTrackingAndStats(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "hits.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// Record hits
	_ = db.RecordHit("/", "1.2.3.4", "Mozilla/5.0", "https://google.com")
	_ = db.RecordHit("/", "1.2.3.4", "Mozilla/5.0", "")
	_ = db.RecordHit("/api/status", "5.6.7.8", "curl/7.68.0", "")

	stats, err := db.GetHitStats()
	if err != nil {
		t.Fatalf("failed to get hit stats: %v", err)
	}

	if stats.TotalHits != 3 {
		t.Errorf("expected 3 total hits, got %d", stats.TotalHits)
	}
	if stats.HitsToday != 3 {
		t.Errorf("expected 3 hits today, got %d", stats.HitsToday)
	}
	if stats.UniqueIPs != 2 {
		t.Errorf("expected 2 unique IPs, got %d", stats.UniqueIPs)
	}
	if len(stats.TopPaths) < 2 {
		t.Errorf("expected at least 2 top paths, got %d", len(stats.TopPaths))
	}
	if len(stats.RecentHits) != 3 {
		t.Errorf("expected 3 recent hits, got %d", len(stats.RecentHits))
	}
}

func TestOverrideState(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "override.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// Default state
	initial, err := db.GetOverrideState()
	if err != nil {
		t.Fatalf("failed to get override state: %v", err)
	}
	if initial.Mode != "none" {
		t.Errorf("expected initial mode 'none', got %q", initial.Mode)
	}

	// Set secret pint cheeky mode
	err = db.SetOverrideState(OverrideState{
		Mode: "secret_pint_cheeky",
	})
	if err != nil {
		t.Fatalf("failed to set cheeky secret pint: %v", err)
	}
	cheeky, _ := db.GetOverrideState()
	if cheeky.Mode != "secret_pint_cheeky" {
		t.Errorf("expected secret_pint_cheeky, got %q", cheeky.Mode)
	}

	// Set manual pub mode
	err = db.SetOverrideState(OverrideState{
		Mode:       "manual_pub",
		PubName:    "The Retreat Hotel",
		PubAddress: "280 Sydney Rd, Brunswick",
		ArrivedAt:  "5:30 PM",
	})
	if err != nil {
		t.Fatalf("failed to set manual pub: %v", err)
	}

	manual, _ := db.GetOverrideState()
	if manual.Mode != "manual_pub" || manual.PubName != "The Retreat Hotel" || manual.ArrivedAt != "5:30 PM" {
		t.Errorf("unexpected manual state: %+v", manual)
	}

	// Clear override
	if err := db.ClearOverrideState(); err != nil {
		t.Fatalf("failed to clear override: %v", err)
	}
	cleared, _ := db.GetOverrideState()
	if cleared.Mode != "none" {
		t.Errorf("expected mode 'none' after clear, got %q", cleared.Mode)
	}
}

func TestSubscribersManagement(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subs_mgmt.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	sub, _, err := db.SubscribeEmail("admin@example.com")
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	all, err := db.AllSubscribers(10, 0)
	if err != nil || len(all) != 1 {
		t.Fatalf("expected 1 subscriber, got %d", len(all))
	}
	if all[0].CreatedAt.IsZero() {
		t.Errorf("expected non-zero CreatedAt for subscriber")
	}

	if err := db.DeactivateEmailSubscriber(sub.ID); err != nil {
		t.Fatalf("failed to deactivate subscriber: %v", err)
	}

	active, _ := db.ActiveSubscribers()
	if len(active) != 0 {
		t.Errorf("expected 0 active subscribers after deactivation, got %d", len(active))
	}
}

func TestLegacyTimeParsing(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "legacy_time.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// Insert legacy unparsed timestamp formats
	legacyDate := "2026-10-09 15:57:06.485462 +1100 AEDT m=+84.375224251"
	_, err = db.conn.Exec("INSERT INTO subscribers (email, token, created_at, active) VALUES (?, ?, ?, 1)", "legacy@example.com", "token123", legacyDate)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	subs, err := db.AllSubscribers(10, 0)
	if err != nil {
		t.Fatalf("AllSubscribers failed: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscriber, got %d", len(subs))
	}
	if subs[0].CreatedAt.Year() != 2026 {
		t.Errorf("expected year 2026, got %d", subs[0].CreatedAt.Year())
	}
}

func TestDriverTimeFormatParsing(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "driver_time.db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// Rows written by the pre-SQLite-normalization code bound time.Time directly,
	// which go-sqlite3 stores in this layout.
	raw := "2026-10-20 18:30:00.123456789+11:00"
	if _, err := db.conn.Exec("INSERT INTO subscribers (email, token, created_at, active) VALUES (?, ?, ?, 1)", "driver@example.com", "tok-driver", raw); err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	if _, err := parseFlexibleTime(raw); err != nil {
		t.Fatalf("parseFlexibleTime(%q) failed: %v", raw, err)
	}

	subs, err := db.AllSubscribers(10, 0)
	if err != nil || len(subs) != 1 {
		t.Fatalf("expected 1 subscriber, got %d (err %v)", len(subs), err)
	}
	if subs[0].CreatedAt.IsZero() || subs[0].CreatedAt.Day() != 20 {
		t.Errorf("expected CreatedAt on Oct 20, got %v", subs[0].CreatedAt)
	}

	if n, err := db.CountSubscribers(); err != nil || n != 1 {
		t.Errorf("expected CountSubscribers 1, got %d (err %v)", n, err)
	}
}

func TestPruneHits(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "prune.db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	old := time.Now().UTC().AddDate(0, 0, -120)
	if _, err := db.conn.Exec("INSERT INTO page_hits (path, ip, user_agent, referer, created_at) VALUES ('/', '', '', '', ?)", old); err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	_ = db.RecordHit("/", "1.2.3.4", "ua", "")

	n, err := db.PruneHits(time.Now().AddDate(0, 0, -90))
	if err != nil || n != 1 {
		t.Fatalf("expected 1 pruned hit, got %d (err %v)", n, err)
	}
	stats, _ := db.GetHitStats()
	if stats.TotalHits != 1 {
		t.Errorf("expected 1 remaining hit, got %d", stats.TotalHits)
	}
}
