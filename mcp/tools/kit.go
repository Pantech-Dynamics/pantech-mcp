// Package tools defines every tool the Pantech MCP server offers, by area:
// one file per resource family, all registered through add and addConfirmed
// in this file, which own the safety rules every tool shares.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	pantech "github.com/Pantech-Dynamics/pantech-go"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Risk says what a tool can do to the account. Cost, Exposure and
// Destructive tools are two-step: they need a confirmation token.
type Risk int

const (
	// Read only reads.
	Read Risk = iota
	// Write changes something without cost, exposure or data loss
	// (start, stop, rename, attach, detach…).
	Write
	// Cost creates or grows something that is billed.
	Cost
	// Exposure opens a path from the internet to the customer's servers.
	Exposure
	// Destructive deletes something or overwrites data or rules.
	Destructive
)

func (r Risk) String() string {
	return [...]string{"read", "write", "cost", "exposure", "destructive"}[r]
}

// NeedsConfirmation reports whether a tool of this risk is two-step.
func (r Risk) NeedsConfirmation() bool { return r >= Cost }

// Spec describes a tool.
type Spec struct {
	Name        string
	Title       string
	Description string
	Risk        Risk
	// Destructive is the destructiveHint: the tool may delete or overwrite.
	Destructive bool
	// Idempotent is the idempotentHint: repeating the call with the same
	// arguments has no further effect.
	Idempotent bool
	// Routes are the API routes the tool may call, as "METHOD /path" with
	// {placeholders}. TestNoExcludedRoutes checks them, and the end-to-end
	// test checks that every route a tool calls is listed.
	Routes []string
}

// Tool is a registered tool: its spec and how to install it on a server.
type Tool struct {
	Spec
	install func(*sdk.Server, *Env)
}

var registry []Tool

