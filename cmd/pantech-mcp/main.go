// Command pantech-mcp is Pantech Dynamics' MCP server.
//
//	pantech-mcp stdio     local, for one AI app, with your CLI sign-in or PANTECH_API_KEY
//	pantech-mcp http      hosted Streamable HTTP, each caller's own key (OAuth)
//	pantech-mcp version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Pantech-Dynamics/pantech-mcp/internal/cliconfig"
	"github.com/Pantech-Dynamics/pantech-mcp/mcp"
)

const usage = `pantech-mcp: Pantech Dynamics' MCP server.

Usage:
  pantech-mcp stdio     Serve one local AI app over stdin/stdout. The key is
                        $PANTECH_API_KEY, else the Pantech CLI's sign-in
                        (pantech auth login; $PANTECH_PROFILE picks a profile).
  pantech-mcp http      Serve Streamable HTTP at /mcp, each caller with their own
                        key (OAuth). See pantech-mcp http -h.
  pantech-mcp version   Print the version.
  pantech-mcp help      Show this help.

Docs: https://docs.pantechdynamics.com/mcp
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch args[0] {
	case "stdio":
		err = runStdio(ctx, args[1:], stderr)
	case "http":
		err = runHTTP(ctx, args[1:], stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "pantech-mcp "+mcp.Version)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "pantech-mcp: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, "pantech-mcp: "+err.Error())
		return 1
	}
	return 0
}

func newLogger(w io.Writer) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv("MCP_LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// runStdio serves one local client. Logs go to stderr: stdout is the protocol.
func runStdio(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("stdio", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiURL := fs.String("api-url", os.Getenv("PANTECH_API_URL"), "public API base URL (env PANTECH_API_URL)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	key := strings.TrimSpace(os.Getenv("PANTECH_API_KEY"))
	if key == "" {
		creds, err := cliconfig.Load()
		if err != nil {
			return fmt.Errorf("reading the Pantech CLI's sign-in: %w", err)
		}
		key = creds.Key
		if *apiURL == "" && creds.APIURL != "" {
			// An old CLI profile signed in on another API: its key works only there.
			*apiURL = creds.APIURL
		}
	}
	if key == "" {
		return errors.New("no Pantech API key. Run `pantech auth login` or set PANTECH_API_KEY")
	}
	if !mcp.ValidAPIKeyFormat(key) {
		return errors.New("the Pantech API key does not look like one (PAN_…). Run `pantech auth login` or set PANTECH_API_KEY")
	}
	if *apiURL != "" {
		// The key is sent there: never in clear text off this machine.
		if err := (&mcp.HTTPConfig{APIURL: *apiURL}).ValidateAPIURL(); err != nil {
			return err
		}
	}
	server := mcp.New(mcp.Options{
		APIURL: *apiURL,
		APIKey: key,
		// Random per process: tokens never outlive this server.
		Confirm: mcp.NewRandomConfirmer(),
		Logger:  newLogger(stderr),
	})
	err := server.Run(ctx, &sdk.StdioTransport{})
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func envBool(name string) bool {
	v, _ := strconv.ParseBool(os.Getenv(name))
	return v
}

func envInt(name string) int {
	v, _ := strconv.Atoi(os.Getenv(name))
	return v
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// envDefault is the variable's value when it is set, even to "", else def.
// Set but empty (a compose file's ${MCP_AUTHORIZATION_SERVER:-} with nothing
// in the env file) is a missing value that Validate refuses, not a cue to
// fall back to production.
func envDefault(name, def string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return def
}

func runHTTP(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("http", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", envOr("MCP_ADDR", ":8080"), "listen address (env MCP_ADDR)")
	publicURL := fs.String("public-url", os.Getenv("MCP_PUBLIC_URL"), "this server's public origin, e.g. https://mcp.pantechdynamics.com (env MCP_PUBLIC_URL)")
	authServer := fs.String("authorization-server", envDefault("MCP_AUTHORIZATION_SERVER", "https://console.pantechdynamics.com"), "OAuth authorization server (env MCP_AUTHORIZATION_SERVER)")
	apiURL := fs.String("api-url", envOr("PANTECH_API_URL", mcp.DefaultAPIURL), "public API base URL (env PANTECH_API_URL)")
	trustProxy := fs.Bool("trust-proxy", envBool("MCP_TRUST_PROXY"), "trust X-Forwarded-For, X-Real-IP and X-Request-Id from the reverse proxy, and send the caller's address to the API as X-Pantech-Client-IP (env MCP_TRUST_PROXY)")
	origins := fs.String("allowed-origins", os.Getenv("MCP_ALLOWED_ORIGINS"), "comma-separated browser origins allowed besides known AI apps (env MCP_ALLOWED_ORIGINS)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var allowed []string
	for _, o := range strings.Split(*origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			allowed = append(allowed, o)
		}
	}
	cfg := mcp.HTTPConfig{
		Addr:                  *addr,
		PublicURL:             *publicURL,
		AuthorizationServer:   *authServer,
		APIURL:                *apiURL,
		ConfirmSecret:         []byte(os.Getenv("MCP_CONFIRM_SECRET")),
		TrustProxy:            *trustProxy,
		AllowedOrigins:        allowed,
		RateLimitIP:           envInt("MCP_RATE_LIMIT_IP"),
		RateLimitToken:        envInt("MCP_RATE_LIMIT_TOKEN"),
		RateLimitAuthFailures: envInt("MCP_RATE_LIMIT_AUTH_FAILURES"),
		Logger:                newLogger(os.Stdout),
	}
	return mcp.Serve(ctx, cfg)
}
