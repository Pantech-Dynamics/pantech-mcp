package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// keyAPI is a fake public API that answers GET /me according to the key:
// per key a status (200 by default), and records what it was sent.
type keyAPI struct {
	mu       sync.Mutex
	status   map[string]int // key -> status for GET /me
	expires  map[string]string
	calls    map[string]int
	clientIP []string
	delay    time.Duration
	total    atomic.Int64
}

func newKeyAPI(t *testing.T) (*keyAPI, string) {
	t.Helper()
	k := &keyAPI{status: map[string]int{}, expires: map[string]string{}, calls: map[string]int{}}
	srv := httptest.NewServer(k)
	t.Cleanup(srv.Close)
	return k, srv.URL + apiPrefix
}

// meAPI is an API that accepts every key.
func meAPI(t *testing.T) string {
	t.Helper()
	_, u := newKeyAPI(t)
	return u
}

func (k *keyAPI) set(key string, status int) {
	k.mu.Lock()
	k.status[key] = status
	k.mu.Unlock()
}

func (k *keyAPI) count(key string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.calls[key]
}

func (k *keyAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k.total.Add(1)
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	k.mu.Lock()
	k.calls[key]++
	k.clientIP = append(k.clientIP, r.Header.Get(ClientIPHeader))
	status, ok := k.status[key]
	exp := k.expires[key]
	delay := k.delay
	k.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	if !ok {
		status = 200
	}
	if r.URL.Path != apiPrefix+"/me" {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[],"next_cursor":null}`)
		return
	}
	switch status {
	case 200:
		w.Header().Set("Content-Type", "application/json")
		expJSON := "null"
		if exp != "" {
			expJSON = fmt.Sprintf("%q", exp)
		}
		fmt.Fprintf(w, `{"organization_id":"org_1","project_id":"prj_1","api_key":{"id":"key_1","name":"Example app","kind":"ai_app","scopes":["read","write"],"expires_at":%s}}`, expJSON)
	case 401:
		problem(w, 401, "UNAUTHENTICATED", "")
	case 403:
		problem(w, 403, "AI_APPS_DISABLED", "")
	case 429:
		problem(w, 429, "RATE_LIMITED", "7")
	default:
		problem(w, status, "INTERNAL", "")
	}
}

func problem(w http.ResponseWriter, status int, code, retryAfter string) {
	w.Header().Set("Content-Type", "application/problem+json")
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"title":"x","status":%d,"code":%q,"detail":"x","request_id":"req_1"}`, status, code)
}

func keyN(i int) string { return testKey[:len(testKey)-4] + fmt.Sprintf("%04d", i) }

const metadataHeader = `resource_metadata="https://mcp.example.test/.well-known/oauth-protected-resource"`

func TestVerifyAgainstTheAPI(t *testing.T) {
	api, u := newKeyAPI(t)
	cfg := testHTTPConfig(u)
	h := newTestHandler(t, cfg)

	valid, invalid, disabled, limited, broken := keyN(1), keyN(2), keyN(3), keyN(4), keyN(5)
	api.set(invalid, 401)
	api.set(disabled, 403)
	api.set(limited, 429)
	api.set(broken, 502)

	if rec := post(h, map[string]string{"Authorization": "Bearer " + valid}); rec.Code != 200 {
		t.Errorf("valid: %d %s", rec.Code, rec.Body)
	}
	rec := post(h, map[string]string{"Authorization": "Bearer " + invalid})
	if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") != `Bearer error="invalid_token", `+metadataHeader {
		t.Errorf("invalid: %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	if strings.Contains(rec.Body.String(), "PAN_") {
		t.Error("the body echoes the token")
	}
	if rec := post(h, map[string]string{"Authorization": "Bearer " + disabled}); rec.Code != 200 {
		t.Errorf("AI apps disabled: %d (want it let through for the tools to explain)", rec.Code)
	}
	for name, key := range map[string]string{"429": limited, "502": broken} {
		rec := post(h, map[string]string{"Authorization": "Bearer " + key})
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" || rec.Header().Get("WWW-Authenticate") != "" {
			t.Errorf("%s: %d %v (want 503 with Retry-After, never 401)", name, rec.Code, rec.Header())
		}
	}
	if got := post(h, map[string]string{"Authorization": "Bearer " + limited}).Header().Get("Retry-After"); got != "7" {
		t.Errorf("429 Retry-After %q, want the API's 7", got)
	}
	// Unknown is never remembered: the API is asked again.
	if n := api.count(broken); n != 1 {
		t.Errorf("502 asked %d times", n)
	}
	post(h, map[string]string{"Authorization": "Bearer " + broken})
	if n := api.count(broken); n != 2 {
		t.Errorf("502 cached: asked %d times", n)
	}
}

