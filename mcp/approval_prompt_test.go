package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const testRelayKey = "relay-key-0123456789abcdef0123456789"

// promptSession connects over http mode, as AI apps do, with an elicitation
// handler when answer is set. relayKey is the server's.
func promptSession(t *testing.T, apiURL, relayKey string, answer func(*sdk.ElicitParams) *sdk.ElicitResult) *sdk.ClientSession {
	t.Helper()
	cfg := testHTTPConfig(apiURL)
	cfg.PublicURL = "http://127.0.0.1"
	cfg.WaitBudget, cfg.PollInterval = 2*time.Second, 5*time.Millisecond
	cfg.ApprovalRelayKey = relayKey
	mcpSrv := httptest.NewServer(newTestHandler(t, cfg))
	t.Cleanup(mcpSrv.Close)
	opts := &sdk.ClientOptions{ProgressNotificationHandler: func(context.Context, *sdk.ProgressNotificationClientRequest) {}}
	if answer != nil {
		opts.ElicitationHandler = func(_ context.Context, r *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
			return answer(r.Params), nil
		}
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "1"}, opts).Connect(context.Background(),
		&sdk.StreamableClientTransport{Endpoint: mcpSrv.URL + "/mcp", HTTPClient: &http.Client{Transport: clientIPTransport{key: testKey}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// A step that waits for approval is shown to the person in their AI app, and
// their answer is sent on with the relay key (Pantech ADR 0156 amendment 16).
func TestAWaitingStepIsAskedInTheAIApp(t *testing.T) {
	for _, tc := range []struct {
		name     string
		relayKey string
		answer   *sdk.ElicitResult
		noPrompt bool
		decided  string // the decision sent, "" for none
		want     string
	}{
		{name: "approved", relayKey: testRelayKey, answer: &sdk.ElicitResult{Action: "accept", Content: map[string]any{"decision": "approve"}},
			decided: `"decision":"allow"`, want: "Call call_1 completed"},
		{name: "denied with a reason", relayKey: testRelayKey, answer: &sdk.ElicitResult{Action: "accept", Content: map[string]any{"decision": "deny", "reason": "not on prod"}},
			decided: `"reason":"not on prod"`, want: "Call call_1 did not run"},
		{name: "declined", relayKey: testRelayKey, answer: &sdk.ElicitResult{Action: "decline"}, decided: `"decision":"deny"`, want: "Call call_1 did not run"},
		{name: "dismissed", relayKey: testRelayKey, answer: &sdk.ElicitResult{Action: "cancel"}, want: "waiting for the user's approval"},
		{name: "no relay key", answer: &sdk.ElicitResult{Action: "accept", Content: map[string]any{"decision": "approve"}}, noPrompt: true, want: "waiting for the user's approval"},
		{name: "no prompts in the app", relayKey: testRelayKey, noPrompt: true, want: "waiting for the user's approval"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, srv := newFakeAPI(t)
			api.toolStatus = "waiting_approval"
			var mu sync.Mutex
			var asked []string
			var answer func(*sdk.ElicitParams) *sdk.ElicitResult
			if tc.answer != nil {
				answer = func(p *sdk.ElicitParams) *sdk.ElicitResult {
					mu.Lock()
					asked = append(asked, p.Message)
					mu.Unlock()
					return tc.answer
				}
			}
			cs := promptSession(t, srv.URL+apiPrefix, tc.relayKey, answer)
			_, text := call(t, cs, "run_in_instance", map[string]any{"session_id": "ags_1", "command": "systemctl restart nginx"})
			if !strings.Contains(text, tc.want) {
				t.Errorf("result %q, want %q", text, tc.want)
			}
			mu.Lock()
			defer mu.Unlock()
			if tc.noPrompt != (len(asked) == 0) {
				t.Fatalf("asked %d times, want a prompt: %v", len(asked), !tc.noPrompt)
			}
			if !tc.noPrompt && (len(asked) != 1 || !strings.Contains(asked[0], "systemctl restart nginx")) {
				t.Errorf("prompts %q, want one showing the command", asked)
			}
			if n := len(api.calls("POST", "/agent-sessions/ags_1/tool-calls")); n != 1 {
				t.Errorf("queued the step %d times, want once", n)
			}
			decisions := api.calls("POST", "/agent-sessions/ags_1/approvals/ap_1")
			if tc.decided == "" {
				if len(decisions) != 0 {
					t.Errorf("sent a decision nobody gave: %+v", decisions)
				}
				return
			}
			if len(decisions) != 1 || !strings.Contains(decisions[0].Body, tc.decided) || decisions[0].ApprovalRelay != testRelayKey {
				t.Errorf("decisions %+v, want one with %s and the relay key", decisions, tc.decided)
			}
		})
	}
}
