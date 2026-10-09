package alerts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"whereis.cam/internal/storage"
)

// Service coordinates subscriber email alerts via the Resend API backed by SQLite.
type Service struct {
	mu              sync.Mutex
	db              *storage.DB
	resendAPIKey    string
	fromEmail       string
	baseURL         string
	cooldownMinutes int
	lastAlertPub    string
	lastAlertTime   time.Time
	httpClient      *http.Client
}

// NewService initializes the alerts service with SQLite database and Resend API.
func NewService(db *storage.DB, resendAPIKey, fromEmail, baseURL string) (*Service, error) {
	if db == nil {
		return nil, fmt.Errorf("storage database is required")
	}

	if fromEmail == "" {
		fromEmail = "Cam's Pub Radar <alerts@whereis.cam>"
	}
	if baseURL == "" {
		baseURL = "https://whereis.cam"
	}

	s := &Service{
		db:              db,
		resendAPIKey:    resendAPIKey,
		fromEmail:       fromEmail,
		baseURL:         baseURL,
		cooldownMinutes: 180, // 3 hours anti-spam debounce
		httpClient:      &http.Client{Timeout: 10 * time.Second},
	}

	return s, nil
}

// Subscribe registers an email address in the database.
func (s *Service) Subscribe(email string) (storage.Subscriber, bool, error) {
	return s.db.SubscribeEmail(email)
}

// Unsubscribe deactivates a subscription using their token.
func (s *Service) Unsubscribe(token string) bool {
	return s.db.UnsubscribeEmail(token)
}

// ActiveSubscribers returns all active email subscribers from SQLite.

// CountActive returns the number of active email subscribers.
func (s *Service) CountActive() int {
	return len(s.ActiveSubscribers())
}

func (s *Service) ActiveSubscribers() []storage.Subscriber {
	subs, err := s.db.ActiveSubscribers()
	if err != nil {
		log.Printf("[ALERTS] Error fetching subscribers: %v", err)
		return nil
	}
	return subs
}

// resendEmailRequest represents the JSON payload expected by Resend's REST API.
type resendEmailRequest struct {
	From    string            `json:"from"`
	To      []string          `json:"to"`
	Subject string            `json:"subject"`
	HTML    string            `json:"html"`
	Headers map[string]string `json:"headers,omitempty"`
}

// SendEmail dispatches an HTML email via the Resend API.
func (s *Service) SendEmail(to, subject, htmlBody string) error {
	if s.resendAPIKey == "" {
		log.Printf("[ALERTS] RESEND_API_KEY not configured. Simulated dispatch to %s: %s", to, subject)
		return nil
	}

	payload := resendEmailRequest{
		From:    s.fromEmail,
		To:      []string{to},
		Subject: subject,
		HTML:    htmlBody,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+s.resendAPIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "whereis.cam/1.0")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("resend API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// SendWelcomeEmail dispatches an immediate welcome notification with a 1-click unsubscribe link.
func (s *Service) SendWelcomeEmail(sub storage.Subscriber) {
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
    <p class="desc">The moment Cam drops in for a pint at any pub, you'll be the first to know so you can pull up a stool and say g'day.</p>
    <div class="footer">
      <p>Sent from whereis.cam &bull; <a href="%s">Unsubscribe anytime</a></p>
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
func (s *Service) SendPubAlert(pubName, arrivedAtTime, mapURL string) {
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
    <div class="badge">CAM IS AT THE PUB</div>
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
</html>`, escapedPub, html.EscapeString(arrivedAtTime), mapURL, escapedPub, unsubURL)

			if err := s.SendEmail(subCopy.Email, subject, htmlBody); err != nil {
				log.Printf("[ALERTS] Error sending alert to %s: %v", subCopy.Email, err)
			}
		}()
	}
}
