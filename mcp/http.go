package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Pantech-Dynamics/pantech-mcp/assets"
)

// MaxBodyBytes caps an HTTP request body.
const MaxBodyBytes = 1 << 20

// KnownAIClientOrigins are the browser origins of AI apps that may call the
// server from a web page. Others must be listed in MCP_ALLOWED_ORIGINS.
var KnownAIClientOrigins = []string{
	"https://claude.ai",
	"https://chatgpt.com",
	"https://chat.openai.com",
	"https://grok.com",
	"https://gemini.google.com",
}

// HTTPConfig configures http mode.
type HTTPConfig struct {
	// Addr is the listen address, e.g. ":8080".
	Addr string
	// PublicURL is the server's own origin, e.g. https://mcp.pantechdynamics.com.
	// https is required except for localhost.
	PublicURL string
	// AuthorizationServer is the OAuth issuer, e.g. https://console.pantechdynamics.com.
	AuthorizationServer string
	// APIURL is the public API base URL.
	APIURL string
	// ConfirmSecret signs confirmation tokens; at least 32 bytes. Replicas
	// behind one URL must share it.
	ConfirmSecret []byte
	// TrustProxy trusts X-Forwarded-For / X-Real-IP / X-Request-Id, set by
	// the reverse proxy in front. Never set it without one.
	TrustProxy bool
	// AllowedOrigins are browser origins allowed besides KnownAIClientOrigins.
	AllowedOrigins []string
	// RateLimitIP and RateLimitToken are requests per minute per client IP
	// and per API key. 0 means the defaults (1200 and 120); negative turns
	// off. The per-IP limit is generous because hosted AI apps reach the
	// server from a few shared addresses; RateLimitAuthFailures keeps
	// requests without a good key tight per IP (default 60).
	RateLimitIP           int
	RateLimitToken        int
	RateLimitAuthFailures int
	Logger                *slog.Logger
	// APIHTTPClient is used for API calls (tests).
	APIHTTPClient *http.Client
	// DocsURL overrides the agent brief's URL (tests).
	DocsURL string
	// WaitBudget and PollInterval shorten waits (tests).
	WaitBudget   time.Duration
	PollInterval time.Duration
}

// Validate checks the configuration http mode refuses to start without.
func (c *HTTPConfig) Validate() error {
	if len(c.ConfirmSecret) < MinConfirmSecret {
		return fmt.Errorf("MCP_CONFIRM_SECRET is required and must be at least %d bytes (e.g. openssl rand -base64 48)", MinConfirmSecret)
	}
	if err := checkOrigin("MCP_PUBLIC_URL", c.PublicURL); err != nil {
		return err
	}
	if err := checkOrigin("MCP_AUTHORIZATION_SERVER", c.AuthorizationServer); err != nil {
		return err
	}
	return c.ValidateAPIURL()
}

// ValidateAPIURL refuses an API URL the key would travel to in clear text:
// it must be https, or http on loopback. Empty means the default.
func (c *HTTPConfig) ValidateAPIURL() error {
	if c.APIURL != "" {
		u, err := url.Parse(c.APIURL)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname()))) {
			return errors.New("PANTECH_API_URL must be an https URL (http only for localhost)")
		}
	}
	return nil
}

// checkOrigin requires an https origin with no path, or http on localhost.
func checkOrigin(name, raw string) error {
	if raw == "" {
		return fmt.Errorf("%s is required", name)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("%s must be an origin such as https://mcp.pantechdynamics.com (no path)", name)
	}
	if u.Scheme == "https" || (u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return nil
	}
	return fmt.Errorf("%s must use https (http is allowed only for localhost)", name)
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// NewHTTPHandler returns the http mode handler: /mcp, the protected
// resource metadata, /healthz and the logo.
func NewHTTPHandler(cfg HTTPConfig) (http.Handler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	confirm, err := NewConfirmer(cfg.ConfirmSecret)
	if err != nil {
		return nil, err
	}
	publicURL := strings.TrimRight(cfg.PublicURL, "/")
	server := New(Options{
		APIURL:     cfg.APIURL,
		Confirm:    confirm,
		HTTPClient: cfg.APIHTTPClient,
		IconURL:    publicURL + "/icon.svg",
		DocsURL:    cfg.DocsURL,
		Logger:     log,

		WaitBudget:   cfg.WaitBudget,
		PollInterval: cfg.PollInterval,
	})
	streamable := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{
		Stateless:           true,
		MaxRequestBodyBytes: MaxBodyBytes,
		// The proxy in front connects over loopback with the public Host, which
		// the SDK's own check would refuse; hostCheck below does the same job.
		DisableLocalhostProtection: true,
		Logger:                     log,
	})

	ipLimit := newLimiter(defaultRate(cfg.RateLimitIP, DefaultRateLimitIP), 0)
	tokenLimit := newLimiter(defaultRate(cfg.RateLimitToken, DefaultRateLimitToken), 0)
	failLimit := newLimiter(defaultRate(cfg.RateLimitAuthFailures, DefaultRateLimitAuthFailures), 0)
	h := &httpServer{cfg: cfg, log: log, origins: originSet(cfg.AllowedOrigins, publicURL), publicHost: hostnameOf(publicURL)}

	metadataURL := publicURL + "/.well-known/oauth-protected-resource"
	verifier := newKeyVerifier(cfg.APIURL, cfg.APIHTTPClient)
	mcpHandler := h.requireKey(verifier, failLimit, metadataURL, h.limitToken(tokenLimit, noStore(streamable)))

	mux := http.NewServeMux()
	mux.Handle("/mcp", h.hostCheck(h.limitIP(ipLimit, h.originCheck(maxBody(mcpHandler)))))
	metadata := MetadataHandler(publicURL, cfg.AuthorizationServer)
	mux.Handle("/.well-known/oauth-protected-resource", h.limitIP(ipLimit, metadata))
	mux.Handle("/.well-known/oauth-protected-resource/mcp", h.limitIP(ipLimit, metadata))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
	})
	mux.Handle("GET /icon.svg", staticFile("image/svg+xml", assets.IconSVG))
	mux.Handle("GET /icon.png", staticFile("image/png", assets.IconPNG))
	mux.Handle("GET /favicon.ico", staticFile("image/png", assets.IconPNG))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	return h.requestID(h.accessLog(securityHeaders(mux))), nil
}