func TestVerifyNetworkErrorIs503(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL + apiPrefix
	srv.Close()
	h := newTestHandler(t, testHTTPConfig(u))
	rec := post(h, map[string]string{"Authorization": "Bearer " + testKey})
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
}

func TestVerifyCaches(t *testing.T) {
	api, u := newKeyAPI(t)
	v := newKeyVerifier(u, nil)
	now := time.Unix(1_000_000, 0)
	v.now = func() time.Time { return now }
	ctx := context.Background()

	good, bad := keyN(1), keyN(2)
	api.set(bad, 401)
	for range 3 {
		if v.verify(ctx, good, "").verdict != keyValid || v.verify(ctx, bad, "").verdict != keyInvalid {
			t.Fatal("verdicts")
		}
	}
	if api.count(good) != 1 || api.count(bad) != 1 {
		t.Fatalf("asked good %d, bad %d times", api.count(good), api.count(bad))
	}
	// A bad key is re-checked after 10s, a good one after 30s.
	now = now.Add(11 * time.Second)
	v.verify(ctx, good, "")
	v.verify(ctx, bad, "")
	if api.count(good) != 1 || api.count(bad) != 2 {
		t.Fatalf("after 11s: good %d, bad %d", api.count(good), api.count(bad))
	}
	// Revoked: within 30s the next check says so.
	api.set(good, 401)
	now = now.Add(20 * time.Second)
	if v.verify(ctx, good, "").verdict != keyInvalid {
		t.Fatal("revoked key still valid after 31s")
	}

	// A key expiring in 5s is remembered no longer than that.
	soon := keyN(3)
	api.mu.Lock()
	api.expires[soon] = now.Add(5 * time.Second).UTC().Format(time.RFC3339)
	api.mu.Unlock()
	res := v.verify(ctx, soon, "")
	if res.verdict != keyValid || res.expires.IsZero() || len(res.scopes) != 2 {
		t.Fatalf("expiring key: %+v", res)
	}
	now = now.Add(6 * time.Second)
	api.set(soon, 401)
	if v.verify(ctx, soon, "").verdict != keyInvalid {
		t.Fatal("expired key kept past its expiry")
	}

	// Nothing but hashes is kept.
	v.mu.Lock()
	for k := range v.cache {
		if strings.Contains(k, "PAN_") {
			t.Error("the cache holds a key")
		}
	}
	v.mu.Unlock()
}

