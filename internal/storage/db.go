package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattn/go-sqlite3"
)

// Subscriber represents an email subscriber.
type Subscriber struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
	Active    bool      `json:"active"`
}

// PushSubscription represents a Web Push device registration.
type PushSubscription struct {
	ID        int64     `json:"id"`
	Endpoint  string    `json:"endpoint"`
	P256DH    string    `json:"p256dh"`
	Auth      string    `json:"auth"`
	CreatedAt time.Time `json:"created_at"`
	Active    bool      `json:"active"`
}

// DB wraps the SQLite connection and queries.
type DB struct {
	conn *sql.DB
}

// Open creates the directory if needed, opens SQLite, and applies schema migrations.
func Open(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Recommended SQLite pragmas for concurrent reliability & performance
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA foreign_keys = ON;",
	}
	for _, p := range pragmas {
		if _, err := conn.Exec(p); err != nil {
			conn.Close()
			return nil, fmt.Errorf("failed to execute pragma: %w", err)
		}
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	return db, nil
}

// Close closes the underlying SQLite connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS subscribers (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		email TEXT UNIQUE NOT NULL,
		token TEXT UNIQUE NOT NULL,
		created_at DATETIME NOT NULL,
		active BOOLEAN NOT NULL DEFAULT 1,
		unsubscribed_at DATETIME
	);

	CREATE INDEX IF NOT EXISTS idx_subscribers_token ON subscribers(token);
	CREATE INDEX IF NOT EXISTS idx_subscribers_active ON subscribers(active);

	CREATE TABLE IF NOT EXISTS push_subscriptions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		endpoint TEXT UNIQUE NOT NULL,
		p256dh TEXT NOT NULL,
		auth TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		active BOOLEAN NOT NULL DEFAULT 1
	);

	CREATE INDEX IF NOT EXISTS idx_push_active ON push_subscriptions(active);

	CREATE TABLE IF NOT EXISTS app_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS pub_visits (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		pub_name TEXT NOT NULL,
		arrived_at DATETIME NOT NULL,
		departed_at DATETIME,
		duration_minutes INTEGER
	);

	CREATE TABLE IF NOT EXISTS page_hits (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		path TEXT NOT NULL,
		ip TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT '',
		referer TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_page_hits_created_at ON page_hits(created_at);
	CREATE INDEX IF NOT EXISTS idx_page_hits_path ON page_hits(path);
	`
	if _, err := db.conn.Exec(schema); err != nil {
		return err
	}

	// Auto-normalize legacy timestamp formats in subscribers table
	rows, err := db.conn.Query("SELECT id, CAST(created_at AS TEXT) FROM subscribers")
	if err == nil {
		type fixItem struct {
			id int64
			t  time.Time
		}
		var toFix []fixItem
		for rows.Next() {
			var id int64
			var raw string
			if err := rows.Scan(&id, &raw); err == nil {
				if parsed, pErr := parseFlexibleTime(raw); pErr == nil {
					toFix = append(toFix, fixItem{id: id, t: parsed})
				}
			}
		}
		rows.Close()
		for _, item := range toFix {
			_, _ = db.conn.Exec("UPDATE subscribers SET created_at = ? WHERE id = ?", item.t.Format(time.RFC3339), item.id)
		}
	}

	return nil
}

// parseFlexibleTime parses various legacy and standard datetime formats.
func parseFlexibleTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty time string")
	}
	if idx := strings.Index(raw, " m="); idx != -1 {
		raw = raw[:idx]
	}
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999 -0700 MST",
		"2006-01-02 15:04:05.999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	// Layouts go-sqlite3 itself writes when a time.Time is bound directly,
	// e.g. "2026-10-20 18:30:00.123456789+11:00".
	formats = append(formats, sqlite3.SQLiteTimestampFormats...)
	for _, f := range formats {
		if t, err := time.Parse(f, raw); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse time: %s", raw)
}

// ── Email Subscriber Methods ──

// NormalizeEmail cleans and normalizes email strings.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func generateToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SubscribeEmail adds or re-activates an email subscriber.
// Returns subscriber, isNew flag, and error.
func (db *DB) SubscribeEmail(rawEmail string) (Subscriber, bool, error) {
	email := NormalizeEmail(rawEmail)
	if email == "" || !strings.Contains(email, "@") {
		return Subscriber{}, false, fmt.Errorf("invalid email address")
	}

	var sub Subscriber
	var unsubscribedAt sql.NullTime
	var rawCreated string
	row := db.conn.QueryRow("SELECT id, email, token, CAST(created_at AS TEXT), active, unsubscribed_at FROM subscribers WHERE email = ?", email)
	err := row.Scan(&sub.ID, &sub.Email, &sub.Token, &rawCreated, &sub.Active, &unsubscribedAt)
	if err == nil {
		sub.CreatedAt, _ = parseFlexibleTime(rawCreated)
	}

	if err == sql.ErrNoRows {
		// New subscriber
		token := generateToken()
		now := time.Now()
		res, err := db.conn.Exec(
			"INSERT INTO subscribers (email, token, created_at, active) VALUES (?, ?, ?, 1)",
			email, token, now.Format(time.RFC3339),
		)
		if err != nil {
			return Subscriber{}, false, err
		}
		id, _ := res.LastInsertId()
		return Subscriber{
			ID:        id,
			Email:     email,
			Token:     token,
			CreatedAt: now,
			Active:    true,
		}, true, nil
	} else if err != nil {
		return Subscriber{}, false, err
	}

	// Existing subscriber
	if !sub.Active {
		_, err := db.conn.Exec("UPDATE subscribers SET active = 1, unsubscribed_at = NULL WHERE id = ?", sub.ID)
		if err != nil {
			return Subscriber{}, false, err
		}
		sub.Active = true
		return sub, true, nil
	}

	return sub, false, nil
}

// UnsubscribeEmail marks a subscriber as inactive by their unique unsubscribe token.
func (db *DB) UnsubscribeEmail(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}

	now := time.Now()
	res, err := db.conn.Exec("UPDATE subscribers SET active = 0, unsubscribed_at = ? WHERE token = ? AND active = 1", now, token)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// ActiveSubscribers returns all active email subscribers.
func (db *DB) ActiveSubscribers() ([]Subscriber, error) {
	rows, err := db.conn.Query("SELECT id, email, token, CAST(created_at AS TEXT), active FROM subscribers WHERE active = 1 ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Subscriber
	for rows.Next() {
		var s Subscriber
		var rawCreated string
		if err := rows.Scan(&s.ID, &s.Email, &s.Token, &rawCreated, &s.Active); err != nil {
			continue
		}
		s.CreatedAt, _ = parseFlexibleTime(rawCreated)
		list = append(list, s)
	}
	return list, nil
}

// AllSubscribers returns one page of email subscribers, newest first.
func (db *DB) AllSubscribers(limit, offset int) ([]Subscriber, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := db.conn.Query("SELECT id, email, token, CAST(created_at AS TEXT), active FROM subscribers ORDER BY id DESC LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Subscriber
	for rows.Next() {
		var s Subscriber
		var rawCreated string
		if err := rows.Scan(&s.ID, &s.Email, &s.Token, &rawCreated, &s.Active); err != nil {
			continue
		}
		s.CreatedAt, _ = parseFlexibleTime(rawCreated)
		list = append(list, s)
	}
	return list, nil
}

// CountSubscribers returns the total number of email subscribers, active or not.
func (db *DB) CountSubscribers() (int, error) {
	var n int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM subscribers").Scan(&n)
	return n, err
}

// DeactivateEmailSubscriber marks an email subscriber inactive by ID.
func (db *DB) DeactivateEmailSubscriber(id int64) error {
	now := time.Now()
	_, err := db.conn.Exec("UPDATE subscribers SET active = 0, unsubscribed_at = ? WHERE id = ?", now, id)
	return err
}

// ── Web Push Subscription Methods ──

// SavePushSubscription inserts or activates a browser push subscription.
func (db *DB) SavePushSubscription(endpoint, p256dh, auth string) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" || p256dh == "" || auth == "" {
		return fmt.Errorf("invalid push subscription fields")
	}

	now := time.Now()
	_, err := db.conn.Exec(`
		INSERT INTO push_subscriptions (endpoint, p256dh, auth, created_at, active)
		VALUES (?, ?, ?, ?, 1)
		ON CONFLICT(endpoint) DO UPDATE SET
			p256dh = excluded.p256dh,
			auth = excluded.auth,
			active = 1;
	`, endpoint, p256dh, auth, now)
	return err
}

// DeactivatePushSubscription marks a push endpoint as inactive.
func (db *DB) DeactivatePushSubscription(endpoint string) error {
	_, err := db.conn.Exec("UPDATE push_subscriptions SET active = 0 WHERE endpoint = ?", strings.TrimSpace(endpoint))
	return err
}

// ActivePushSubscriptions returns all active push subscriptions.
func (db *DB) ActivePushSubscriptions() ([]PushSubscription, error) {
	rows, err := db.conn.Query("SELECT id, endpoint, p256dh, auth, created_at, active FROM push_subscriptions WHERE active = 1 ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []PushSubscription
	for rows.Next() {
		var p PushSubscription
		if err := rows.Scan(&p.ID, &p.Endpoint, &p.P256DH, &p.Auth, &p.CreatedAt, &p.Active); err != nil {
			continue
		}
		list = append(list, p)
	}
	return list, nil
}

// ── App Settings (VAPID Keys & State) Methods ──

// GetSetting retrieves a key-value setting.
func (db *DB) GetSetting(key string) (string, error) {
	var val string
	err := db.conn.QueryRow("SELECT value FROM app_settings WHERE key = ?", key).Scan(&val)
	if err != nil {
		return "", err
	}
	return val, nil
}

// SetSetting saves or updates a key-value setting.
func (db *DB) SetSetting(key, value string) error {
	_, err := db.conn.Exec(`
		INSERT INTO app_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value;
	`, key, value)
	return err
}

// ── JSON Legacy Importer ──

// ImportLegacyJSON imports subscribers, VAPID keys, and push endpoints from JSON if tables are empty.
func (db *DB) ImportLegacyJSON(subscribersJSONPath, vapidKeysJSONPath, pushSubsJSONPath string) {
	// 1. Import Email Subscribers
	var subCount int
	_ = db.conn.QueryRow("SELECT COUNT(*) FROM subscribers").Scan(&subCount)
	if subCount == 0 && subscribersJSONPath != "" {
		if data, err := os.ReadFile(subscribersJSONPath); err == nil {
			type legacySub struct {
				Email     string    `json:"email"`
				Token     string    `json:"token"`
				CreatedAt time.Time `json:"created_at"`
				Active    bool      `json:"active"`
			}
			var list []legacySub
			if err := json.Unmarshal(data, &list); err == nil {
				for _, s := range list {
					if s.Email != "" && s.Token != "" {
						_, _ = db.conn.Exec(
							"INSERT OR IGNORE INTO subscribers (email, token, created_at, active) VALUES (?, ?, ?, ?)",
							s.Email, s.Token, s.CreatedAt.Format(time.RFC3339), s.Active,
						)
					}
				}
			}
		}
	}

	// 2. Import VAPID Keys
	var vapidPub string
	_ = db.conn.QueryRow("SELECT value FROM app_settings WHERE key = 'vapid_public_key'").Scan(&vapidPub)
	if vapidPub == "" && vapidKeysJSONPath != "" {
		if data, err := os.ReadFile(vapidKeysJSONPath); err == nil {
			var keys struct {
				PublicKey  string `json:"public_key"`
				PrivateKey string `json:"private_key"`
			}
			if err := json.Unmarshal(data, &keys); err == nil && keys.PublicKey != "" && keys.PrivateKey != "" {
				_ = db.SetSetting("vapid_public_key", keys.PublicKey)
				_ = db.SetSetting("vapid_private_key", keys.PrivateKey)
			}
		}
	}

	// 3. Import Push Subscriptions
	var pushCount int
	_ = db.conn.QueryRow("SELECT COUNT(*) FROM push_subscriptions").Scan(&pushCount)
	if pushCount == 0 && pushSubsJSONPath != "" {
		if data, err := os.ReadFile(pushSubsJSONPath); err == nil {
			type legacyPush struct {
				Endpoint  string    `json:"endpoint"`
				P256DH    string    `json:"p256dh"`
				Auth      string    `json:"auth"`
				CreatedAt time.Time `json:"created_at"`
				Active    bool      `json:"active"`
			}
			var list []legacyPush
			if err := json.Unmarshal(data, &list); err == nil {
				for _, p := range list {
					if p.Endpoint != "" && p.P256DH != "" && p.Auth != "" {
						_, _ = db.conn.Exec(
							"INSERT OR IGNORE INTO push_subscriptions (endpoint, p256dh, auth, created_at, active) VALUES (?, ?, ?, ?, ?)",
							p.Endpoint, p.P256DH, p.Auth, p.CreatedAt, p.Active,
						)
					}
				}
			}
		}
	}
}

// ── Page Hits & Analytics ──

// PageHit represents an individual request hit.
type PageHit struct {
	ID        int64     `json:"id"`
	Path      string    `json:"path"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	Referer   string    `json:"referer"`
	CreatedAt time.Time `json:"created_at"`
}

