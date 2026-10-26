package alerts

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/SherClockHolmes/webpush-go"
	"whereis.cam/internal/storage"
)

// PushPayload is the JSON structure sent to the browser service worker.
type PushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Icon  string `json:"icon"`
	Badge string `json:"badge"`
	URL   string `json:"url"`
}

// PushService manages Web Push subscriptions and dispatch backed by SQLite.
type PushService struct {
	mu            sync.RWMutex
	db            *storage.DB
	vapidPub      string
	vapidPriv     string
	contactEmail  string
	lastAlertPub  string
	lastAlertTime time.Time
}

// NewPushService initializes Web Push with persistent SQLite storage and VAPID keys.
func NewPushService(db *storage.DB, contactEmail string) (*PushService, error) {
	if db == nil {
		return nil, fmt.Errorf("storage database is required")
	}

	if contactEmail == "" {
		contactEmail = "admin@whereis.cam"
	}

	ps := &PushService{
		db:           db,
		contactEmail: contactEmail,
	}

	if err := ps.loadOrGenerateVAPID(); err != nil {
		return nil, fmt.Errorf("failed to init VAPID keys: %w", err)
	}

	return ps, nil
}

func (ps *PushService) loadOrGenerateVAPID() error {
	pub, _ := ps.db.GetSetting("vapid_public_key")
	priv, _ := ps.db.GetSetting("vapid_private_key")

	if pub != "" && priv != "" {
		ps.vapidPub = pub
		ps.vapidPriv = priv
		return nil
	}

	// Generate new VAPID keys
	generatedPriv, generatedPub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return fmt.Errorf("webpush keygen error: %w", err)
	}

	if err := ps.db.SetSetting("vapid_public_key", generatedPub); err != nil {
		return err
	}
	if err := ps.db.SetSetting("vapid_private_key", generatedPriv); err != nil {
		return err
	}

	ps.vapidPub = generatedPub
	ps.vapidPriv = generatedPriv
	return nil
}

// VAPIDPublicKey returns the public key required for the browser push subscription.
func (ps *PushService) VAPIDPublicKey() string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.vapidPub
}

// pushServiceHosts are the browser vendors' Web Push gateways. Endpoints are
// supplied by clients, so anything else is refused to avoid SSRF.
var pushServiceHosts = []string{
	"fcm.googleapis.com",
	"android.googleapis.com",
	"updates.push.services.mozilla.com",
	"push.services.mozilla.com",
	".push.apple.com",
	".notify.windows.com",
}

// ValidatePushEndpoint checks that endpoint is an https URL on a known push gateway.
func ValidatePushEndpoint(endpoint string) error {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Port() != "" || u.User != nil {
		return fmt.Errorf("invalid push endpoint")
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range pushServiceHosts {
		if strings.HasPrefix(allowed, ".") {
			if strings.HasSuffix(host, allowed) {
				return nil
			}
		} else if host == allowed {
			return nil
		}
	}
	return fmt.Errorf("push endpoint host %q is not a recognised push service", host)
}

// Subscribe stores or updates a push subscription in SQLite.
func (ps *PushService) Subscribe(endpoint, p256dh, auth string) error {
	if err := ValidatePushEndpoint(endpoint); err != nil {
		return err
	}
	return ps.db.SavePushSubscription(endpoint, p256dh, auth)
}

// Unsubscribe deactivates a push subscription in SQLite.
func (ps *PushService) Unsubscribe(endpoint string) error {
	return ps.db.DeactivatePushSubscription(endpoint)
}

// ActiveSubscriptions returns all currently active push subscriptions.
func (ps *PushService) ActiveSubscriptions() []storage.PushSubscription {
	subs, err := ps.db.ActivePushSubscriptions()
	if err != nil {
		log.Printf("[PUSH] Error fetching push subscriptions: %v", err)
		return nil
	}
	return subs
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

// SendDirect sends a targeted push notification directly to an individual subscription.
func (ps *PushService) SendDirect(endpoint, p256dh, auth string, payload PushPayload) error {
	if err := ValidatePushEndpoint(endpoint); err != nil {
		return err
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
		return fmt.Errorf("failed to marshal push payload: %w", err)
	}

	ps.mu.RLock()
	pubKey := ps.vapidPub
	privKey := ps.vapidPriv
	email := ps.contactEmail
	ps.mu.RUnlock()

	wpSub := &webpush.Subscription{
		Endpoint: endpoint,
		Keys: webpush.Keys{
			P256dh: p256dh,
			Auth:   auth,
		},
	}

	resp, err := webpush.SendNotification(payloadBytes, wpSub, &webpush.Options{
		Subscriber:      "mailto:" + email,
		VAPIDPublicKey:  pubKey,
		VAPIDPrivateKey: privKey,
		TTL:             120,
	})
	if err != nil {
		return fmt.Errorf("push dispatch error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("push gateway rejected with HTTP %d", resp.StatusCode)
	}

	return nil
}
