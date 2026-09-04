package creds

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// config is zlily's per-user settings file. It exists today only to remember
// who last logged in where, but it is a JSON object so later settings can join
// it without a format change.
type config struct {
	// Logins maps a Lily server address to the username last used against it.
	// A username on its own is not a secret, so this is remembered without
	// asking — it is what lets the dialog open with the cursor on the password
	// field even when no password is stored.
	Logins map[string]string `json:"logins,omitempty"`
}

func loadConfig() (config, error) {
	var cfg config
	path, err := configPath()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		// A corrupt settings file must not stop the user logging in; the next
		// successful login rewrites it.
		return config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func saveConfig(cfg config) error {
	if _, err := ensureDir(); err != nil {
		return err
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return writePrivate(path, append(data, '\n'))
}

// LastUser returns the username last used against host, or "" if there is none.
func LastUser(host string) string {
	if host == "" {
		return ""
	}
	cfg, err := loadConfig()
	if err != nil {
		return ""
	}
	for h, u := range cfg.Logins {
		if matches(h, host) {
			return u
		}
	}
	return ""
}

// RememberUser records user as the last username used against host.
func RememberUser(host, user string) error {
	if host == "" || user == "" {
		return nil
	}
	cfg, err := loadConfig()
	if err != nil {
		// Start over rather than refuse: the alternative is a permanently
		// broken prefill because of one bad byte in a convenience file.
		cfg = config{}
	}
	if cfg.Logins == nil {
		cfg.Logins = make(map[string]string)
	}
	for h := range cfg.Logins {
		if matches(h, host) && h != host {
			delete(cfg.Logins, h)
		}
	}
	if strings.TrimSpace(cfg.Logins[host]) == user {
		return nil
	}
	cfg.Logins[host] = user
	return saveConfig(cfg)
}
