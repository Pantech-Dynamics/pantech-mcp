package tools

import (
	"strings"
	"testing"
)

// The summary tells a model what part of a cut result it has: a command's
// output keeps its end, a file and a listing their start.
func TestToolCallSummaries(t *testing.T) {
	cases := []struct {
		tool, status, result, want string
	}{
		{"run", "completed", `{"exit_code":0,"truncated":true}`, "only its end is shown"},
		{"read_file", "completed", `{"truncated":true}`, "only its start was read"},
		{"list_dir", "completed", `{"truncated":true}`, "only the first are listed"},
		{"read_file", "completed", `{"truncated":false}`, "completed."},
		{"read_file", "failed", `{"reason":"Binary file"}`, "failed: the reason is in the data"},
	}
	for _, c := range cases {
		got := toolCallResult(&agentToolCall{ID: "call_1", SessionID: "ags_1", Tool: c.tool, Status: c.status, Result: []byte(c.result)}).Text
		if !strings.Contains(got, c.want) {
			t.Errorf("%s %s: %q, want it to say %q", c.tool, c.status, got, c.want)
		}
		if strings.Contains(got, "ran and failed") {
			t.Errorf("%s %s: %q claims the call ran", c.tool, c.status, got)
		}
	}
}

// A limit over 500 asks for 500, not the default.
func TestEventsLimit(t *testing.T) {
	for n, want := range map[int]int{0: 100, -1: 100, 1: 1, 250: 250, 500: 500, 501: 500, 10000: 500} {
		if got := eventsLimit(n); got != want {
			t.Errorf("eventsLimit(%d) = %d, want %d", n, got, want)
		}
	}
}
