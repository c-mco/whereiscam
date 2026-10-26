package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadEnvFile(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, "test.env")

	content := `
# Comment line
HASS_URL=https://test.local/
PORT=9999
QUOTED_VAL="quoted string"
SINGLE_QUOTED='single quoted'
EMPTY_LINE=
`
	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test env file: %v", err)
	}

	// Ensure clean env for test
	os.Unsetenv("HASS_URL")
	os.Unsetenv("PORT")
	os.Unsetenv("QUOTED_VAL")
	os.Unsetenv("SINGLE_QUOTED")

	if err := LoadEnvFile(envPath); err != nil {
		t.Fatalf("LoadEnvFile returned error: %v", err)
	}

	if got := os.Getenv("PORT"); got != "9999" {
		t.Errorf("expected PORT=9999, got %q", got)
	}
	if got := os.Getenv("QUOTED_VAL"); got != "quoted string" {
		t.Errorf("expected QUOTED_VAL='quoted string', got %q", got)
	}
	if got := os.Getenv("SINGLE_QUOTED"); got != "single quoted" {
		t.Errorf("expected SINGLE_QUOTED='single quoted', got %q", got)
	}
}

func TestLoad(t *testing.T) {
	t.Setenv("HASS_TOKEN", "test-token-123")
	t.Setenv("HASS_URL", "https://fitz.example.com/")
	t.Setenv("PORT", "3000")
	t.Setenv("CACHE_TTL_SECONDS", "45")
	t.Setenv("PUB_ZONES", "zone.the_vic, zone.the_union")
	t.Setenv("ADMIN_PASSWORD", "supersecret123")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.BindAddr != "" {
		t.Errorf("expected BindAddr to default to all interfaces, got %q", cfg.BindAddr)
	}

	if cfg.AdminPassword != "supersecret123" {
		t.Errorf("expected AdminPassword 'supersecret123', got %q", cfg.AdminPassword)
	}

	if cfg.HassURL != "https://fitz.example.com" {
		t.Errorf("expected trimmed HassURL, got %q", cfg.HassURL)
	}
	if cfg.HassToken != "test-token-123" {
		t.Errorf("expected HassToken 'test-token-123', got %q", cfg.HassToken)
	}
	if cfg.Port != "3000" {
		t.Errorf("expected Port '3000', got %q", cfg.Port)
	}
	if cfg.CacheTTL != 45*time.Second {
		t.Errorf("expected CacheTTL 45s, got %v", cfg.CacheTTL)
	}
	if len(cfg.PubZones) != 2 || cfg.PubZones[0] != "zone.the_vic" || cfg.PubZones[1] != "zone.the_union" {
		t.Errorf("unexpected PubZones: %v", cfg.PubZones)
	}
}

func TestLoadMissingToken(t *testing.T) {
	os.Unsetenv("HASS_TOKEN")
	_, err := Load()
	if err == nil {
		t.Error("expected error when HASS_TOKEN is missing, got nil")
	}
}
