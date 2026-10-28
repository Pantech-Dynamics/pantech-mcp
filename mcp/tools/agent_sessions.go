package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// A tools session lets the AI app work inside one of the user's instances:
// run a command, read or write a file, list a directory. No model runs on
// the instance; the AI app decides each step. The instance's Pantech agent
// carries each step out as an unprivileged user, under the same checks as
// any agent step: platform policy, the session's rules, and, in an ask
// session, a person's approval in the console, which an AI app cannot give.
// Everything the instance reports was written by whatever ran there, so it
// is only ever data in a result.

type agentHost struct {
	Enrolled     bool    `json:"enrolled"`
	Connected    bool    `json:"connected"`
	TokenPending bool    `json:"token_pending"`
	EnrolledAt   *string `json:"enrolled_at"`
	LastSeenAt   *string `json:"last_seen_at"`
	AgentVersion string  `json:"agent_version"`
}

type agentSession struct {
	ID           string  `json:"id"`
	InstanceID   string  `json:"instance_id"`
	Title        string  `json:"title"`
	Kind         string  `json:"kind"`
	Mode         string  `json:"mode"`
	State        string  `json:"state"`
	DesiredState string  `json:"desired_state"`
	Connected    bool    `json:"connected"`
	LastSeq      int64   `json:"last_seq"`
	LastEventAt  *string `json:"last_event_at"`
	CreatedAt    string  `json:"created_at"`
	TerminatedAt *string `json:"terminated_at"`
	EndReason    *string `json:"end_reason"`
}

type agentToolCall struct {
	ID          string          `json:"id"`
	SessionID   string          `json:"session_id"`
	Tool        string          `json:"tool"`
	Status      string          `json:"status"`
	ApprovalID  *string         `json:"approval_id"`
	Result      json.RawMessage `json:"result"`
	CompletedAt *string         `json:"completed_at"`
}

type instanceAgentInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance's id, e.g. vm_…"`
}

type startToolsSessionInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance to work in. Its Pantech agent must be enabled (get_instance_agent)."`
	Title      string `json:"title" jsonschema:"A short title for the session, shown to the user in the console, e.g. 'Fix the nginx config'."`
	Mode       string `json:"mode" jsonschema:"plan: only read_instance_file and list_instance_dir. ask: also run_in_instance and write_instance_file; anything outside a safe list waits for the user's approval in the console."`
}

type listAgentSessionsInput struct {
	InstanceID string `json:"instance_id,omitempty" jsonschema:"Only this instance's sessions."`
}

type agentSessionInput struct {
	SessionID string `json:"session_id" jsonschema:"The session's id, e.g. ags_…, from start_tools_session or list_agent_sessions."`
}

