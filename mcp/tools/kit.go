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
	// ConfirmWhen, for a two-step tool that confirms only some calls, says
	// which ones in the description's words ("when replace is true"); the
	// others run in one step. Register such a tool with addConfirmedWith.
	ConfirmWhen string
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
	// CallerAddress describes, for this call, the address the API sees its
	// requests come from: what open_ssh_access opens port 22 to. Default: a
	// generic description.
	CallerAddress func(req *sdk.CallToolRequest) CallerAddress
	Logger        *slog.Logger
}

// CallerAddress is the address the API sees a call's requests come from.
type CallerAddress struct {
	// IP is the address as this server sees it, when it knows it.
	IP string
	// Description says where the address comes from, in our own words.
	Description string
}

func (e *Env) callerAddress(req *sdk.CallToolRequest) CallerAddress {
	if e.CallerAddress != nil {
		return e.CallerAddress(req)
	}
	return CallerAddress{Description: "the public address this server's requests to Pantech come from"}
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
	// bound is the live state a confirmed call's token was checked
	// against (see confirmOptions.bind), for execute to use.
	bound string
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
	// bound is the live state the token is bound to, for a tool registered
	// with confirmOptions.bind: the preview sets it from the same function.
	bound string
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
	switch {
	case s.Risk.NeedsConfirmation() && s.ConfirmWhen != "":
		d += "\n\nTwo steps " + s.ConfirmWhen + ": the first call changes nothing and returns a preview and a confirmation_token. Show the preview to the user; only after they agree, call again with exactly the same arguments plus confirmation_token. Other calls run in one step."
	case s.Risk.NeedsConfirmation():
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
	addConfirmedWith(spec, confirmOptions[In]{}, preview, execute)
}

// confirmOptions refine a two-step tool.
type confirmOptions[In any] struct {
	// when, if set, reports whether this call needs confirmation; a call it
	// returns false for runs execute in one step. Spec.ConfirmWhen must say
	// the same in words.
	when func(in In) bool
	// whenLive is when for a decision that needs the account's state (for
	// example, whether a name is in one of its DNS zones).
	whenLive func(ctx context.Context, c *Call, in In) (bool, error)
	// bind, if set, reads live state the token is bound to besides the
	// arguments: a quoted price, a record's version. The preview sets
	// Preview.bound from the same function, and the confirmed call reads it
	// again, so a confirmation is refused when that state changed after the
	// user saw the preview. execute finds the value in Call.bound.
	bind func(ctx context.Context, c *Call, in In) (string, error)
}

// addConfirmedWith is addConfirmed with options.
func addConfirmedWith[In confirmable](spec Spec, opts confirmOptions[In],
	preview func(ctx context.Context, c *Call, in In) (*Preview, error),
	execute func(ctx context.Context, c *Call, in In) (*Result, error),
) {
	if !spec.Risk.NeedsConfirmation() {
		panic("tools: " + spec.Name + " does not need confirmation: register it with add")
	}
	if (opts.when != nil || opts.whenLive != nil) != (spec.ConfirmWhen != "") {
		panic("tools: " + spec.Name + ": ConfirmWhen and confirmOptions.when go together")
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
				rec := record(ctx)
				needs := true
				switch {
				case opts.when != nil:
					needs = opts.when(in)
				case opts.whenLive != nil:
					if needs, err = opts.whenLive(ctx, c, in); err != nil {
						return toolError(ctx, err).render()
					}
				}
				if !needs {
					res, err := execute(ctx, c, in)
					if err != nil {
						return toolError(ctx, err).render()
					}
					return res.render()
				}
				args, err := CanonicalArgs(in)
				if err != nil {
					return toolError(ctx, err).render()
				}
				token := in.token()
				if token == "" {
					rec.Phase = "preview"
					p, err := preview(ctx, c, in)
					if err != nil {
						return toolError(ctx, err).render()
					}
					tok, exp := env.Confirm.Issue(spec.Name, boundArgs(args, p.bound), subject)
					return previewResult(spec, p, tok, exp).render()
				}
				if opts.bind != nil {
					if c.bound, err = opts.bind(ctx, c, in); err != nil {
						return toolError(ctx, err).render()
					}
				}
				if err := env.Confirm.Verify(token, spec.Name, boundArgs(args, c.bound), subject); err != nil {
					rec.Phase = "refused"
					return refusedResult(spec, opts.bind != nil).render()
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

// boundArgs adds the live state a token is bound to after the canonical
// arguments. A NUL cannot appear in JSON, so the two never run together.
func boundArgs(args []byte, bound string) []byte {
	if bound == "" {
		return args
	}
	out := slices.Clone(args)
	out = append(out, 0)
	return append(out, bound...)
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

func refusedResult(spec Spec, bound bool) *Result {
	why := "or the arguments differ from the preview's"
	if bound {
		why = "the arguments differ from the preview's, or what the preview showed (the price, or the record) has changed since"
	}
	return &Result{
		IsError: true,
		Text: fmt.Sprintf("Nothing was done: that confirmation_token is not valid for this request. It may have expired (tokens last 5 minutes), "+
			"%s. Call %s again without confirmation_token to get a new preview, and confirm it with the user.", why, spec.Name),
		Data: map[string]any{"status": "confirmation_invalid"},
	}
}
