package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "gsmd.conf")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_Happy(t *testing.T) {
	p := writeTemp(t, `{
		"cpe_host":"192.168.2.4",
		"cpe_user":"admin",
		"cpe_pass":"secret",
		"listen":":8080",
		"rate_limit_per_min":60,
		"rate_limit_burst":10,
		"tokens":{"ha":"abc","grafana":"def"}
	}`)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if c.CPEHost != "192.168.2.4" || c.CPEUser != "admin" || c.CPEPass != "secret" {
		t.Errorf("CPE fields wrong: %+v", c)
	}
	if c.Listen != ":8080" {
		t.Errorf("Listen wrong: %q", c.Listen)
	}
	if c.RateLimitPerMin != 60 || c.RateLimitBurst != 10 {
		t.Errorf("rate fields wrong: per_min=%d burst=%d", c.RateLimitPerMin, c.RateLimitBurst)
	}
	if len(c.Tokens) != 2 || c.Tokens["ha"] != "abc" || c.Tokens["grafana"] != "def" {
		t.Errorf("tokens wrong: %+v", c.Tokens)
	}
}

func TestLoad_DefaultsRateLimit(t *testing.T) {
	p := writeTemp(t, `{
		"cpe_host":"x","cpe_user":"y","cpe_pass":"z","listen":":8080",
		"tokens":{"a":"b"}
	}`)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if c.RateLimitPerMin != 30 {
		t.Errorf("expected default rate_limit_per_min=30, got %d", c.RateLimitPerMin)
	}
	if c.RateLimitBurst != 5 {
		t.Errorf("expected default rate_limit_burst=5, got %d", c.RateLimitBurst)
	}
}

func TestLoad_IgnoresUnknownKeys(t *testing.T) {
	p := writeTemp(t, `{
		"cpe_host":"x","cpe_user":"y","cpe_pass":"z","listen":":8080",
		"tokens":{"a":"b"},
		"future_field":"whatever"
	}`)
	if _, err := Load(p); err != nil {
		t.Errorf("expected to ignore unknown key, got error: %v", err)
	}
}

func TestLoad_ValidationFailures(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantSub string
	}{
		{"empty_tokens", `{"cpe_host":"x","cpe_user":"y","cpe_pass":"z","listen":":8080","tokens":{}}`, "at least one token"},
		{"empty_cpe_host", `{"cpe_user":"y","cpe_pass":"z","listen":":8080","tokens":{"a":"b"}}`, "cpe_host"},
		{"empty_cpe_user", `{"cpe_host":"x","cpe_pass":"z","listen":":8080","tokens":{"a":"b"}}`, "cpe_user"},
		{"empty_cpe_pass", `{"cpe_host":"x","cpe_user":"y","listen":":8080","tokens":{"a":"b"}}`, "cpe_pass"},
		{"empty_listen", `{"cpe_host":"x","cpe_user":"y","cpe_pass":"z","tokens":{"a":"b"}}`, "listen"},
		{"empty_token_value", `{"cpe_host":"x","cpe_user":"y","cpe_pass":"z","listen":":8080","tokens":{"a":""}}`, "empty value"},
		{"negative_rate", `{"cpe_host":"x","cpe_user":"y","cpe_pass":"z","listen":":8080","rate_limit_per_min":-1,"tokens":{"a":"b"}}`, "rate_limit_per_min"},
		{"negative_burst", `{"cpe_host":"x","cpe_user":"y","cpe_pass":"z","listen":":8080","rate_limit_burst":-1,"tokens":{"a":"b"}}`, "rate_limit_burst"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTemp(t, tc.content)
			_, err := Load(p)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("expected error to contain %q, got: %v", tc.wantSub, err)
			}
		})
	}
}

func TestLoad_FileMissing(t *testing.T) {
	if _, err := Load("/nonexistent/path/that/does/not/exist.conf"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_BadJSON(t *testing.T) {
	p := writeTemp(t, `{not valid json`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected parse error")
	}
}
