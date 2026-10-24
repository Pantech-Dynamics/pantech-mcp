package mcp

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	pantech "github.com/Pantech-Dynamics/pantech-go"
	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// apiKeyPattern is what a bearer must look like before it is forwarded:
// a Pantech API key (PAN_ + base62 + _ + base62 today; a little looser so a
// future key format still passes). Anything else is refused here and never
// reaches the API.
var apiKeyPattern = regexp.MustCompile(`^PAN_[A-Za-z0-9_]{16,250}$`)

// ValidAPIKeyFormat reports whether s looks like a Pantech API key.
func ValidAPIKeyFormat(s string) bool { return apiKeyPattern.MatchString(s) }

// ClientIPHeader carries the AI app's address to the API in http mode (with
// TrustProxy only). The public API ignores it; only a trusted proxy placed
// between this server and the API should turn it into forwarding headers.
const ClientIPHeader = "X-Pantech-Client-IP"

// TokenInfo.Extra keys, for the length of one request. Never logged, stored
// or cached.
const (
	// bearerExtra holds the caller's key.
	bearerExtra = "pantech_api_key"
	// clientIPExtra holds the caller's address, sent as ClientIPHeader.
	clientIPExtra = "pantech_client_ip"
)

// bearerFrom returns the caller's key from a tool call in http mode.
func bearerFrom(req *sdk.CallToolRequest) string {
	return tokenExtra(req, bearerExtra)
}

// clientIPFrom returns the caller's address from a tool call in http mode,
// or "" when it is not to be forwarded.
func clientIPFrom(req *sdk.CallToolRequest) string {
	return tokenExtra(req, clientIPExtra)
}

func tokenExtra(req *sdk.CallToolRequest, name string) string {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return ""
	}
	v, _ := req.Extra.TokenInfo.Extra[name].(string)
	return v
}

// How long a verification is remembered. A good key is re-checked at least
// every 30 seconds (sooner when it expires), so a revoked one stops working
// within that; a bad one is refused for 10 seconds without asking the API.
const (
	validTTL      = 30 * time.Second
	invalidTTL    = 10 * time.Second
	verifyTimeout = 10 * time.Second
	// maxVerifyCache bounds the cache (an entry is ~200 bytes).
	maxVerifyCache = 50_000
	// unavailableRetry is the Retry-After when the API could not say.
	unavailableRetry = 5 * time.Second
)

type verdict int

const (
	// keyValid: the API accepted the key (or refused only what it may do,
	// such as AI_APPS_DISABLED, which the tools then explain).
	keyValid verdict = iota
	// keyInvalid: the API answered 401: unknown, expired or revoked.
	keyInvalid
	// keyUnknown: the API could not be asked (429, 5xx, network). Never a
	// reason to make the app drop its key.
	keyUnknown
)

type verification struct {
	verdict    verdict
	scopes     []string
	expires    time.Time // zero when the key has no expiry or it is unknown
	retryAfter time.Duration
}

// keyVerifier asks the API (GET /me) whether a key is good, remembering the
// answer by the key's SHA-256 (never the key itself), and asking only once at
// a time for the same key.
type keyVerifier struct {
	apiURL string
	http   *http.Client
	now    func() time.Time

	mu       sync.Mutex
	cache    map[string]*list.Element
	order    *list.List // of *cacheEntry, oldest first
	max      int
	inflight map[string]*flight
}

type cacheEntry struct {
	key   string
	v     verification
	until time.Time
}

type flight struct {
	done chan struct{}
	v    verification
}

func newKeyVerifier(apiURL string, hc *http.Client) *keyVerifier {
	if apiURL == "" {
		apiURL = DefaultAPIURL
	}
	if hc == nil {
		hc = &http.Client{Timeout: verifyTimeout}
	}
	return &keyVerifier{
		apiURL: apiURL, http: hc, now: time.Now,
		cache: map[string]*list.Element{}, order: list.New(), max: maxVerifyCache,
		inflight: map[string]*flight{},
	}
}

// cached returns the remembered verification of the key with this hash.
func (v *keyVerifier) cached(hash string) (verification, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.getLocked(hash)
}

