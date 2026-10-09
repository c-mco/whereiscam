package alerts

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Subscriber represents a user signed up for pub alerts.
type Subscriber struct {
	Email     string    `json:"email"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
	Active    bool      `json:"active"`
}

// Service manages subscriber persistence and email dispatch via Resend.
type Service struct {
	mu              sync.RWMutex
	filePath        string
	subscribers     map[string]*Subscriber // keyed by normalized email
	apiKey          string
	fromEmail       string
	baseURL         string
	httpClient      *http.Client
	lastAlertPub    string
	lastAlertTime   time.Time
	cooldownMinutes int
}

// NewService creates a new alerts service.
func NewService(filePath, apiKey, fromEmail, baseURL string) (*Service, error) {
	if fromEmail == "" {
		fromEmail = "Cam's Pub Radar <alerts@whereis.cam>"
	}
	if baseURL == "" {
		baseURL = "https://whereis.cam"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	s := &Service{
		filePath:        filePath,
		subscribers:     make(map[string]*Subscriber),
		apiKey:          apiKey,
		fromEmail:       fromEmail,
		baseURL:         baseURL,
		httpClient:      &http.Client{Timeout: 10 * time.Second},
		cooldownMinutes: 180, // 3 hours cooldown between alerts for same pub
	}

	if err := s.load(); err != nil {
		return nil, fmt.Errorf("failed to load subscribers: %w", err)
	}

	return s, nil
}

func (s *Service) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(s.filePath); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}

	if len(data) == 0 {
		return nil
	}

	var list []Subscriber
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}

	for i := range list {
		sub := list[i]
		s.subscribers[strings.ToLower(sub.Email)] = &sub
	}
	return nil
}

func (s *Service) save() error {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	list := make([]Subscriber, 0, len(s.subscribers))
	for _, sub := range s.subscribers {
		list = append(list, *sub)
	}

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.filePath, data, 0644)
}

// NormalizeEmail checks and normalizes an email address.
func NormalizeEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return "", fmt.Errorf("invalid email address format")
	}
	norm := strings.ToLower(addr.Address)
	if !strings.Contains(norm, ".") {
		return "", fmt.Errorf("invalid email domain")
	}
	return norm, nil
}

func generateToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Subscribe adds or re-activates an email address.
// Returns (sub, isNew, error).
func (s *Service) Subscribe(rawEmail string) (*Subscriber, bool, error) {
	normEmail, err := NormalizeEmail(rawEmail)
	if err != nil {
		return nil, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, found := s.subscribers[normEmail]; found {
		if !existing.Active {
			existing.Active = true
			if err := s.save(); err != nil {
				return nil, false, err
			}
			return existing, false, nil
		}
		return existing, false, nil
	}

	sub := &Subscriber{
		Email:     normEmail,
		Token:     generateToken(),
		CreatedAt: time.Now(),
		Active:    true,
	}
	s.subscribers[normEmail] = sub

	if err := s.save(); err != nil {
		delete(s.subscribers, normEmail)
		return nil, false, err
	}

	return sub, true, nil
}

// Unsubscribe deactivates a subscriber by their secure token.
func (s *Service) Unsubscribe(token string) bool {
	if token == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, sub := range s.subscribers {
		if sub.Token == token {
			if sub.Active {
				sub.Active = false
				_ = s.save()
				return true
			}
			return true // already inactive
		}
	}
	return false
}

// ActiveSubscribers returns all currently subscribed users.
func (s *Service) ActiveSubscribers() []Subscriber {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Subscriber
	for _, sub := range s.subscribers {
		if sub.Active {
			result = append(result, *sub)
		}
	}
	return result
}

// CountActive returns the number of active subscribers.
func (s *Service) CountActive() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, sub := range s.subscribers {
		if sub.Active {
			count++
		}
	}
	return count
}

// SendEmail dispatches an email via the Resend API.
func (s *Service) SendEmail(to, subject, htmlBody string) error {
	if s.apiKey == "" {
		log.Printf("[ALERTS] RESEND_API_KEY not set. Simulated sending email to %s: %s", to, subject)
		return nil
	}

	payload := map[string]interface{}{
		"from":    s.fromEmail,
		"to":      []string{to},
		"subject": subject,
		"html":    htmlBody,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, "https://api.resend.com/emails", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "whereis.cam/1.0")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("resend API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// SendWelcomeEmail sends a confirmation email to a newly subscribed user.
func (s *Service) SendWelcomeEmail(sub *Subscriber) {
	if sub == nil || !sub.Active {
		return
	}

	unsubURL := fmt.Sprintf("%s/unsubscribe?token=%s", s.baseURL, sub.Token)
	subject := "★ You're on Cam's Pub Radar!"

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; background: #faf4eb; color: #231309; padding: 24px; margin: 0; }
  .card { max-width: 480px; margin: 0 auto; background: #faf4eb; border: 4px solid #231309; border-radius: 18px; padding: 28px 24px; text-align: center; box-shadow: 0 10px 24px rgba(35,19,9,0.15); }
  .crest { font-size: 13px; font-weight: bold; letter-spacing: 0.15em; text-transform: uppercase; color: #543420; margin-bottom: 16px; }
  .title { font-family: Georgia, serif; font-size: 26px; color: #231309; margin: 0 0 14px; font-weight: bold; }
  .desc { font-size: 15px; line-height: 1.5; color: #3c2214; margin-bottom: 22px; }
  .badge { display: inline-block; background: #e5a823; color: #231309; font-weight: bold; padding: 8px 16px; border-radius: 20px; font-size: 13px; border: 2px solid #231309; margin-bottom: 20px; }
  .footer { margin-top: 24px; font-size: 12px; color: #754c30; }
  .footer a { color: #ba3f0c; text-decoration: underline; }
</style>
</head>
<body>
  <div class="card">
    <div class="crest">★ whereis.cam ★</div>
    <div class="badge">RADAR ARMED & READY</div>
    <div class="title">You're on the list!</div>
    <p class="desc">The moment Cam drops in for a pint at any Brunswick pub, you'll be the first to know so you can pull up a stool and say g'day.</p>
    <div class="footer">
      <p>Sent from Brunswick Pub Radar &bull; <a href="%s">Unsubscribe anytime</a></p>
    </div>
  </div>
</body>
</html>`, unsubURL)

	go func() {
		if err := s.SendEmail(sub.Email, subject, htmlBody); err != nil {
			log.Printf("[ALERTS] Failed to send welcome email to %s: %v", sub.Email, err)
		} else {
			log.Printf("[ALERTS] Sent welcome email to %s", sub.Email)
		}
	}()
}

