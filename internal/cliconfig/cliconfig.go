// Package cliconfig reads the API key the Pantech CLI stored, exactly as the
// CLI finds it (pantech-cli/internal/config), so `pantech-mcp stdio` works
// after `pantech auth login`. It only reads: it never writes the config, the
// credentials file or the keychain.
package cliconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/zalando/go-keyring"
)

// The CLI's keychain service; items are keyed by profile name.
const keyringService = "pantech-cli"

const defaultProfile = "default"

type profile struct {
	// APIURL is set by old CLI versions that signed in elsewhere than
	// production; such a key works only on that API.
	APIURL string `json:"api_url,omitempty"`
}

type config struct {
	Current  string             `json:"current,omitempty"`
	Profiles map[string]profile `json:"profiles"`
}

// Dir is $PANTECH_CONFIG_DIR, else $XDG_CONFIG_HOME/pantech, else ~/.config/pantech.
func Dir() (string, error) {
	if d := os.Getenv("PANTECH_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "pantech"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "pantech"), nil
}

// Credentials is a stored key and the API it belongs to ("" = production).
type Credentials struct {
	Profile string
	Key     string
	APIURL  string
}

// keyringGet is replaced in tests.
var keyringGet = keyring.Get

// Load returns the key of the profile in effect: $PANTECH_PROFILE, else the
// config's current profile, else "default". Key is "" when nothing is stored.
func Load() (*Credentials, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	cfg := config{Profiles: map[string]profile{}}
	p := filepath.Join(dir, "config.json")
	data, err := readPrivate(p)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("%s is not valid JSON: %w", p, err)
		}
	}
	name := os.Getenv("PANTECH_PROFILE")
	if name == "" {
		name = cfg.Current
	}
	if name == "" {
		name = defaultProfile
	}
	creds := &Credentials{Profile: name, APIURL: cfg.Profiles[name].APIURL}

	// The keychain first (unless PANTECH_NO_KEYRING, as the CLI does), then
	// credentials.json beside the config.
	if os.Getenv("PANTECH_NO_KEYRING") == "" {
		if key, err := keyringGet(keyringService, name); err == nil && key != "" {
			creds.Key = key
			return creds, nil
		}
	}
	data, err = readPrivate(filepath.Join(dir, "credentials.json"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return creds, nil
	case err != nil:
		return nil, err
	}
	stored := map[string]string{}
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON", filepath.Join(dir, "credentials.json"))
	}
	creds.Key = stored[name]
	return creds, nil
}

// readPrivate reads a file of the CLI's that decides which key is used and
// where it is sent (config.json's api_url, credentials.json's key). It
// refuses one that other users can write, as the CLI never makes one: they
// could point the key at an API of their own or swap in theirs.
func readPrivate(path string) ([]byte, error) {
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.Mode().Perm()&0o022 != 0 {
			return nil, fmt.Errorf("%s can be changed by other users of this computer: run chmod 600 %s, then try again", path, path)
		}
	}
	return os.ReadFile(path)
}
