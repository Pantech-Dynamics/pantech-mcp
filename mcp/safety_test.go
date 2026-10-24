package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/Pantech-Dynamics/pantech-mcp/mcp/tools"
)

func testConfirmer(t *testing.T, now *time.Time) *Confirmer {
	t.Helper()
	c, err := NewConfirmer([]byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return *now }
	return c
}

func TestConfirmationToken(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	c := testConfirmer(t, &now)
	args := []byte(`{"instance_id":"vm_1"}`)
	subject := Subject("PAN_key_one")
	token, exp := c.Issue("delete_instance", args, subject)
	if exp.Sub(now) != ConfirmTTL {
		t.Fatalf("expires after %s", exp.Sub(now))
	}
	if err := c.Verify(token, "delete_instance", args, subject); err != nil {
		t.Fatalf("a fresh token: %v", err)
	}

	cases := map[string]func() error{
		"another tool":    func() error { return c.Verify(token, "delete_volume", args, subject) },
		"other arguments": func() error { return c.Verify(token, "delete_instance", []byte(`{"instance_id":"vm_2"}`), subject) },
		"another bearer":  func() error { return c.Verify(token, "delete_instance", args, Subject("PAN_key_two")) },
		"tampered mac":    func() error { return c.Verify(token[:len(token)-2]+"AA", "delete_instance", args, subject) },
		"tampered expiry": func() error { return c.Verify(bumpExpiry(token), "delete_instance", args, subject) },
		"garbage":         func() error { return c.Verify("pct1.x.y", "delete_instance", args, subject) },
		"empty":           func() error { return c.Verify("", "delete_instance", args, subject) },
		"another secret": func() error {
			other, _ := NewConfirmer([]byte(strings.Repeat("t", 32)))
			return other.Verify(token, "delete_instance", args, subject)
		},
		"expired": func() error {
			later := now.Add(ConfirmTTL + time.Second)
			c2 := testConfirmer(t, &later)
			return c2.Verify(token, "delete_instance", args, subject)
		},
	}
	for name, verify := range cases {
		if err := verify(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// Just before expiry it still works.
	almost := now.Add(ConfirmTTL - time.Second)
	if err := testConfirmer(t, &almost).Verify(token, "delete_instance", args, subject); err != nil {
		t.Errorf("before expiry: %v", err)
	}
}

func bumpExpiry(token string) string {
	parts := strings.Split(token, ".")
	parts[1] = parts[1][:len(parts[1])-1] + "9"
	return strings.Join(parts, ".")
}

func TestShortSecretRefused(t *testing.T) {
	if _, err := NewConfirmer([]byte("short")); err == nil {
		t.Fatal("a short secret was accepted")
	}
}

func TestIdempotencyKeyIsDeterministic(t *testing.T) {
	now := time.Now()
	c := testConfirmer(t, &now)
	a, _ := c.Issue("create_instance", []byte(`{}`), "s")
	b := "pct1.1.other"
	if c.IdempotencyKey(a) != c.IdempotencyKey(a) {
		t.Fatal("the same token gave two keys")
	}
	if c.IdempotencyKey(a) == c.IdempotencyKey(b) {
		t.Fatal("two tokens gave one key")
	}
	if k := c.IdempotencyKey(a); len(k) > 255 || strings.Contains(k, a) {
		t.Fatalf("key %q", k)
	}
}

func TestCanonicalArgsIgnoreTokenAndOrder(t *testing.T) {
	type in struct {
		B string `json:"b"`
		A int    `json:"a,omitempty"`
		tools.Confirmation
	}
	x, _ := tools.CanonicalArgs(in{B: "x", A: 1})
	y, _ := tools.CanonicalArgs(in{B: "x", A: 1, Confirmation: tools.Confirmation{ConfirmationToken: "t"}})
	if string(x) != string(y) || string(x) != `{"a":1,"b":"x"}` {
		t.Fatalf("%s vs %s", x, y)
	}
}