// Default rate limits, requests a minute.
const (
	DefaultRateLimitIP           = 1200
	DefaultRateLimitToken        = 120
	DefaultRateLimitAuthFailures = 60
)

func defaultRate(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// Serve runs http mode until ctx is cancelled, then shuts down gracefully.
func Serve(ctx context.Context, cfg HTTPConfig) error {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	handler, err := NewHTTPHandler(cfg)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Tool calls wait up to ~60s for a change, streaming progress.
		WriteTimeout:   150 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 32 << 10,
		ErrorLog:       slog.NewLogLogger(cfg.Logger.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	cfg.Logger.Info("listening", slog.String("addr", cfg.Addr), slog.String("public_url", cfg.PublicURL), slog.String("version", Version))
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	cfg.Logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

type httpServer struct {
	cfg        HTTPConfig
	log        *slog.Logger
	origins    map[string]bool
	publicHost string
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,128}$`)

// requestID gives every request an id, in X-Request-Id on the request (for
// the tool logs) and on the response. The proxy's id is kept only when the
// proxy is trusted.
func (h *httpServer) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !h.cfg.TrustProxy || !requestIDPattern.MatchString(id) {
			id = newRequestID()
		}
		r.Header.Set(RequestIDHeader, id)
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Flush keeps streaming (SSE) working through the wrapper.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// accessLog logs one line per HTTP request: never headers, query strings or
// bodies.
func (h *httpServer) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/healthz" {
			return
		}
		h.log.Info("http request",
			slog.String("request_id", r.Header.Get(RequestIDHeader)),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", sw.status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("client_ip", h.clientIP(r)),
		)
	})
}

// clientIP is the caller's address: the connection's, or with TrustProxy
// X-Real-IP, else the last X-Forwarded-For entry (the one the proxy added;
// earlier entries are whatever the client sent).
func (h *httpServer) clientIP(r *http.Request) string {
	if h.cfg.TrustProxy {
		if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
			return ip.String()
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func tooMany(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"error":"rate_limited","error_description":"Too many requests: slow down and try again in a minute."}` + "\n"))
}

func (h *httpServer) limitIP(l *limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow("ip:" + h.clientIP(r)) {
			tooMany(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limitToken limits per API key, by its hash (set by requireKey).
func (h *httpServer) limitToken(l *limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ti := auth.TokenInfoFromContext(r.Context()); ti != nil && !l.allow("key:"+ti.UserID) {
			tooMany(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostnameOf is an origin's lowercase host name, without port.
func hostnameOf(origin string) string {
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// hostCheck refuses a Host that is neither the public host nor loopback:
// a DNS rebinding page cannot drive the server through a name of its own.
func (h *httpServer) hostCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := hostnameOf("http://" + r.Host)
		if name == "" || (name != h.publicHost && !isLoopbackHost(name)) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func originSet(extra []string, publicURL string) map[string]bool {
	set := map[string]bool{}
	for _, o := range append(append(append([]string{}, KnownAIClientOrigins...), extra...), publicURL) {
		if o = normalizeOrigin(o); o != "" {
			set[o] = true
		}
	}
	return set
}

func normalizeOrigin(o string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(o)), "/")
}

// originCheck allows requests with no Origin (every non-browser client), and
// browser requests only from an allowed origin, to which it answers CORS.
func (h *httpServer) originCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !h.origins[normalizeOrigin(origin)] {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		hd := w.Header()
		hd.Set("Access-Control-Allow-Origin", origin)
		hd.Add("Vary", "Origin")
		hd.Set("Access-Control-Expose-Headers", "WWW-Authenticate, Mcp-Session-Id, Mcp-Protocol-Version, X-Request-Id")
		if r.Method == http.MethodOptions {
			hd.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			hd.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Mcp-Session-Id, Mcp-Protocol-Version, Last-Event-ID")
			hd.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func maxBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > MaxBodyBytes {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// staticFile serves an embedded file, public and cached for a week.
func staticFile(contentType string, body []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Type", contentType)
		hd.Set("Content-Length", strconv.Itoa(len(body)))
		hd.Set("Cache-Control", "public, max-age=604800, immutable")
		hd.Set("Access-Control-Allow-Origin", "*")
		hd.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		_, _ = w.Write(body)
	})
}
