package alerts

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/SherClockHolmes/webpush-go"
)

// PushSubscription represents a browser push notification registration.
type PushSubscription struct {
	Endpoint  string    `json:"endpoint"`
	P256DH    string    `json:"p256dh"`
	Auth      string    `json:"auth"`
	CreatedAt time.Time `json:"created_at"`
	Active    bool      `json:"active"`
}

// PushPayload is the JSON structure sent to the browser service worker.
type PushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Icon  string `json:"icon"`
	Badge string `json:"badge"`
	URL   string `json:"url"`
}

// VAPIDKeys stores the permanent application server keys for Web Push.
type VAPIDKeys struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

// PushService manages Web Push subscriptions and dispatch.
type PushService struct {
	mu           sync.RWMutex
	filePath     string
	keysFilePath string
	subs         map[string]*PushSubscription // keyed by endpoint
	vapidPub     string
	vapidPriv    string
	contactEmail string
	lastAlertPub string
	lastAlertTime time.Time
}

// NewPushService initializes Web Push with persistent subscriptions and VAPID keys.
func NewPushService(filePath, keysFilePath, contactEmail string) (*PushService, error) {
	if contactEmail == "" {
		contactEmail = "admin@whereis.cam"
	}

	ps := &PushService{
		filePath:     filePath,
		keysFilePath: keysFilePath,
		subs:         make(map[string]*PushSubscription),
		contactEmail: contactEmail,
	}

	if err := ps.loadOrGenerateVAPID(); err != nil {
		return nil, fmt.Errorf("failed to init VAPID keys: %w", err)
	}

	if err := ps.load(); err != nil {
		return nil, fmt.Errorf("failed to load push subscriptions: %w", err)
	}

	return ps, nil
}

func (ps *PushService) loadOrGenerateVAPID() error {
	dir := filepath.Dir(ps.keysFilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	if data, err := os.ReadFile(ps.keysFilePath); err == nil && len(data) > 0 {
		var keys VAPIDKeys
		if err := json.Unmarshal(data, &keys); err == nil && keys.PublicKey != "" && keys.PrivateKey != "" {
			ps.vapidPub = keys.PublicKey
			ps.vapidPriv = keys.PrivateKey
			return nil
		}
	}

	// Generate new VAPID keys
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return fmt.Errorf("webpush keygen error: %w", err)
	}

	keys := VAPIDKeys{
		PublicKey:  pub,
		PrivateKey: priv,
	}
	data, _ := json.MarshalIndent(keys, "", "  ")
	_ = os.WriteFile(ps.keysFilePath, data, 0600)

	ps.vapidPub = pub
	ps.vapidPriv = priv
	return nil
}

// VAPIDPublicKey returns the public key required for the browser push subscription.
func (ps *PushService) VAPIDPublicKey() string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.vapidPub
}

func (ps *PushService) load() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if _, err := os.Stat(ps.filePath); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(ps.filePath)
	if err != nil {
		return err
	}

	var list []PushSubscription
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}

	for i := range list {
		s := list[i]
		ps.subs[s.Endpoint] = &s
	}
	return nil
}

func (ps *PushService) save() error {
	dir := filepath.Dir(ps.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	list := make([]PushSubscription, 0, len(ps.subs))
	for _, s := range ps.subs {
		list = append(list, *s)
	}

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(ps.filePath, data, 0644)
}

// Subscribe stores or updates a push subscription.
func (ps *PushService) Subscribe(sub PushSubscription) error {
	if sub.Endpoint == "" || sub.P256DH == "" || sub.Auth == "" {
		return fmt.Errorf("invalid subscription fields")
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	sub.CreatedAt = time.Now()
	sub.Active = true
	ps.subs[sub.Endpoint] = &sub

	return ps.save()
}

// Unsubscribe deactivates a push subscription.
func (ps *PushService) Unsubscribe(endpoint string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if sub, exists := ps.subs[endpoint]; exists {
		sub.Active = false
		return ps.save()
	}
	return nil
}

// ActiveSubscriptions returns all currently active push subscriptions.
func (ps *PushService) ActiveSubscriptions() []PushSubscription {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var result []PushSubscription
	for _, sub := range ps.subs {
		if sub.Active {
			result = append(result, *sub)
		}
	}
	return result
}

// Broadcast sends a push notification to all active browser subscriptions.
func (ps *PushService) Broadcast(payload PushPayload) {
	subs := ps.ActiveSubscriptions()
	if len(subs) == 0 {
		return
	}

	if payload.Icon == "" {
		payload.Icon = "/static/favicon.svg"
	}
	if payload.Badge == "" {
		payload.Badge = "/static/favicon.svg"
	}
	if payload.URL == "" {
		payload.URL = "/"
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[PUSH] Payload marshal error: %v", err)
		return
	}

	ps.mu.RLock()
	pubKey := ps.vapidPub
	privKey := ps.vapidPriv
	email := ps.contactEmail
	ps.mu.RUnlock()

	log.Printf("[PUSH] Broadcasting notification '%s' to %d endpoints", payload.Title, len(subs))

	for _, sub := range subs {
		subCopy := sub
		go func() {
			wpSub := &webpush.Subscription{
				Endpoint: subCopy.Endpoint,
				Keys: webpush.Keys{
					P256dh: subCopy.P256DH,
					Auth:   subCopy.Auth,
				},
			}

			resp, err := webpush.SendNotification(payloadBytes, wpSub, &webpush.Options{
				Subscriber:      "mailto:" + email,
				VAPIDPublicKey:  pubKey,
				VAPIDPrivateKey: privKey,
				TTL:             120, // 2 minutes
			})

			if err != nil {
				log.Printf("[PUSH] Error sending to endpoint: %v", err)
				return
			}
			defer resp.Body.Close()

			// 404 or 410 Gone means the user revoked permissions or closed subscription
			if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
				log.Printf("[PUSH] Expired subscription (HTTP %d). Removing.", resp.StatusCode)
				_ = ps.Unsubscribe(subCopy.Endpoint)
			}
		}()
	}
}

// BroadcastPubAlert checks debounce cooldown and broadcasts pub alert.
func (ps *PushService) BroadcastPubAlert(pubName, arrivedAt, url string) {
	ps.mu.Lock()
	now := time.Now()
	if ps.lastAlertPub == pubName && now.Sub(ps.lastAlertTime) < 180*time.Minute {
		ps.mu.Unlock()
		log.Printf("[PUSH] Skipping push alert for %s (cooldown active: last sent %v ago)", pubName, now.Sub(ps.lastAlertTime))
		return
	}
	ps.lastAlertPub = pubName
	ps.lastAlertTime = now
	ps.mu.Unlock()

	ps.Broadcast(PushPayload{
		Title: fmt.Sprintf("★ Cam is at %s!", pubName),
		Body:  fmt.Sprintf("First pot poured at %s. Tap to join him!", arrivedAt),
		URL:   url,
	})
}
