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

	_ "modernc.org/sqlite"
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

	conn, err := sql.Open("sqlite", dbPath)
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
	`
	_, err := db.conn.Exec(schema)
	return err
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
	row := db.conn.QueryRow("SELECT id, email, token, created_at, active, unsubscribed_at FROM subscribers WHERE email = ?", email)
	err := row.Scan(&sub.ID, &sub.Email, &sub.Token, &sub.CreatedAt, &sub.Active, &unsubscribedAt)

	if err == sql.ErrNoRows {
		// New subscriber
		token := generateToken()
		now := time.Now()
		res, err := db.conn.Exec(
			"INSERT INTO subscribers (email, token, created_at, active) VALUES (?, ?, ?, 1)",
			email, token, now,
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
	rows, err := db.conn.Query("SELECT id, email, token, created_at, active FROM subscribers WHERE active = 1 ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Subscriber
	for rows.Next() {
		var s Subscriber
		if err := rows.Scan(&s.ID, &s.Email, &s.Token, &s.CreatedAt, &s.Active); err != nil {
			continue
		}
		list = append(list, s)
	}
	return list, nil
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
							s.Email, s.Token, s.CreatedAt, s.Active,
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