func TestVerifyExpiredKeyIs401(t *testing.T) {
	api, u := newKeyAPI(t)
	expired := keyN(9)
	api.set(expired, 401) // the API refuses an expired key with 401
	h := newTestHandler(t, testHTTPConfig(u))
	if rec := post(h, map[string]string{"Authorization": "Bearer " + expired}); rec.Code != 401 ||
		!strings.Contains(rec.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
}

func TestVerifyCacheStaysBounded(t *testing.T) {
	_, u := newKeyAPI(t)
	v := newKeyVerifier(u, nil)
	v.max = 50
	for i := range 200 {
		v.verify(context.Background(), keyN(i), "")
	}
	v.mu.Lock()
	n, m := len(v.cache), v.order.Len()
	v.mu.Unlock()
	if n > 50 || n != m {
		t.Fatalf("%d entries (%d in order), cap 50", n, m)
	}
	// The newest are kept, the oldest went.
	if _, ok := v.cached(Subject(keyN(199))); !ok {
		t.Error("newest evicted")
	}
	if _, ok := v.cached(Subject(keyN(0))); ok {
		t.Error("oldest kept")
	}
}

func TestVerifyConcurrentAsksOnce(t *testing.T) {
	api, u := newKeyAPI(t)
	api.delay = 50 * time.Millisecond
	v := newKeyVerifier(u, nil)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := keyN(i % 5)
			if v.verify(context.Background(), key, "").verdict != keyValid {
				t.Error("not valid")
			}
		}()
	}
	wg.Wait()
	if n := api.total.Load(); n != 5 {
		t.Fatalf("%d API calls for 5 keys", n)
	}
}

func TestAuthFailuresLimitedPerIP(t *testing.T) {
	api, u := newKeyAPI(t)
	cfg := testHTTPConfig(u)
	cfg.RateLimitAuthFailures = 4 // burst 2
	h := newTestHandler(t, cfg)
	good := keyN(1)
	post(h, map[string]string{"Authorization": "Bearer " + good}) // remembered
	codes := []int{}
	for i := range 4 {
		bad := keyN(100 + i)
		api.set(bad, 401)
		codes = append(codes, post(h, map[string]string{"Authorization": "Bearer " + bad}).Code)
	}
	if codes[0] != 401 || codes[1] != 401 || codes[2] != 429 || codes[3] != 429 {
		t.Fatalf("bad keys: %v", codes)
	}
	// Guessing stopped reaching the API.
	if n := api.count(keyN(103)); n != 0 {
		t.Errorf("the API was asked about a key after the limit: %d", n)
	}
	// A key already known to be good still works from the same address.
	if c := post(h, map[string]string{"Authorization": "Bearer " + good}).Code; c != 200 {
		t.Errorf("known good key: %d", c)
	}
	// No token at all counts too.
	if c := post(h, nil).Code; c != 429 {
		t.Errorf("missing token after the limit: %d", c)
	}
}

func TestClientIPForwardedOnlyWithTrustProxy(t *testing.T) {
	for _, trust := range []bool{false, true} {
		api, u := newKeyAPI(t)
		cfg := testHTTPConfig(u)
		cfg.TrustProxy = trust
		h := newTestHandler(t, cfg)
		post(h, map[string]string{"Authorization": "Bearer " + testKey, "X-Real-IP": "203.0.113.5"})
		api.mu.Lock()
		got := append([]string{}, api.clientIP...)
		api.mu.Unlock()
		want := ""
		if trust {
			want = "203.0.113.5"
		}
		if len(got) != 1 || got[0] != want {
			t.Errorf("trust %v: %s sent %q", trust, ClientIPHeader, got)
		}
	}
}

func TestTokenInfoFromMe(t *testing.T) {
	api, u := newKeyAPI(t)
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	api.mu.Lock()
	api.expires[testKey] = exp.Format(time.RFC3339)
	api.mu.Unlock()
	cfg := testHTTPConfig(u)
	cfg.TrustProxy = true
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	hs := &httpServer{cfg: cfg}
	var got *auth.TokenInfo
	h := hs.requireKey(newKeyVerifier(u, nil), newLimiter(-1, 0), "https://mcp.example.test/.well-known/oauth-protected-resource",
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = auth.TokenInfoFromContext(r.Context()) }))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+testKey)
	req.Header.Set("X-Real-IP", "2001:db8::1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got == nil {
		t.Fatal("not let through")
	}
	if !got.Expiration.Equal(exp) || strings.Join(got.Scopes, ",") != "read,write" || got.UserID != Subject(testKey) ||
		got.Extra[bearerExtra] != testKey || got.Extra[clientIPExtra] != "2001:db8::1" {
		t.Fatalf("%+v", got)
	}
}