func (v *keyVerifier) getLocked(hash string) (verification, bool) {
	el, ok := v.cache[hash]
	if !ok {
		return verification{}, false
	}
	e := el.Value.(*cacheEntry)
	if !v.now().Before(e.until) {
		v.order.Remove(el)
		delete(v.cache, hash)
		return verification{}, false
	}
	return e.v, true
}

func (v *keyVerifier) putLocked(hash string, res verification, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	now := v.now()
	if el, ok := v.cache[hash]; ok {
		v.order.Remove(el)
		delete(v.cache, hash)
	}
	// Expired entries are dropped first, then the oldest, to stay bounded.
	for front := v.order.Front(); front != nil && (len(v.cache) >= v.max || !now.Before(front.Value.(*cacheEntry).until)); front = v.order.Front() {
		v.order.Remove(front)
		delete(v.cache, front.Value.(*cacheEntry).key)
	}
	v.cache[hash] = v.order.PushBack(&cacheEntry{key: hash, v: res, until: now.Add(ttl)})
}

// verify returns whether token is a good key. clientIP, when set, is sent to
// the API as ClientIPHeader.
func (v *keyVerifier) verify(ctx context.Context, token, clientIP string) verification {
	hash := Subject(token)
	v.mu.Lock()
	if res, ok := v.getLocked(hash); ok {
		v.mu.Unlock()
		return res
	}
	if f, ok := v.inflight[hash]; ok {
		v.mu.Unlock()
		select {
		case <-f.done:
			return f.v
		case <-ctx.Done():
			return verification{verdict: keyUnknown, retryAfter: unavailableRetry}
		}
	}
	f := &flight{done: make(chan struct{})}
	v.inflight[hash] = f
	v.mu.Unlock()

	// Not tied to this request: other callers may be waiting on the answer.
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), verifyTimeout)
	res, ttl := v.ask(callCtx, token, clientIP)
	cancel()

	v.mu.Lock()
	v.putLocked(hash, res, ttl)
	delete(v.inflight, hash)
	f.v = res
	v.mu.Unlock()
	close(f.done)
	return res
}

// ask makes the one GET /me and says how long to remember the answer.
func (v *keyVerifier) ask(ctx context.Context, token, clientIP string) (verification, time.Duration) {
	c, err := pantech.NewClient(
		pantech.WithAPIKey(token),
		pantech.WithBaseURL(v.apiURL),
		pantech.WithHTTPClient(v.http),
		pantech.WithUserAgent("pantech-mcp/"+Version),
		pantech.WithHeader("Pantech-Client", "mcp/"+Version),
		// One quick try: the app retries on 503, not us while it waits.
		pantech.WithMaxRetries(0),
	)
	if err != nil {
		return verification{verdict: keyUnknown, retryAfter: unavailableRetry}, 0
	}
	var opts []pantech.RequestOption
	if clientIP != "" {
		opts = append(opts, pantech.WithRequestHeader(ClientIPHeader, clientIP))
	}
	me, err := c.Me.Get(ctx, opts...)
	if err == nil {
		res := verification{verdict: keyValid, scopes: me.APIKey.Scopes}
		ttl := validTTL
		if exp := me.APIKey.ExpiresAt; exp != nil {
			res.expires = *exp
			ttl = min(ttl, exp.Sub(v.now()))
		}
		return res, ttl
	}
	var apiErr *pantech.Error
	if !errors.As(err, &apiErr) {
		return verification{verdict: keyUnknown, retryAfter: unavailableRetry}, 0
	}
	switch {
	case apiErr.Status == http.StatusUnauthorized:
		return verification{verdict: keyInvalid}, invalidTTL
	case apiErr.Status == http.StatusForbidden:
		// The key is real; what it may do (AI_APPS_DISABLED, a scope) is
		// decided per call, and the tools say so clearly.
		return verification{verdict: keyValid}, validTTL
	default:
		retry := apiErr.RetryAfter
		if retry <= 0 {
			retry = unavailableRetry
		}
		return verification{verdict: keyUnknown, retryAfter: retry}, 0
	}
}

// bearerToken is the request's bearer token, or "".
func bearerToken(r *http.Request) string {
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") {
		return ""
	}
	return fields[1]
}

type verifiedKey struct{}

