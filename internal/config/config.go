package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime configuration for whereis.cam.
type Config struct {
	HassURL         string
	HassToken       string
	PersonEntity    string
	Port            string
	CacheTTL        time.Duration
	PubZones        []string
	PubIconKeywords []string
	ResendAPIKey    string
	ResendFrom      string
	SubscribersPath string
	BaseURL         string
	PushSubsPath    string
	VAPIDKeysPath   string
	MinDwellTime    time.Duration
	DBPath          string
}

// Load reads configuration from environment variables, optionally loading from
// .env or other env files first.
func Load() (*Config, error) {
	// Attempt loading .env if present (non-fatal if missing)
	_ = LoadEnvFile(".env")

	cfg := &Config{
		HassURL:         getEnv("HASS_URL", "https://fitz.itspartyti.me"),
		HassToken:       getEnv("HASS_TOKEN", ""),
		PersonEntity:    getEnv("PERSON_ENTITY", "person.cam"),
		Port:            getEnv("PORT", "8080"),
		CacheTTL:        15 * time.Second,
		PubIconKeywords: []string{"glass", "beer", "mug", "wine", "pub", "bar", "cocktail", "liquor"},
		ResendAPIKey:    getEnv("RESEND_API_KEY", ""),
		ResendFrom:      getEnv("RESEND_FROM", "Cam's Pub Radar <alerts@whereis.cam>"),
		SubscribersPath: getEnv("SUBSCRIBERS_PATH", "data/subscribers.json"),
		BaseURL:         getEnv("BASE_URL", "https://whereis.cam"),
		PushSubsPath:    getEnv("PUSH_SUBS_PATH", "data/push_subscriptions.json"),
		VAPIDKeysPath:   getEnv("VAPID_KEYS_PATH", "data/vapid_keys.json"),
		MinDwellTime:    3 * time.Minute,
		DBPath:          getEnv("DB_PATH", "data/whereiscam.db"),
	}

	if ttlStr := os.Getenv("CACHE_TTL_SECONDS"); ttlStr != "" {
		if ttlSec, err := strconv.Atoi(strings.TrimSpace(ttlStr)); err == nil && ttlSec >= 0 {
			cfg.CacheTTL = time.Duration(ttlSec) * time.Second
		}
	}

	if zonesStr := os.Getenv("PUB_ZONES"); zonesStr != "" {
		cfg.PubZones = splitAndTrim(zonesStr, ",")
	}

	if keywordsStr := os.Getenv("PUB_ICON_KEYWORDS"); keywordsStr != "" {
		cfg.PubIconKeywords = splitAndTrim(keywordsStr, ",")
	}

	// Clean trailing slash from HassURL
	cfg.HassURL = strings.TrimRight(cfg.HassURL, "/")

	if cfg.HassToken == "" {
		return nil, fmt.Errorf("HASS_TOKEN is required. Set it in .env or via environment variables")
	}

	return cfg, nil
}

// LoadEnvFile parses a simple KEY=VALUE formatted file and sets env variables
// if they are not already set in the current process environment.
func LoadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		// Strip optional outer matching quotes
		if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) ||
			(strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			if len(val) >= 2 {
				val = val[1 : len(val)-1]
			}
		}

		// Only set if not already defined in environment
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}

	return scanner.Err()
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
