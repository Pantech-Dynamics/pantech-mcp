package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestStdioWithoutKey(t *testing.T) {
	t.Setenv("PANTECH_API_KEY", "")
	t.Setenv("PANTECH_CONFIG_DIR", t.TempDir())
	t.Setenv("PANTECH_NO_KEYRING", "1")
	var out, errOut bytes.Buffer
	if code := run([]string{"stdio"}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errOut.String(), "Run `pantech auth login` or set PANTECH_API_KEY") {
		t.Fatalf("message: %s", errOut.String())
	}
}

func TestHTTPRefusesToStartWithoutSecret(t *testing.T) {
	t.Setenv("MCP_CONFIRM_SECRET", "")
	t.Setenv("MCP_PUBLIC_URL", "https://mcp.example.test")
	var out, errOut bytes.Buffer
	if code := run([]string{"http", "--addr", "127.0.0.1:0"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "MCP_CONFIRM_SECRET") {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	t.Setenv("MCP_CONFIRM_SECRET", strings.Repeat("s", 40))
	t.Setenv("MCP_PUBLIC_URL", "http://mcp.example.test")
	errOut.Reset()
	if code := run([]string{"http", "--addr", "127.0.0.1:0"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "https") {
		t.Fatalf("http public URL: exit %d: %s", code, errOut.String())
	}
}

func TestHTTPRefusesAnEmptyAuthorizationServer(t *testing.T) {
	t.Setenv("MCP_CONFIRM_SECRET", strings.Repeat("s", 40))
	t.Setenv("MCP_PUBLIC_URL", "https://mcp.example.test")
	// Set but empty, as compose passes ${MCP_AUTHORIZATION_SERVER:-}: no
	// silent fallback to the production console.
	t.Setenv("MCP_AUTHORIZATION_SERVER", "")
	var out, errOut bytes.Buffer
	if code := run([]string{"http", "--addr", "127.0.0.1:0"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "MCP_AUTHORIZATION_SERVER") {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
}

func TestStdioRefusesACleartextAPI(t *testing.T) {
	t.Setenv("PANTECH_API_KEY", "PAN_abcdefghijklmnopqrstuvwxyz_0123")
	t.Setenv("PANTECH_API_URL", "http://api.example.test/public/v1")
	var out, errOut bytes.Buffer
	if code := run([]string{"stdio"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "https") {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
}

func TestVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), "pantech-mcp ") {
		t.Fatalf("%d %q", code, out.String())
	}
}
