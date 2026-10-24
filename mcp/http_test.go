package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/Pantech-Dynamics/pantech-mcp/assets"
)

const testKey = "PAN_" + "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmnopqrstuvwxyz0123" + "_aB1234"

func testHTTPConfig(apiURL string) HTTPConfig {
	return HTTPConfig{
		Addr:                ":0",
		PublicURL:           "https://mcp.example.test",
		AuthorizationServer: "https://console.example.test",
		APIURL:              apiURL,
		ConfirmSecret:       []byte(strings.Repeat("x", 32)),
		RateLimitIP:         -1,
		RateLimitToken:      -1,
	}
}

func newTestHandler(t *testing.T, cfg HTTPConfig) http.Handler {
	t.Helper()
	h, err := NewHTTPHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func initializeBody() string {
	return `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
}

func post(h http.Handler, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "https://mcp.example.test/mcp", strings.NewReader(initializeBody()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestValidateRefusesUnsafeConfig(t *testing.T) {
	good := testHTTPConfig("https://api.example.test/public/v1")
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*HTTPConfig){
		"no secret":         func(c *HTTPConfig) { c.ConfirmSecret = nil },
		"short secret":      func(c *HTTPConfig) { c.ConfirmSecret = []byte("short") },
		"no public url":     func(c *HTTPConfig) { c.PublicURL = "" },
		"http public url":   func(c *HTTPConfig) { c.PublicURL = "http://mcp.example.test" },
		"public url path":   func(c *HTTPConfig) { c.PublicURL = "https://mcp.example.test/mcp" },
		"http auth server":  func(c *HTTPConfig) { c.AuthorizationServer = "http://console.example.test" },
		"http api":          func(c *HTTPConfig) { c.APIURL = "http://api.example.test/public/v1" },
		"no auth server":    func(c *HTTPConfig) { c.AuthorizationServer = "" },
		"javascript scheme": func(c *HTTPConfig) { c.PublicURL = "javascript://x" },
	} {
		c := good
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	local := good
	local.PublicURL = "http://localhost:8080"
	if err := local.Validate(); err != nil {
		t.Errorf("http on localhost: %v", err)
	}
}

func TestAuthRequired(t *testing.T) {
	h := newTestHandler(t, testHTTPConfig(meAPI(t)))
	for name, auth := range map[string]string{
		"missing":        "",
		"not bearer":     "Basic " + testKey,
		"not a pan key":  "Bearer eyJhbGciOiJIUzI1NiJ9.e30.x",
		"too short":      "Bearer PAN_short",
		"bad characters": "Bearer PAN_abcdefghijklmnopqrstuvwxyz/../../x",
		"too long":       "Bearer PAN_" + strings.Repeat("a", 300),
	} {
		// No token: just where to sign in. A token we refuse: invalid_token.
		want := `Bearer error="invalid_token", ` + metadataHeader
		if auth == "" || strings.HasPrefix(auth, "Basic") {
			want = `Bearer ` + metadataHeader
		}
		hdr := map[string]string{}
		if auth != "" {
			hdr["Authorization"] = auth
		}
		rec := post(h, hdr)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d", name, rec.Code)
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != want {
			t.Errorf("%s: WWW-Authenticate %q", name, got)
		}
		if strings.Contains(rec.Body.String(), "PAN_") {
			t.Errorf("%s: the body echoes the token", name)
		}
	}
	if rec := post(h, map[string]string{"Authorization": "Bearer " + testKey}); rec.Code != http.StatusOK {
		t.Errorf("a well-formed key: status %d %s", rec.Code, rec.Body)
	}
}

func TestProtectedResourceMetadata(t *testing.T) {
	h := newTestHandler(t, testHTTPConfig("https://api.example.test/public/v1"))
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://mcp.example.test"+path, nil))
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Header().Get("Content-Type"))
		}
		var doc map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"resource":                 "https://mcp.example.test/mcp",
			"authorization_servers":    []any{"https://console.example.test"},
			"scopes_supported":         []any{"read", "write", "delete"},
			"bearer_methods_supported": []any{"header"},
			"resource_name":            "Pantech Dynamics",
		}
		for k, v := range want {
			got, _ := json.Marshal(doc[k])
			exp, _ := json.Marshal(v)
			if !bytes.Equal(got, exp) {
				t.Errorf("%s %s = %s, want %s", path, k, got, exp)
			}
		}
	}
}

func TestOriginChecks(t *testing.T) {
	cfg := testHTTPConfig(meAPI(t))
	cfg.AllowedOrigins = []string{"https://app.example.test"}
	h := newTestHandler(t, cfg)
	auth := "Bearer " + testKey
	known := KnownAIClientOrigins[0]
	cases := map[string]bool{
		"":                         true,
		"https://app.example.test": true,
		"https://evil.example":     false,
		// The same host over plain http is a different origin.
		"http://" + strings.TrimPrefix(known, "https://"): false,
		"null": false,
	}
	for _, o := range KnownAIClientOrigins {
		cases[o] = true
	}
	for origin, wantOK := range cases {
		hdr := map[string]string{"Authorization": auth}
		if origin != "" {
			hdr["Origin"] = origin
		}
		rec := post(h, hdr)
		if ok := rec.Code == http.StatusOK; ok != wantOK {
			t.Errorf("origin %q: status %d", origin, rec.Code)
		}
		if wantOK && origin != "" && rec.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Errorf("origin %q: no CORS header", origin)
		}
	}
	// A preflight from an allowed origin needs no token.
	req := httptest.NewRequest(http.MethodOptions, "https://mcp.example.test/mcp", nil)
	req.Header.Set("Origin", known)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Errorf("preflight: %d %v", rec.Code, rec.Header())
	}
}

func TestHostCheck(t *testing.T) {
	h := newTestHandler(t, testHTTPConfig(meAPI(t)))
	for host, wantOK := range map[string]bool{"mcp.example.test": true, "127.0.0.1:8080": true, "localhost": true, "rebind.evil.example": false} {
		req := httptest.NewRequest(http.MethodPost, "https://mcp.example.test/mcp", strings.NewReader(initializeBody()))
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+testKey)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if (rec.Code == http.StatusOK) != wantOK {
			t.Errorf("host %q: status %d", host, rec.Code)
		}
	}
}

func TestBodyLimit(t *testing.T) {
	h := newTestHandler(t, testHTTPConfig("https://api.example.test/public/v1"))
	big := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"` + strings.Repeat("a", MaxBodyBytes) + `"}}`
	req := httptest.NewRequest(http.MethodPost, "https://mcp.example.test/mcp", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestRateLimits(t *testing.T) {
	cfg := testHTTPConfig(meAPI(t))
	cfg.RateLimitIP = 4 // burst 2
	h := newTestHandler(t, cfg)
	codes := []int{}
	for range 4 {
		codes = append(codes, post(h, map[string]string{"Authorization": "Bearer " + testKey}).Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("per IP: %v", codes)
	}

	cfg = testHTTPConfig(meAPI(t))
	cfg.RateLimitToken = 2 // burst 1
	h = newTestHandler(t, cfg)
	if c := post(h, map[string]string{"Authorization": "Bearer " + testKey}).Code; c != 200 {
		t.Fatalf("first: %d", c)
	}
	if c := post(h, map[string]string{"Authorization": "Bearer " + testKey}).Code; c != http.StatusTooManyRequests {
		t.Fatalf("second with the same key: %d", c)
	}
	other := testKey[:len(testKey)-1] + "9"
	if c := post(h, map[string]string{"Authorization": "Bearer " + other}).Code; c != 200 {
		t.Fatalf("another key: %d", c)
	}
}

func TestForwardedForTrustedOnlyWithProxy(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "192.0.2.66, 203.0.113.9")
	if ip := (&httpServer{}).clientIP(r); ip != "10.0.0.1" {
		t.Errorf("untrusted: %s", ip)
	}
	if ip := (&httpServer{cfg: HTTPConfig{TrustProxy: true}}).clientIP(r); ip != "203.0.113.9" {
		t.Errorf("trusted: %s (want the entry the proxy added)", ip)
	}
	r.Header.Set("X-Real-IP", "198.51.100.7")
	if ip := (&httpServer{cfg: HTTPConfig{TrustProxy: true}}).clientIP(r); ip != "198.51.100.7" {
		t.Errorf("X-Real-IP: %s", ip)
	}
}

func TestLimiterRefills(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(60, 1)
	l.now = func() time.Time { return now }
	if !l.allow("a") || l.allow("a") {
		t.Fatal("burst of 1")
	}
	now = now.Add(time.Second)
	if !l.allow("a") {
		t.Fatal("did not refill")
	}
	now = now.Add(time.Hour)
	l.allow("b")
	if _, ok := l.buckets["a"]; ok {
		t.Fatal("idle bucket kept")
	}
}

func TestLimiterStaysBounded(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(60, 2)
	l.now = func() time.Time { return now }
	l.maxBuckets = 100
	// A flood of made-up keys, faster than buckets go idle.
	for i := range 1000 {
		l.allow(fmt.Sprintf("key:%d", i))
		l.allow(fmt.Sprintf("key:%d", i))
	}
	if n := len(l.buckets); n > l.maxBuckets {
		t.Fatalf("%d buckets, cap %d", n, l.maxBuckets)
	}
	// A caller at its limit keeps its bucket when idle, full ones go first.
	l.buckets = map[string]*bucket{}
	l.allow("busy")
	l.allow("busy")
	for i := range 99 {
		l.buckets[fmt.Sprintf("idle:%d", i)] = &bucket{lim: rate.NewLimiter(1, 2), seen: now}
	}
	l.allow("new")
	if _, ok := l.buckets["busy"]; !ok || l.allow("busy") {
		t.Fatal("eviction forgot a caller at its limit")
	}
}

func TestHealthzAndIcons(t *testing.T) {
	h := newTestHandler(t, testHTTPConfig("https://api.example.test/public/v1"))
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://mcp.example.test"+path, nil))
		return rec
	}
	if rec := get("/healthz"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Errorf("healthz: %d %s", rec.Code, rec.Body)
	}
	for path, want := range map[string]struct {
		ctype string
		body  []byte
	}{
		"/icon.svg":    {"image/svg+xml", assets.IconSVG},
		"/icon.png":    {"image/png", assets.IconPNG},
		"/favicon.ico": {"image/png", assets.IconPNG},
	} {
		rec := get(path)
		body, _ := io.ReadAll(rec.Body)
		if rec.Code != 200 || rec.Header().Get("Content-Type") != want.ctype || !bytes.Equal(body, want.body) {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Header().Get("Content-Type"))
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), "max-age=604800") || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: headers %v", path, rec.Header())
		}
	}
	if len(assets.IconSVG) == 0 || !bytes.HasPrefix(assets.IconPNG, []byte("\x89PNG")) {
		t.Error("the embedded logo is missing")
	}
	if rec := get("/nope"); rec.Code != 404 {
		t.Errorf("unknown path: %d", rec.Code)
	}
}

func TestServerInfoIcons(t *testing.T) {
	h := newTestHandler(t, testHTTPConfig(meAPI(t)))
	rec := post(h, map[string]string{"Authorization": "Bearer " + testKey})
	body := rec.Body.String()
	for _, want := range []string{`"name":"pantech"`, `"title":"Pantech Dynamics"`, `"websiteUrl":"https://pantechdynamics.com"`,
		`"src":"https://mcp.example.test/icon.svg"`, `"mimeType":"image/svg+xml"`} {
		if !strings.Contains(body, want) {
			t.Errorf("initialize result lacks %s: %s", want, body)
		}
	}
}
