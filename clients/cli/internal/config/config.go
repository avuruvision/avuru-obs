// Package config holds where the CLI points and what it authenticates with.
//
// Two sources, environment first: AVURUOBS_URL / AVURUOBS_TOKEN override the
// file. That ordering is the point — CI should supply a token as a secret in the
// environment, and a developer's saved credential must never quietly win over it
// on a shared runner.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

const (
	EnvURL   = "AVURUOBS_URL"
	EnvToken = "AVURUOBS_TOKEN"
)

// Path is where `login` writes. Respects AVURUOBS_CONFIG for tests and for
// anyone keeping credentials somewhere else.
func Path() (string, error) {
	if p := os.Getenv("AVURUOBS_CONFIG"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, ".avuruobs", "config.json"), nil
}

// Load resolves the effective config. A missing file is not an error — the
// environment alone is a complete configuration.
func Load() (Config, error) {
	var c Config
	p, err := Path()
	if err == nil {
		if b, readErr := os.ReadFile(p); readErr == nil {
			if err := json.Unmarshal(b, &c); err != nil {
				return c, fmt.Errorf("reading %s: %w", p, err)
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return c, fmt.Errorf("reading %s: %w", p, readErr)
		}
	}
	if v := os.Getenv(EnvURL); v != "" {
		c.URL = v
	}
	if v := os.Getenv(EnvToken); v != "" {
		c.Token = v
	}
	c.URL = strings.TrimRight(c.URL, "/")
	return c, nil
}

// Save writes the credential file with owner-only permissions, creating its
// directory. The mode is set explicitly on the file too: an existing file keeps
// its old mode through a write, and a token is not something to leave readable
// because it was once created carelessly.
func Save(c Config) (string, error) {
	p, err := Path()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", filepath.Dir(p), err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(p, append(b, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("writing %s: %w", p, err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		return "", fmt.Errorf("securing %s: %w", p, err)
	}
	return p, nil
}

// Validate reports the specific thing that is missing, because "unauthorized"
// is a bad way to learn you never ran login.
func (c Config) Validate() error {
	switch {
	case c.URL == "":
		return fmt.Errorf("no hub URL configured — run `avuruobs login --url … --token …` or set %s", EnvURL)
	case c.Token == "":
		return fmt.Errorf("no API token configured — create one in Settings → Access, then run `avuruobs login` or set %s", EnvToken)
	}
	return nil
}
