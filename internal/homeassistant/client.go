package homeassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Client interacts with the Home Assistant REST API with built-in caching.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	cacheTTL   time.Duration

	mu          sync.RWMutex
	cachedState []EntityState
	cachedAt    time.Time
}

// NewClient creates a new Home Assistant API client.
func NewClient(baseURL, token string, cacheTTL time.Duration) *Client {
	return &Client{
		baseURL:  baseURL,
		token:    token,
		cacheTTL: cacheTTL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// FetchAllStates returns all entity states from Home Assistant, serving from
// in-memory cache if the cached response is still fresh.
func (c *Client) FetchAllStates(ctx context.Context, forceRefresh bool) ([]EntityState, error) {
	if !forceRefresh && c.cacheTTL > 0 {
		c.mu.RLock()
		if len(c.cachedState) > 0 && time.Since(c.cachedAt) < c.cacheTTL {
			cached := make([]EntityState, len(c.cachedState))
			copy(cached, c.cachedState)
			c.mu.RUnlock()
			return cached, nil
		}
		c.mu.RUnlock()
	}

	reqURL := fmt.Sprintf("%s/api/states", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("home assistant request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("home assistant returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var states []EntityState
	if err := json.NewDecoder(resp.Body).Decode(&states); err != nil {
		return nil, fmt.Errorf("failed to decode home assistant response: %w", err)
	}

	if c.cacheTTL > 0 {
		c.mu.Lock()
		c.cachedState = states
		c.cachedAt = time.Now()
		c.mu.Unlock()
	}

	return states, nil
}

// CheckConnectivity verifies if Home Assistant is reachable.
func (c *Client) CheckConnectivity(ctx context.Context) error {
	reqURL := fmt.Sprintf("%s/api/", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("home assistant ping returned HTTP %d", resp.StatusCode)
	}
	return nil
}