type runInInstanceInput struct {
	SessionID      string `json:"session_id" jsonschema:"A tools session, from start_tools_session."`
	Command        string `json:"command" jsonschema:"A bash command, run as the session's unprivileged user (sudo only where the session allows it). At most 8000 characters."`
	Cwd            string `json:"cwd,omitempty" jsonschema:"An absolute directory to run in. Default: the session's own work directory."`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"1 to 600. Default 120. The command is killed after it."`
}

type instanceFileInput struct {
	SessionID string `json:"session_id" jsonschema:"A tools session, from start_tools_session."`
	Path      string `json:"path" jsonschema:"An absolute path on the instance."`
}

type writeInstanceFileInput struct {
	SessionID string `json:"session_id" jsonschema:"A tools session, from start_tools_session."`
	Path      string `json:"path" jsonschema:"An absolute path on the instance. An existing file is replaced."`
	Content   string `json:"content" jsonschema:"The whole file, UTF-8, at most 32 KiB."`
}

type agentToolCallInput struct {
	SessionID string `json:"session_id" jsonschema:"The session's id."`
	CallID    string `json:"call_id" jsonschema:"The call's id, e.g. call_…, from run_in_instance or another instance tool."`
}

type agentEventsInput struct {
	SessionID string `json:"session_id" jsonschema:"The session's id."`
	After     int64  `json:"after,omitempty" jsonschema:"Only events after this seq; pass the last next_after to follow the session."`
	Limit     int    `json:"limit,omitempty" jsonschema:"At most this many events, 1 to 500. Default 100."`
}

const toolCallRoutes = "POST /agent-sessions/{session_id}/tool-calls"
const toolCallGetRoute = "GET /agent-sessions/{session_id}/tool-calls/{call_id}"

// sessionGetRoute is also read by the step tools, to say when a queued step's agent is offline.
const sessionGetRoute = "GET /agent-sessions/{session_id}"

func init() {
	add(Spec{
		Name: "get_instance_agent", Title: "Check an instance's Pantech agent",
		Description: "Whether the instance's Pantech agent is enabled and connected, which a tools session needs. If it is not set up, enable_instance_agent sets it up (the instance restarts once).",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}/agent"},
	}, func(ctx context.Context, c *Call, in instanceAgentInput) (*Result, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		var h agentHost
		if err := c.Client.Do(ctx, "GET", "/instances/"+in.InstanceID+"/agent", nil, nil, &h); err != nil {
			return nil, err
		}
		switch {
		case h.Enrolled && h.Connected:
			return ok(fmt.Sprintf("Instance %s's agent is enabled and connected: start_tools_session can work there.", in.InstanceID), h)
		case h.Enrolled:
			return ok(fmt.Sprintf("Instance %s's agent is enabled but not connected right now: the instance may be stopped or still starting.", in.InstanceID), h)
		case h.TokenPending:
			return ok(fmt.Sprintf("Instance %s's agent is being set up and has not connected yet: it connects within a few minutes of the instance starting. Check again shortly.", in.InstanceID), h)
		}
		return ok(fmt.Sprintf("Instance %s's agent is not enabled. enable_instance_agent sets it up (the instance restarts once, after the user confirms).", in.InstanceID), h)
	})

	add(Spec{
		Name: "start_tools_session", Title: "Start working in an instance",
		Description: "Start a tools session on an instance, so run_in_instance, read_instance_file, write_instance_file and list_instance_dir can work there. No model runs on the instance: this AI app decides each step. Every step runs as an unprivileged user, and in ask mode anything outside a safe list waits for the user to approve it in the console. The user sees the session and every step in the console, and can end it at any time.",
		Risk:        Write,
		Routes:      []string{"POST /instances/{instance_id}/agent-sessions"},
	}, func(ctx context.Context, c *Call, in startToolsSessionInput) (*Result, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		if in.Mode != "plan" && in.Mode != "ask" {
			return nil, errorf("mode must be plan or ask.")
		}
		body := map[string]any{"title": in.Title, "kind": "tools", "mode": in.Mode}
		var s agentSession
		if err := c.Client.Do(ctx, "POST", "/instances/"+in.InstanceID+"/agent-sessions", nil, body, &s, c.writeOpts()...); err != nil {
			return nil, err
		}
		pickup := "Its agent picks it up within seconds"
		if !s.Connected {
			pickup = "Its agent is not connected right now, so the session and its steps wait until it is (" + offlineCauses + ")"
		}
		return ok(fmt.Sprintf("Started tools session %s on instance %s in %s mode. %s; use run_in_instance and the other instance tools with session_id %s. The session ends after a while with no calls, or with end_agent_session.",
			arg(s.ID), in.InstanceID, in.Mode, pickup, arg(s.ID)), s)
	})

	add(Spec{
		Name: "list_agent_sessions", Title: "List agent sessions",
		Description: "List the organization's agent sessions, newest first (at most 100): tools sessions started by AI apps, and agent sessions started in the console.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /agent-sessions"},
	}, func(ctx context.Context, c *Call, in listAgentSessionsInput) (*Result, error) {
		if err := checkOptionalIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		var q url.Values
		if in.InstanceID != "" {
			q = url.Values{"instance_id": {in.InstanceID}}
		}
		var page struct {
			Data []agentSession `json:"data"`
		}
		if err := c.Client.Do(ctx, "GET", "/agent-sessions", q, nil, &page); err != nil {
			return nil, err
		}
		return ok(plural(len(page.Data), "agent session", "agent sessions")+".", page.Data)
	})

	add(Spec{
		Name: "get_agent_session", Title: "Get an agent session",
		Description: "Get an agent session: its kind, mode, state (starting, running, waiting_approval, idle, terminating, ended, failed, or unknown when the instance's agent has not been heard from) and whether its agent is connected.",
		Risk:        Read, Idempotent: true,
		Routes: []string{sessionGetRoute},
	}, func(ctx context.Context, c *Call, in agentSessionInput) (*Result, error) {
		if err := checkIDs("session_id", in.SessionID); err != nil {
			return nil, err
		}
		var s agentSession
		if err := c.Client.Do(ctx, "GET", "/agent-sessions/"+in.SessionID, nil, nil, &s); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Agent session %s is %s.", in.SessionID, arg(s.State)), s)
	})

	add(Spec{
		Name: "run_in_instance", Title: "Run a command in an instance",
		Description: "Run a bash command on the instance of a tools session, as the session's unprivileged user, and wait for it (up to about a minute; longer commands keep running: follow them with get_agent_tool_call). Returns the exit code and the last 16 KiB of stdout and stderr. In an ask session a command outside the safe list waits for the user's approval in the console. Not in a plan session.",
		Risk:        Write,
		Routes:      []string{toolCallRoutes, toolCallGetRoute, sessionGetRoute},
	}, func(ctx context.Context, c *Call, in runInInstanceInput) (*Result, error) {
		if in.TimeoutSeconds < 0 || in.TimeoutSeconds > 600 {
			return nil, errorf("timeout_seconds must be 1 to 600.")
		}
		body := map[string]any{"tool": "run", "command": in.Command}
		if in.Cwd != "" {
			body["cwd"] = in.Cwd
		}
		if in.TimeoutSeconds > 0 {
			body["timeout_seconds"] = in.TimeoutSeconds
		}
		return c.callInstanceTool(ctx, in.SessionID, body)
	})

	add(Spec{
		Name: "read_instance_file", Title: "Read a file on an instance",
		Description: "Read a file on the instance of a tools session, as the session's user: its first 32 KiB and its size. Works in plan and ask sessions.",
		Risk:        Write,
		Routes:      []string{toolCallRoutes, toolCallGetRoute, sessionGetRoute},
	}, func(ctx context.Context, c *Call, in instanceFileInput) (*Result, error) {
		return c.callInstanceTool(ctx, in.SessionID, map[string]any{"tool": "read_file", "path": in.Path})
	})

	add(Spec{
		Name: "list_instance_dir", Title: "List a directory on an instance",
		Description: "List a directory on the instance of a tools session, as the session's user: names, types and sizes, at most 500 entries. Works in plan and ask sessions.",
		Risk:        Write,
		Routes:      []string{toolCallRoutes, toolCallGetRoute, sessionGetRoute},
	}, func(ctx context.Context, c *Call, in instanceFileInput) (*Result, error) {
		return c.callInstanceTool(ctx, in.SessionID, map[string]any{"tool": "list_dir", "path": in.Path})
	})

	add(Spec{
		Name: "write_instance_file", Title: "Write a file on an instance",
		Description: "Write a whole file on the instance of a tools session, as the session's user, replacing it if it exists. In an ask session it waits for the user's approval in the console unless a session rule allows it. Not in a plan session.",
		Risk:        Write,
		Routes:      []string{toolCallRoutes, toolCallGetRoute, sessionGetRoute},
	}, func(ctx context.Context, c *Call, in writeInstanceFileInput) (*Result, error) {
		return c.callInstanceTool(ctx, in.SessionID, map[string]any{"tool": "write_file", "path": in.Path, "content": in.Content})
	})

	add(Spec{
		Name: "get_agent_tool_call", Title: "Get a step in an instance",
		Description: "Get a step a tools session was asked to carry out: queued, running, waiting_approval (the user must approve or deny it in the console), completed, failed, denied, blocked or timed_out, and its result once it finished.",
		Risk:        Read, Idempotent: true,
		Routes: []string{toolCallGetRoute, sessionGetRoute},
	}, func(ctx context.Context, c *Call, in agentToolCallInput) (*Result, error) {
		if err := checkIDs("session_id", in.SessionID, "call_id", in.CallID); err != nil {
			return nil, err
		}
		var tc agentToolCall
		if err := c.Client.Do(ctx, "GET", "/agent-sessions/"+in.SessionID+"/tool-calls/"+in.CallID, nil, nil, &tc); err != nil {
			return nil, err
		}
		res := toolCallResult(&tc)
		c.noteOffline(ctx, res, &tc)
		return res, nil
	})

	add(Spec{
		Name: "list_agent_session_events", Title: "List an agent session's events",
		Description: "The session's record, oldest first: every step, approval and result, as the user sees it in the console. Pass after (the last next_after) to read only what is new.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /agent-sessions/{session_id}/events"},
	}, func(ctx context.Context, c *Call, in agentEventsInput) (*Result, error) {
		if err := checkIDs("session_id", in.SessionID); err != nil {
			return nil, err
		}
		q := url.Values{"limit": {fmt.Sprint(eventsLimit(in.Limit))}}
		if in.After > 0 {
			q.Set("after", fmt.Sprint(in.After))
		}
		var page struct {
			Data      []json.RawMessage `json:"data"`
			NextAfter int64             `json:"next_after"`
		}
		if err := c.Client.Do(ctx, "GET", "/agent-sessions/"+in.SessionID+"/events", q, nil, &page); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("%s. Pass after=%d to read what comes next.", plural(len(page.Data), "event", "events"), page.NextAfter),
			map[string]any{"events": page.Data, "next_after": page.NextAfter})
	})

	add(Spec{
		Name: "end_agent_session", Title: "End an agent session",
		Description: "End an agent session: its agent stops whatever is running for it. What it already changed on the instance stays.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"POST /agent-sessions/{session_id}/terminate"},
	}, func(ctx context.Context, c *Call, in agentSessionInput) (*Result, error) {
		if err := checkIDs("session_id", in.SessionID); err != nil {
			return nil, err
		}
		var s agentSession
		if err := c.Client.Do(ctx, "POST", "/agent-sessions/"+in.SessionID+"/terminate", nil, nil, &s, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Agent session %s is %s.", in.SessionID, arg(s.State)), s)
	})

	add(Spec{
		Name: "get_agent_session_report", Title: "Get an agent session's report",
		Description: "What a session did: how long it ran, the commands run and files changed, approvals asked, allowed, denied and blocked, and errors.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /agent-sessions/{session_id}/report"},
	}, func(ctx context.Context, c *Call, in agentSessionInput) (*Result, error) {
		if err := checkIDs("session_id", in.SessionID); err != nil {
			return nil, err
		}
		var report json.RawMessage
		if err := c.Client.Do(ctx, "GET", "/agent-sessions/"+in.SessionID+"/report", nil, nil, &report); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Report for agent session %s.", in.SessionID), report)
	})
}

// callInstanceTool queues one step and waits for it within the wait budget,
// with progress. A step that needs a person's approval is returned at once:
// waiting longer only uses up the call.
func (c *Call) callInstanceTool(ctx context.Context, sessionID string, body map[string]any) (*Result, error) {
	if err := checkIDs("session_id", sessionID); err != nil {
		return nil, err
	}
	var tc agentToolCall
	if err := c.Client.Do(ctx, "POST", "/agent-sessions/"+sessionID+"/tool-calls", nil, body, &tc, c.writeOpts()...); err != nil {
		return nil, err
	}
	if checkIDs("call_id", tc.ID) != nil {
		return nil, errorf("Pantech accepted the step but did not say which call it is: check list_agent_session_events.")
	}
	deadline := time.Now().Add(c.env.waitBudget())
	p := newProgress(c.Req)
	for !finishedToolCall(tc.Status) && tc.Status != "waiting_approval" {
		p.send(ctx, fmt.Sprintf("call %s is %s", tc.ID, tc.Status))
		if time.Now().Add(c.env.pollInterval()).After(deadline) || sleep(ctx, c.env.pollInterval()) != nil {
			break
		}
		next := agentToolCall{}
		if err := c.Client.Do(ctx, "GET", "/agent-sessions/"+sessionID+"/tool-calls/"+tc.ID, nil, nil, &next); err != nil {
			break // it was queued: say so below rather than invite a retry
		}
		tc = next
	}
	res := toolCallResult(&tc)
	c.noteOffline(ctx, res, &tc)
	return res, nil
}

// offlineCauses is why an instance's agent may not be connected.
const offlineCauses = "the instance may be stopped, still starting, or unreachable: get_instance_agent shows when it last checked in"

// noteOffline adds to a step still queued that the session's agent is not
// connected, when it is not: otherwise "queued" reads as "about to run".
func (c *Call) noteOffline(ctx context.Context, res *Result, tc *agentToolCall) {
	if tc.Status != "queued" {
		return
	}
	var s agentSession
	if err := c.Client.Do(ctx, "GET", "/agent-sessions/"+tc.SessionID, nil, nil, &s); err != nil || s.Connected {
		return
	}
	res.Text += " The instance's agent is not connected right now, so it runs once the agent is back (" + offlineCauses + ")."
}

func finishedToolCall(status string) bool {
	switch status {
	case "completed", "failed", "denied", "blocked", "timed_out":
		return true
	}
	return false
}

// toolCallResult says where a step is in our own words. Its result, written
// by whatever ran on the instance, only goes in the fenced data.
func toolCallResult(tc *agentToolCall) *Result {
	id := arg(tc.ID)
	follow := fmt.Sprintf(" Follow it with get_agent_tool_call (session_id %s, call_id %s).", arg(tc.SessionID), id)
	var text string
	switch tc.Status {
	case "completed":
		text = fmt.Sprintf("Call %s completed.", id)
		var r struct {
			ExitCode  *int `json:"exit_code"`
			Truncated bool `json:"truncated"`
		}
		if json.Unmarshal(tc.Result, &r) == nil {
			if r.ExitCode != nil {
				text = fmt.Sprintf("Call %s completed with exit code %d.", id, *r.ExitCode)
			}
			if r.Truncated {
				// A command's output keeps its end; a file and a listing keep their start.
				switch tc.Tool {
				case "read_file":
					text += " The file is longer than shown; only its start was read."
				case "list_dir":
					text += " The folder has more entries than shown; only the first are listed."
				default:
					text += " The output was cut; only its end is shown."
				}
			}
		}
	case "failed":
		// It may not have run at all: a binary file, or a session that ended first.
		text = fmt.Sprintf("Call %s failed: the reason is in the data.", id)
	case "timed_out":
		text = fmt.Sprintf("Call %s ran out of time and was stopped.", id)
	case "denied":
		// Denied by the user, or closed because nobody answered within 15
		// minutes: the reason, written on the instance, is in the data.
		text = fmt.Sprintf("Call %s did not run: the user denied it, or nobody answered it in time (the reason is in the data). Do not repeat it; ask them what they want instead.", id)
	case "blocked":
		text = fmt.Sprintf("Call %s was refused without asking: platform policy does not allow it, or the session's mode does not.", id)
	case "waiting_approval":
		text = fmt.Sprintf("Call %s is waiting for the user's approval. Ask them to approve or deny it in the Pantech console, under this session; an AI app cannot approve its own steps.", id) + follow
	default:
		text = fmt.Sprintf("Call %s is %s on the instance.", id, arg(tc.Status)) + follow
	}
	return &Result{Text: text, Data: slimToolCall(tc)}
}

// slimToolCall is the call as the API returned it, less what its result
// repeats: output is the console's few-line preview of stdout, content or
// entries, which are there in full, and the result's own copies of the
// call's id, tool and status (kept when they say something the call does not).
func slimToolCall(tc *agentToolCall) *agentToolCall {
	var r map[string]json.RawMessage
	if len(tc.Result) == 0 || json.Unmarshal(tc.Result, &r) != nil {
		return tc
	}
	for _, full := range []string{"stdout", "content", "entries", "bytes"} {
		if _, ok := r[full]; ok {
			delete(r, "output")
			break
		}
	}
	for _, repeated := range []string{"call_id", "tool_use_id", "is_error"} {
		delete(r, repeated)
	}
	for field, same := range map[string]string{"tool": tc.Tool, "status": tc.Status} {
		var v string
		if json.Unmarshal(r[field], &v) == nil && v == same {
			delete(r, field)
		}
	}
	slim, err := json.Marshal(r)
	if err != nil {
		return tc
	}
	out := *tc
	out.Result = slim
	return &out
}

// eventsLimit is how many events to ask for: 100 when unset, and at most 500,
// so a larger limit still gets the most it can, like listLimit.
func eventsLimit(n int) int {
	switch {
	case n <= 0:
		return 100
	case n > 500:
		return 500
	}
	return n
}