// All returns every tool, sorted by name.
func All() []Tool {
	out := slices.Clone(registry)
	slices.SortFunc(out, func(a, b Tool) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Register installs every tool on s.
func Register(s *sdk.Server, env *Env) {
	for _, t := range All() {
		t.install(s, env)
	}
}

// Confirmer issues and checks confirmation tokens. The mcp package provides
// the HMAC implementation.
type Confirmer interface {
	// Issue returns a token for this tool, these canonical arguments and this
	// caller, and when it expires.
	Issue(tool string, args []byte, subject string) (token string, expires time.Time)
	// Verify returns nil only for a live token issued for exactly this tool,
	// these arguments and this caller.
	Verify(token, tool string, args []byte, subject string) error
	// IdempotencyKey derives the Idempotency-Key a confirmed call sends.
	IdempotencyKey(token string) string
}

// Env is what tools need from the server.
type Env struct {
	// Client returns the API client for this call, and the caller's subject
	// (a hash of their key) that confirmation tokens are bound to.
	Client  func(ctx context.Context, req *sdk.CallToolRequest) (*pantech.Client, string, error)
	Confirm Confirmer
	// WaitBudget bounds how long a tool waits for a change to finish
	// before returning its operation id. Default 60s.
	WaitBudget time.Duration
	// PollInterval is the pause between polls. Default 2s.
	PollInterval time.Duration
	// CallTimeout bounds a whole tool call. Default 110s.
	CallTimeout time.Duration
	Logger      *slog.Logger
}

func (e *Env) waitBudget() time.Duration {
	if e.WaitBudget > 0 {
		return e.WaitBudget
	}
	return 60 * time.Second
}

func (e *Env) pollInterval() time.Duration {
	if e.PollInterval > 0 {
		return e.PollInterval
	}
	return 2 * time.Second
}

func (e *Env) callTimeout() time.Duration {
	if e.CallTimeout > 0 {
		return e.CallTimeout
	}
	return 110 * time.Second
}

// Call is one tool call in progress.
type Call struct {
	Client *pantech.Client
	Req    *sdk.CallToolRequest
	env    *Env
	tool   string
	// idemKey is set on the confirmed step of a two-step tool.
	idemKey string
}

// writeOpts are the request options a write sends: the Idempotency-Key
// derived from the confirmation token, when there is one. Other writes get
// the SDK's random key, reused across its retries.
func (c *Call) writeOpts() []pantech.RequestOption {
	if c.idemKey == "" {
		return nil
	}
	return []pantech.RequestOption{pantech.WithIdempotencyKey(c.idemKey)}
}

// Record is what the server's log line says about a call. The mcp package
// puts one in the context; tools fill it in.
type Record struct {
	// Phase is "preview", "confirmed" or "refused" for two-step tools.
	Phase string
	// Code is the API's problem code when the call failed.
	Code string
	// APIRequestID is the API's request id when the call failed.
	APIRequestID string
}

type recordKey struct{}

// WithRecord returns ctx carrying r.
func WithRecord(ctx context.Context, r *Record) context.Context {
	return context.WithValue(ctx, recordKey{}, r)
}

func record(ctx context.Context) *Record {
	if r, ok := ctx.Value(recordKey{}).(*Record); ok {
		return r
	}
	return &Record{}
}

// Confirmation is embedded in the input of every two-step tool.
type Confirmation struct {
	ConfirmationToken string `json:"confirmation_token,omitempty" jsonschema:"Leave this out on the first call: it returns a preview and a confirmation_token and changes nothing. Show the preview to the user, and only after they agree call again with exactly the same arguments plus this token. Tokens expire after 5 minutes."`
}

func (c Confirmation) token() string { return c.ConfirmationToken }

type confirmable interface{ token() string }

// Preview is what the first step of a two-step tool shows.
type Preview struct {
	// Action says what will happen, in one sentence of our own words
	// (ids are fine; customer-chosen names go in Data).
	Action string
	// Price is the cost, when the API can quote one.
	Price string
	// Warnings are extra lines the user should read before agreeing.
	Warnings []string
	// Data is the account data the preview is based on (current state,
	// the request), shown fenced as untrusted.
	Data any
}

func annotations(s Spec) *sdk.ToolAnnotations {
	f := false
	a := &sdk.ToolAnnotations{
		Title:          s.Title,
		ReadOnlyHint:   s.Risk == Read,
		IdempotentHint: s.Idempotent,
		OpenWorldHint:  &f,
	}
	if s.Risk != Read {
		d := s.Destructive
		a.DestructiveHint = &d
	}
	return a
}

const twoStepNote = "\n\nTwo steps: the first call changes nothing and returns a preview (with the price where the API quotes one) and a confirmation_token. Show the preview to the user; only after they agree, call again with exactly the same arguments plus confirmation_token."

func describe(s Spec) string {
	d := s.Description
	if s.Risk.NeedsConfirmation() {
		d += twoStepNote
	}
	return d
}

// add registers a tool that runs in one step.
func add[In any](spec Spec, run func(ctx context.Context, c *Call, in In) (*Result, error)) {
	if spec.Risk.NeedsConfirmation() {
		panic("tools: " + spec.Name + " needs confirmation: register it with addConfirmed")
	}
	registry = append(registry, Tool{Spec: spec, install: func(s *sdk.Server, env *Env) {
		sdk.AddTool(s, &sdk.Tool{Name: spec.Name, Title: spec.Title, Description: describe(spec), Annotations: annotations(spec)},
			func(ctx context.Context, req *sdk.CallToolRequest, in In) (*sdk.CallToolResult, any, error) {
				ctx, cancel := context.WithTimeout(ctx, env.callTimeout())
				defer cancel()
				c, _, err := newCall(ctx, env, spec.Name, req)
				if err != nil {
					return toolError(ctx, err).render()
				}
				res, err := run(ctx, c, in)
				if err != nil {
					return toolError(ctx, err).render()
				}
				return res.render()
			})
	}})
}

// addConfirmed registers a two-step tool: without a token it previews and
// issues one; with a valid token for exactly these arguments and this
// caller it executes, with an Idempotency-Key derived from the token so a
// retried confirmed call never makes the change twice.
func addConfirmed[In confirmable](spec Spec,
	preview func(ctx context.Context, c *Call, in In) (*Preview, error),
	execute func(ctx context.Context, c *Call, in In) (*Result, error),
) {
	if !spec.Risk.NeedsConfirmation() {
		panic("tools: " + spec.Name + " does not need confirmation: register it with add")
	}
	registry = append(registry, Tool{Spec: spec, install: func(s *sdk.Server, env *Env) {
		sdk.AddTool(s, &sdk.Tool{Name: spec.Name, Title: spec.Title, Description: describe(spec), Annotations: annotations(spec)},
			func(ctx context.Context, req *sdk.CallToolRequest, in In) (*sdk.CallToolResult, any, error) {
				ctx, cancel := context.WithTimeout(ctx, env.callTimeout())
				defer cancel()
				c, subject, err := newCall(ctx, env, spec.Name, req)
				if err != nil {
					return toolError(ctx, err).render()
				}
				args, err := CanonicalArgs(in)
				if err != nil {
					return toolError(ctx, err).render()
				}
				token := in.token()
				rec := record(ctx)
				if token == "" {
					rec.Phase = "preview"
					p, err := preview(ctx, c, in)
					if err != nil {
						return toolError(ctx, err).render()
					}
					tok, exp := env.Confirm.Issue(spec.Name, args, subject)
					return previewResult(spec, p, tok, exp).render()
				}
				if err := env.Confirm.Verify(token, spec.Name, args, subject); err != nil {
					rec.Phase = "refused"
					return refusedResult(spec).render()
				}
				rec.Phase = "confirmed"
				c.idemKey = env.Confirm.IdempotencyKey(token)
				res, err := execute(ctx, c, in)
				if err != nil {
					return toolError(ctx, err).render()
				}
				return res.render()
			})
	}})
}

func newCall(ctx context.Context, env *Env, tool string, req *sdk.CallToolRequest) (*Call, string, error) {
	client, subject, err := env.Client(ctx, req)
	if err != nil {
		return nil, "", err
	}
	return &Call{Client: client, Req: req, env: env, tool: tool}, subject, nil
}

// CanonicalArgs is the JSON a confirmation token is bound to: the input with
// confirmation_token removed, keys sorted, numbers kept exact.
func CanonicalArgs(in any) ([]byte, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	delete(m, "confirmation_token")
	return json.Marshal(m) // map keys are sorted
}

func previewResult(spec Spec, p *Preview, token string, exp time.Time) *Result {
	var b strings.Builder
	b.WriteString("PREVIEW: nothing has been done yet.\n")
	b.WriteString(p.Action)
	b.WriteString("\n")
	if p.Price != "" {
		b.WriteString("Price: " + p.Price + "\n")
	}
	for _, w := range p.Warnings {
		b.WriteString("Note: " + w + "\n")
	}
	fmt.Fprintf(&b, "\nAsk the user to confirm. If they agree, call %s again with the same arguments and confirmation_token %q (valid until %s). If they do not, do nothing.",
		spec.Name, token, exp.UTC().Format(time.RFC3339))
	return &Result{
		Text: b.String(),
		Data: map[string]any{
			"status":             "confirmation_required",
			"action":             p.Action,
			"price":              p.Price,
			"warnings":           p.Warnings,
			"confirmation_token": token,
			"expires_at":         exp.UTC().Format(time.RFC3339),
			"details":            p.Data,
		},
	}
}

func refusedResult(spec Spec) *Result {
	return &Result{
		IsError: true,
		Text: fmt.Sprintf("Nothing was done: that confirmation_token is not valid for this request. It may have expired (tokens last 5 minutes), "+
			"or the arguments differ from the preview's. Call %s again without confirmation_token to get a new preview, and confirm it with the user.", spec.Name),
		Data: map[string]any{"status": "confirmation_invalid"},
	}
}