// PathStat represents hit count for a specific path.
type PathStat struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// HitStats aggregates page view analytics.
type HitStats struct {
	TotalHits  int        `json:"total_hits"`
	HitsToday  int        `json:"hits_today"`
	Hits7Days  int        `json:"hits_7_days"`
	UniqueIPs  int        `json:"unique_ips"`
	TopPaths   []PathStat `json:"top_paths"`
	RecentHits []PageHit  `json:"recent_hits"`
}

// OverrideState captures the manual pub or secret pint settings.
type OverrideState struct {
	Mode       string    `json:"mode"` // "none", "secret_pint_stealth", "secret_pint_cheeky", "manual_pub"
	PubName    string    `json:"pub_name"`
	PubAddress string    `json:"pub_address"`
	ArrivedAt  string    `json:"arrived_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// RecordHit saves an incoming HTTP request into page_hits.
func (db *DB) RecordHit(path, ip, userAgent, referer string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/"
	}
	now := time.Now().UTC()
	_, err := db.conn.Exec(
		"INSERT INTO page_hits (path, ip, user_agent, referer, created_at) VALUES (?, ?, ?, ?, ?)",
		path, ip, userAgent, referer, now,
	)
	return err
}

// PruneHits deletes page hits recorded before cutoff and returns how many were removed.
func (db *DB) PruneHits(cutoff time.Time) (int64, error) {
	res, err := db.conn.Exec("DELETE FROM page_hits WHERE created_at < ?", cutoff.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// GetHitStats returns aggregated metrics and recent hits.
func (db *DB) GetHitStats() (HitStats, error) {
	var stats HitStats
	now := time.Now().UTC()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	sevenDaysAgo := now.AddDate(0, 0, -7)

	// 1. Total hits
	_ = db.conn.QueryRow("SELECT COUNT(*) FROM page_hits").Scan(&stats.TotalHits)

	// 2. Hits today
	_ = db.conn.QueryRow("SELECT COUNT(*) FROM page_hits WHERE created_at >= ?", startOfToday).Scan(&stats.HitsToday)

	// 3. Hits last 7 days
	_ = db.conn.QueryRow("SELECT COUNT(*) FROM page_hits WHERE created_at >= ?", sevenDaysAgo).Scan(&stats.Hits7Days)

	// 4. Unique IPs
	_ = db.conn.QueryRow("SELECT COUNT(DISTINCT ip) FROM page_hits WHERE ip != ''").Scan(&stats.UniqueIPs)

	// 5. Top paths (limit 10)
	rows, err := db.conn.Query("SELECT path, COUNT(*) as cnt FROM page_hits GROUP BY path ORDER BY cnt DESC LIMIT 10")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p PathStat
			if err := rows.Scan(&p.Path, &p.Count); err == nil {
				stats.TopPaths = append(stats.TopPaths, p)
			}
		}
	}

	// 6. Recent hits (limit 25)
	rRows, err := db.conn.Query("SELECT id, path, ip, user_agent, referer, created_at FROM page_hits ORDER BY id DESC LIMIT 25")
	if err == nil {
		defer rRows.Close()
		for rRows.Next() {
			var h PageHit
			if err := rRows.Scan(&h.ID, &h.Path, &h.IP, &h.UserAgent, &h.Referer, &h.CreatedAt); err == nil {
				stats.RecentHits = append(stats.RecentHits, h)
			}
		}
	}

	return stats, nil
}

// GetOverrideState retrieves the current manual override or secret pint mode from app_settings.
func (db *DB) GetOverrideState() (OverrideState, error) {
	mode, _ := db.GetSetting("override_mode")
	if mode == "" {
		mode = "none"
	}
	pubName, _ := db.GetSetting("override_pub_name")
	pubAddress, _ := db.GetSetting("override_pub_address")
	arrivedAt, _ := db.GetSetting("override_arrived_at")
	updatedStr, _ := db.GetSetting("override_updated_at")

	var updatedAt time.Time
	if updatedStr != "" {
		updatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	}

	return OverrideState{
		Mode:       mode,
		PubName:    pubName,
		PubAddress: pubAddress,
		ArrivedAt:  arrivedAt,
		UpdatedAt:  updatedAt,
	}, nil
}

// SetOverrideState saves or updates the override settings.
func (db *DB) SetOverrideState(state OverrideState) error {
	if state.Mode == "" {
		state.Mode = "none"
	}
	now := time.Now().UTC()
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = now
	}

	if err := db.SetSetting("override_mode", state.Mode); err != nil {
		return err
	}
	if err := db.SetSetting("override_pub_name", state.PubName); err != nil {
		return err
	}
	if err := db.SetSetting("override_pub_address", state.PubAddress); err != nil {
		return err
	}
	if err := db.SetSetting("override_arrived_at", state.ArrivedAt); err != nil {
		return err
	}
	return db.SetSetting("override_updated_at", state.UpdatedAt.Format(time.RFC3339))
}

// ClearOverrideState resets any override to normal mode.
func (db *DB) ClearOverrideState() error {
	return db.SetOverrideState(OverrideState{
		Mode:      "none",
		UpdatedAt: time.Now().UTC(),
	})
}
