package tools

import (
	"context"
	"errors"
	"fmt"
	"time"

	pantech "github.com/Pantech-Dynamics/pantech-go"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// progress sends MCP progress notifications when the client asked for them
// (it sent a progressToken), and does nothing otherwise.
type progress struct {
	req   *sdk.CallToolRequest
	token any
	n     float64
}

func newProgress(req *sdk.CallToolRequest) *progress {
	p := &progress{req: req}
	if req != nil && req.Params != nil {
		p.token = req.Params.GetProgressToken()
	}
	return p
}

func (p *progress) send(ctx context.Context, msg string) {
	if p.token == nil || p.req.Session == nil {
		return
	}
	p.n++
	// Best effort: a client that went away just misses the update.
	_ = p.req.Session.NotifyProgress(ctx, &sdk.ProgressNotificationParams{ProgressToken: p.token, Progress: p.n, Message: msg})
}

// errStillRunning means the wait budget ran out before the change finished.
var errStillRunning = errors.New("still running")

// sleep pauses for d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// waitOperation polls the operation until it finishes or the deadline
// passes, reporting progress. It returns the last state seen; err is
// errStillRunning when it ran out of time, *pantech.OperationError when the
// operation failed, or an API error.
func (c *Call) waitOperation(ctx context.Context, opID string, deadline time.Time, p *progress) (*pantech.Operation, error) {
	var last *pantech.Operation
	for {
		op, err := c.Client.Operations.Get(ctx, opID)
		if err != nil {
			return last, err
		}
		last = op
		switch op.Status {
		case pantech.OperationSucceeded:
			p.send(ctx, fmt.Sprintf("operation %s succeeded", op.ID))
			return op, nil
		case pantech.OperationFailed:
			return op, &pantech.OperationError{Operation: op}
		}
		p.send(ctx, fmt.Sprintf("operation %s is %s", op.ID, op.Status))
		if time.Now().Add(c.env.pollInterval()).After(deadline) {
			return op, errStillRunning
		}
		if err := sleep(ctx, c.env.pollInterval()); err != nil {
			return op, errStillRunning
		}
	}
}

// finish waits for an accepted change and says how it went. what is our
// own description, such as "Instance vm_1 is stopping".
func (c *Call) finish(ctx context.Context, acc *pantech.Accepted, what string) (*Result, error) {
	if acc.OperationID == "" {
		// The API answers a change that changes nothing with no operation.
		return ok("Nothing to change: it was already that way.", map[string]any{"status": "unchanged", "resource_id": acc.ResourceID})
	}
	deadline := time.Now().Add(c.env.waitBudget())
	op, err := c.waitOperation(ctx, acc.OperationID, deadline, newProgress(c.Req))
	data := map[string]any{"operation_id": acc.OperationID, "resource_id": acc.ResourceID}
	if op != nil {
		data["operation"] = op
	}
	switch {
	case err == nil:
		data["status"] = "succeeded"
		return ok(fmt.Sprintf("Done: %s (operation %s succeeded).", what, acc.OperationID), data)
	case errors.As(err, new(*pantech.OperationError)):
		return nil, err
	default:
		// Still running, or polling failed: the change was accepted either
		// way, so say so rather than report an error that invites a retry.
		data["status"] = "running"
		return ok(fmt.Sprintf("Started: %s. It is still running (operation %s); call wait_for_operation with that id to follow it.", what, acc.OperationID), data)
	}
}
