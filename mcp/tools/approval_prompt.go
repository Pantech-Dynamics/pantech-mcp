package tools

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	pantech "github.com/Pantech-Dynamics/pantech-go"
	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ApprovalRelayHeader carries Env.ApprovalRelayKey to the API with a decision
// the person gave in their AI app's own prompt. The API accepts that decision
// from an AI app's key only with it (Pantech ADR 0156 amendment 16).
const ApprovalRelayHeader = "X-Pantech-Approval-Relay"

const approvalDecideRoute = "POST /agent-sessions/{session_id}/approvals/{approval_id}"

// approvalInput is the one input request a waiting step asks for.
const approvalInput = "approval"

// promptProtocol is the first protocol version whose clients answer an input
// request in the result: this server is stateless over HTTP, so it cannot ask
// a client on an older version anything mid-call.
const promptProtocol = "2026-07-28"

// pendingApproval is what a prompt's request state names: the step, and the
// caller it was shown to.
type pendingApproval struct {
	SessionID  string `json:"s"`
	CallID     string `json:"c"`
	ApprovalID string `json:"a"`
	Subject    string `json:"u"`
}

// canPrompt reports whether this call may show a waiting step to the person in
// their AI app: the server holds the relay key and the client answers form
// input requests.
func (c *Call) canPrompt() bool {
	if c.env.ApprovalRelayKey == "" || c.Req == nil || c.Req.Session == nil {
		return false
	}
	p := c.Req.Session.InitializeParams()
	if p == nil || p.ProtocolVersion < promptProtocol || p.Capabilities == nil || p.Capabilities.Elicitation == nil {
		return false
	}
	e := p.Capabilities.Elicitation
	// An empty elicitation capability means form prompts.
	return e.Form != nil || e.URL == nil
}

// promptApproval returns the input request that asks the person, in their AI
// app, to approve or deny a waiting step. what says what the step does.
func (c *Call) promptApproval(tc *agentToolCall, what string) *Result {
	state, err := c.signState(pendingApproval{SessionID: tc.SessionID, CallID: tc.ID, ApprovalID: *tc.ApprovalID, Subject: c.subject})
	if err != nil {
		return nil
	}
	message := "Your AI app wants to " + what + "\n\nPantech runs it only if you approve. Deny to stop it; the AI app is told."
	return &Result{
		Ask: &sdk.ElicitParams{
			Message: message,
			RequestedSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"decision": {Type: "string", Title: "Your decision", Enum: []any{"approve", "deny"}},
					"reason":   {Type: "string", Title: "Reason (optional)", Description: "Told to the AI app with a denial.", MaxLength: ptr(500)},
				},
				Required: []string{"decision"},
			},
		},
		State: state,
	}
}

// answerPrompt is a retried call carrying the person's answer: it sends the
// decision and waits for the step like a first call would. ok is false when
// the call carries no answer.
func (c *Call) answerPrompt(ctx context.Context) (*Result, bool, error) {
	if c.Req == nil || c.Req.Params == nil || len(c.Req.Params.InputResponses) == 0 {
		return nil, false, nil
	}
	pending, err := c.verifyState(c.Req.Params.RequestState)
	if err != nil {
		return nil, true, errorf("The answer to the approval prompt could not be matched to its step. Ask the user to approve or deny it in the Pantech console, under the session.")
	}
	answer, _ := c.Req.Params.InputResponses[approvalInput].(*sdk.ElicitResult)
	tc := agentToolCall{ID: pending.CallID, SessionID: pending.SessionID, Status: "waiting_approval", ApprovalID: &pending.ApprovalID}
	decision, reason := "", ""
	switch {
	case answer == nil || answer.Action == "cancel":
		// Dismissed: the step still waits, for the console.
		return toolCallResult(&tc), true, nil
	case answer.Action == "decline":
		decision = "deny"
	default:
		d, _ := answer.Content["decision"].(string)
		reason, _ = answer.Content["reason"].(string)
		switch d {
		case "approve":
			decision = "allow"
		case "deny":
			decision = "deny"
		default:
			return toolCallResult(&tc), true, nil
		}
	}
	body := map[string]any{"decision": decision, "scope": "once"}
	if reason = strings.TrimSpace(reason); reason != "" && decision == "deny" {
		body["reason"] = reason
	}
	path := "/agent-sessions/" + pending.SessionID + "/approvals/" + pending.ApprovalID
	if err := c.Client.Do(ctx, "POST", path, nil, body, nil, pantech.WithRequestHeader(ApprovalRelayHeader, c.env.ApprovalRelayKey)); err != nil {
		return nil, true, err
	}
	res, err := c.followToolCall(ctx, &tc)
	return res, true, err
}

// followToolCall polls a step until it finishes or the wait budget is spent.
func (c *Call) followToolCall(ctx context.Context, tc *agentToolCall) (*Result, error) {
	deadline := time.Now().Add(c.env.waitBudget())
	p := newProgress(c.Req)
	for {
		next := agentToolCall{}
		if err := c.Client.Do(ctx, "GET", "/agent-sessions/"+tc.SessionID+"/tool-calls/"+tc.ID, nil, nil, &next); err != nil {
			break
		}
		*tc = next
		if finishedToolCall(tc.Status) {
			break
		}
		p.send(ctx, fmt.Sprintf("call %s is %s", tc.ID, tc.Status))
		if time.Now().Add(c.env.pollInterval()).After(deadline) || sleep(ctx, c.env.pollInterval()) != nil {
			break
		}
	}
	res := toolCallResult(tc)
	c.noteOffline(ctx, res, tc)
	return res, nil
}

// signState is p, signed with the relay key so a retry names only a step this
// server showed this caller.
func (c *Call) signState(p pendingApproval) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	return body + "." + c.stateMAC(body), nil
}

func (c *Call) verifyState(state string) (pendingApproval, error) {
	var p pendingApproval
	body, mac, found := strings.Cut(state, ".")
	if !found || !hmac.Equal([]byte(mac), []byte(c.stateMAC(body))) {
		return p, fmt.Errorf("request state: bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || json.Unmarshal(raw, &p) != nil || p.Subject != c.subject ||
		checkIDs("session_id", p.SessionID) != nil || checkIDs("call_id", p.CallID) != nil || p.ApprovalID == "" {
		return pendingApproval{}, fmt.Errorf("request state: not this caller's step")
	}
	return p, nil
}

func (c *Call) stateMAC(body string) string {
	m := hmac.New(sha256.New, []byte("pantech-mcp approval state\x00"+c.env.ApprovalRelayKey))
	m.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func ptr[T any](v T) *T { return &v }