// SendPubAlert checks debounce cooldown and broadcasts alert to all active subscribers.
func (s *Service) SendPubAlert(pubName, durationStr, mapURL string) {
	s.mu.Lock()
	now := time.Now()
	// Debounce check
	if s.lastAlertPub == pubName && now.Sub(s.lastAlertTime) < time.Duration(s.cooldownMinutes)*time.Minute {
		s.mu.Unlock()
		log.Printf("[ALERTS] Skipping alert for %s (cooldown active: last sent %v ago)", pubName, now.Sub(s.lastAlertTime))
		return
	}
	s.lastAlertPub = pubName
	s.lastAlertTime = now
	s.mu.Unlock()

	subscribers := s.ActiveSubscribers()
	if len(subscribers) == 0 {
		log.Printf("[ALERTS] No active subscribers to alert for %s", pubName)
		return
	}

	log.Printf("[ALERTS] Broadcasting pub alert for '%s' to %d subscribers", pubName, len(subscribers))

	escapedPub := html.EscapeString(pubName)
	subject := fmt.Sprintf("★ Cam is at %s!", pubName)

	for _, sub := range subscribers {
		subCopy := sub
		go func() {
			unsubURL := fmt.Sprintf("%s/unsubscribe?token=%s", s.baseURL, subCopy.Token)
			htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; background: #faf4eb; color: #231309; padding: 24px; margin: 0; }
  .card { max-width: 480px; margin: 0 auto; background: #faf4eb; border: 4px solid #231309; border-radius: 18px; padding: 28px 24px; text-align: center; box-shadow: 0 10px 24px rgba(35,19,9,0.15); }
  .crest { font-size: 13px; font-weight: bold; letter-spacing: 0.15em; text-transform: uppercase; color: #543420; margin-bottom: 12px; }
  .badge { display: inline-block; background: #1c532b; color: #faf4eb; font-weight: bold; padding: 6px 14px; border-radius: 20px; font-size: 12px; border: 2px solid #231309; margin-bottom: 16px; }
  .headline { font-family: Georgia, serif; font-size: 32px; color: #ba3f0c; margin: 0 0 10px; font-weight: bold; }
  .timer { font-size: 14px; color: #543420; margin-bottom: 20px; }
  .btn { display: inline-block; background: #ba3f0c; color: #faf4eb !important; font-weight: bold; text-decoration: none; padding: 14px 28px; border-radius: 28px; font-size: 16px; border: 3px solid #231309; margin-bottom: 18px; }
  .footer { margin-top: 24px; font-size: 12px; color: #754c30; border-top: 1px dashed #754c30; padding-top: 16px; }
  .footer a { color: #ba3f0c; text-decoration: underline; }
</style>
</head>
<body>
  <div class="card">
    <div class="crest">★ whereis.cam ★</div>
    <div class="badge">SALOON BAR ACTIVE &bull; CAM IS AT THE PUB</div>
    <div style="font-size: 16px; color: #3c2214; margin-bottom: 4px;">Cam is having a pint at</div>
    <div class="headline">%s</div>
    <div class="timer">First pot poured at %s</div>
    <div>
      <a href="%s" class="btn">JOIN CAM AT %s &rarr;</a>
    </div>
    <div class="footer">
      <p>Live alert from <a href="https://whereis.cam">whereis.cam</a> &bull; <a href="%s">Unsubscribe</a></p>
    </div>
  </div>
</body>
</html>`, escapedPub, html.EscapeString(durationStr), mapURL, strings.ToUpper(escapedPub), unsubURL)

			if err := s.SendEmail(subCopy.Email, subject, htmlBody); err != nil {
				log.Printf("[ALERTS] Failed sending pub alert to %s: %v", subCopy.Email, err)
			}
		}()
	}
}
