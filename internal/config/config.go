// Package config loads and validates gsmd's runtime configuration.
//
// The on-disk format is a JSON file at /etc/gsmd.conf. See gsmd.conf.example
// for the schema.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

const (
	defaultRatePerMin = 30
	defaultRateBurst  = 5
)

// Config is the parsed contents of the config file.
type Config struct {
	CPEHost         string            `json:"cpe_host"`
	CPEUser         string            `json:"cpe_user"`
	CPEPass         string            `json:"cpe_pass"`
	Listen          string            `json:"listen"`
	RateLimitPerMin int               `json:"rate_limit_per_min"`
	RateLimitBurst  int               `json:"rate_limit_burst"`
	Tokens          map[string]string `json:"tokens"`
}

// Load reads, parses and validates the config file at path.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	var c Config
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	if c.CPEHost == "" {
		return errors.New("cpe_host is required")
	}
	if c.CPEUser == "" {
		return errors.New("cpe_user is required")
	}
	if c.CPEPass == "" {
		return errors.New("cpe_pass is required")
	}
	if c.Listen == "" {
		return errors.New("listen is required")
	}
	if c.RateLimitPerMin < 0 {
		return errors.New("rate_limit_per_min must be > 0")
	}
	if c.RateLimitBurst < 0 {
		return errors.New("rate_limit_burst must be > 0")
	}
	if c.RateLimitPerMin == 0 {
		c.RateLimitPerMin = defaultRatePerMin
	}
	if c.RateLimitBurst == 0 {
		c.RateLimitBurst = defaultRateBurst
	}
	if len(c.Tokens) == 0 {
		return errors.New("at least one token is required")
	}
	for name, tok := range c.Tokens {
		if name == "" {
			return errors.New("token name cannot be empty")
		}
		if tok == "" {
			return fmt.Errorf("token %q has empty value", name)
		}
	}
	return nil
}