// requireKey lets a request through only with a key the API accepts. A
// missing, malformed, unknown, expired or revoked key gets 401 with
// WWW-Authenticate pointing at the protected resource metadata, which makes
// the AI app sign in again (RFC 9728, RFC 6750). When the API cannot answer
// the answer is 503 with Retry-After, never 401, so a good key is not
// dropped. Failures count against fail per client address, so guessing keys
// stays slow even when the overall per-address limit is generous.
func (h *httpServer) requireKey(v *keyVerifier, fail *limiter, metadataURL string, next http.Handler) http.Handler {
	inner := auth.RequireBearerToken(func(ctx context.Context, _ string, _ *http.Request) (*auth.TokenInfo, error) {
		if ti, ok := ctx.Value(verifiedKey{}).(*auth.TokenInfo); ok {
			return ti, nil
		}
		return nil, auth.ErrInvalidToken
	}, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL:    metadataURL,
		AllowMissingExpiration: true,
		ClockSkew:              time.Minute,
	})(next)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := h.clientIP(r)
		token := bearerToken(r)
		if token == "" {
			h.authFailed(w, fail, ip, metadataURL, false)
			return
		}
		if !ValidAPIKeyFormat(token) {
			h.authFailed(w, fail, ip, metadataURL, true)
			return
		}
		forwardIP := ""
		if h.cfg.TrustProxy {
			forwardIP = ip
		}
		if _, ok := v.cached(Subject(token)); !ok && fail.exhausted("ip:"+ip) {
			// Too many bad keys from here lately: do not ask the API about
			// another unknown one yet.
			tooMany(w)
			return
		}
		res := v.verify(r.Context(), token, forwardIP)
		switch res.verdict {
		case keyInvalid:
			h.authFailed(w, fail, ip, metadataURL, true)
			return
		case keyUnknown:
			unavailable(w, res.retryAfter)
			return
		}
		ti := &auth.TokenInfo{UserID: Subject(token), Scopes: res.scopes, Extra: map[string]any{bearerExtra: token}}
		if res.expires.After(v.now()) {
			ti.Expiration = res.expires
		}
		if forwardIP != "" {
			ti.Extra[clientIPExtra] = forwardIP
		}
		inner.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), verifiedKey{}, ti)))
	})
}

// authFailed answers 401 (or 429 once this address has failed too often).
func (h *httpServer) authFailed(w http.ResponseWriter, fail *limiter, ip, metadataURL string, invalid bool) {
	if !fail.allow("ip:" + ip) {
		tooMany(w)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "application/json")
	hd.Set("Cache-Control", "no-store")
	if invalid {
		hd.Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="invalid_token", resource_metadata=%q`, metadataURL))
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_token","error_description":"The access token is invalid, expired or revoked. Sign in to Pantech Dynamics again."}` + "\n"))
		return
	}
	hd.Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q`, metadataURL))
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized","error_description":"Sign in to Pantech Dynamics to use this server."}` + "\n"))
}

// unavailable answers 503: the key could not be checked right now.
func unavailable(w http.ResponseWriter, retry time.Duration) {
	secs := int(math.Ceil(retry.Seconds()))
	secs = min(max(secs, 1), 60)
	hd := w.Header()
	hd.Set("Retry-After", strconv.Itoa(secs))
	hd.Set("Content-Type", "application/json")
	hd.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"error":"temporarily_unavailable","error_description":"Pantech Dynamics could not check the sign-in just now. Try again shortly."}` + "\n"))
}

// ProtectedResourceMetadata is this server's RFC 9728 document.
func ProtectedResourceMetadata(publicURL, authorizationServer string) *oauthex.ProtectedResourceMetadata {
	return &oauthex.ProtectedResourceMetadata{
		Resource:               strings.TrimRight(publicURL, "/") + "/mcp",
		AuthorizationServers:   []string{strings.TrimRight(authorizationServer, "/")},
		ScopesSupported:        []string{"read", "write", "delete"},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "Pantech Dynamics",
		ResourceDocumentation:  "https://docs.pantechdynamics.com",
	}
}

// MetadataHandler serves the protected resource metadata, open to any origin.
func MetadataHandler(publicURL, authorizationServer string) http.Handler {
	return auth.ProtectedResourceMetadataHandler(ProtectedResourceMetadata(publicURL, authorizationServer))
}
