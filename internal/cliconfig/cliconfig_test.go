package cliconfig

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func setup(t *testing.T, keychain map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PANTECH_CONFIG_DIR", dir)
	t.Setenv("PANTECH_PROFILE", "")
	t.Setenv("PANTECH_NO_KEYRING", "")
	old := keyringGet
	keyringGet = func(service, user string) (string, error) {
		if service != keyringService {
			t.Errorf("keychain service %q", service)
		}
		if k, ok := keychain[user]; ok {
			return k, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { keyringGet = old })
	return dir
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestKeychainThenFile(t *testing.T) {
	dir := setup(t, map[string]string{"work": "PAN_from_keychain"})
	write(t, filepath.Join(dir, "config.json"), `{"current":"work","profiles":{"work":{},"home":{"api_url":"https://api.test.example/public/v1"}}}`)
	write(t, filepath.Join(dir, "credentials.json"), `{"home":"PAN_from_file","work":"PAN_stale"}`)

	c, err := Load()
	if err != nil || c.Profile != "work" || c.Key != "PAN_from_keychain" {
		t.Fatalf("current profile: %+v %v", c, err)
	}
	t.Setenv("PANTECH_PROFILE", "home")
	c, err = Load()
	if err != nil || c.Key != "PAN_from_file" || c.APIURL != "https://api.test.example/public/v1" {
		t.Fatalf("PANTECH_PROFILE: %+v %v", c, err)
	}
	t.Setenv("PANTECH_PROFILE", "work")
	t.Setenv("PANTECH_NO_KEYRING", "1")
	if c, _ := Load(); c.Key != "PAN_stale" {
		t.Fatalf("PANTECH_NO_KEYRING should skip the keychain: %+v", c)
	}
}

func TestNothingStored(t *testing.T) {
	setup(t, nil)
	c, err := Load()
	if err != nil || c.Key != "" || c.Profile != "default" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestDirOrder(t *testing.T) {
	t.Setenv("PANTECH_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if d, _ := Dir(); d != "/xdg/pantech" {
		t.Fatal(d)
	}
	t.Setenv("PANTECH_CONFIG_DIR", "/explicit")
	if d, _ := Dir(); d != "/explicit" {
		t.Fatal(d)
	}
}

func TestRefusesFilesOthersCanWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix permissions")
	}
	dir := setup(t, nil)
	t.Setenv("PANTECH_NO_KEYRING", "1")
	for _, name := range []string{"config.json", "credentials.json"} {
		p := filepath.Join(dir, name)
		write(t, filepath.Join(dir, "config.json"), `{"profiles":{}}`)
		write(t, filepath.Join(dir, "credentials.json"), `{"default":"PAN_x"}`)
		if err := os.Chmod(p, 0o666); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("%s writable by others: %v", name, err)
		}
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
		if c, err := Load(); err != nil || c.Key != "PAN_x" {
			t.Errorf("%s readable by others but private to write: %v", name, err)
		}
	}
}
