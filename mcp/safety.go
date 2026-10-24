package mcp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MinConfirmSecret is the shortest confirm secret http mode accepts.
const MinConfirmSecret = 32

// ConfirmTTL is how long a confirmation token lasts.
const ConfirmTTL = 5 * time.Minute

const tokenPrefix = "pct1"

// ErrInvalidConfirmation is returned for a token that is malformed,
// expired, or issued for another tool, other arguments or another caller.
var ErrInvalidConfirmation = errors.New("invalid confirmation token")

// Confirmer issues and checks the confirmation tokens of two-step tools.
//
// A token is "pct1.<expiry unix>.<mac>", where mac is HMAC-SHA256 with the
// confirm secret over the tool name, the SHA-256 of the canonical arguments,
// the caller's subject (the SHA-256 of their API key) and the expiry. So a
// token works only for the same tool, the same arguments and the same key,
// for 5 minutes, and only on servers that share the secret.
type Confirmer struct {
	secret []byte
	now    func() time.Time
}

// NewConfirmer returns a Confirmer for secret, which must be at least
// MinConfirmSecret bytes.
func NewConfirmer(secret []byte) (*Confirmer, error) {
	if len(secret) < MinConfirmSecret {
		return nil, fmt.Errorf("the confirm secret must be at least %d bytes", MinConfirmSecret)
	}
	return &Confirmer{secret: append([]byte(nil), secret...), now: time.Now}, nil
}

// NewRandomConfirmer returns a Confirmer with a secret of its own, for one
// process (stdio mode): its tokens die with the process.
func NewRandomConfirmer() *Confirmer {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic("pantech-mcp: no randomness: " + err.Error())
	}
	return &Confirmer{secret: secret, now: time.Now}
}

func (c *Confirmer) mac(tool string, args []byte, subject string, exp int64) []byte {
	argsSum := sha256.Sum256(args)
	m := hmac.New(sha256.New, c.secret)
	// Fields are separated by NUL, which none of them can contain.
	fmt.Fprintf(m, "pantech-mcp confirm v1\x00%s\x00%x\x00%s\x00%d", tool, argsSum, subject, exp)
	return m.Sum(nil)
}

// Issue returns a token for tool, args and subject, and its expiry.
func (c *Confirmer) Issue(tool string, args []byte, subject string) (string, time.Time) {
	exp := c.now().Add(ConfirmTTL).Unix()
	mac := c.mac(tool, args, subject, exp)
	return tokenPrefix + "." + strconv.FormatInt(exp, 10) + "." + base64.RawURLEncoding.EncodeToString(mac), time.Unix(exp, 0)
}

// Verify returns nil only for a live token issued for exactly this tool,
// these arguments and this subject. The MAC is compared in constant time.
func (c *Confirmer) Verify(token, tool string, args []byte, subject string) error {
	if len(token) > 128 {
		return ErrInvalidConfirmation
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != tokenPrefix {
		return ErrInvalidConfirmation
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return ErrInvalidConfirmation
	}
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ErrInvalidConfirmation
	}
	want := c.mac(tool, args, subject, exp)
	if !hmac.Equal(got, want) {
		return ErrInvalidConfirmation
	}
	now := c.now().Unix()
	// A token from the future (beyond the TTL) was not issued by us.
	if now > exp || exp > now+int64(ConfirmTTL/time.Second)+60 {
		return ErrInvalidConfirmation
	}
	return nil
}

// IdempotencyKey derives the Idempotency-Key of a confirmed call from its
// token: the same token always sends the same key, so a confirmed call that
// is retried (by the model, the client or the network) is answered with the
// original result instead of making the change twice.
func (c *Confirmer) IdempotencyKey(token string) string {
	sum := sha256.Sum256([]byte("pantech-mcp idempotency v1\x00" + token))
	return "mcp-" + hex.EncodeToString(sum[:24])
}

// Subject is the identity a token is bound to: the SHA-256 of the API key,
// so the key itself is never kept.
func Subject(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:])
}
