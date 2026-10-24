package tools

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type operationInput struct {
	OperationID string `json:"operation_id" jsonschema:"The operation's id, e.g. op_…"`
}

// Webhooks are listed only: creating one or rotating its secret returns the
// signing secret, which never goes through this server.
func init() {
	add(Spec{
		Name: "list_webhooks", Title: "List webhooks",
		Description: "List the organization's webhook endpoints: the events each receives and whether it is enabled or failing. URLs are shown without query strings or credentials, and secrets are never returned.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /webhooks"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		hooks, err := c.Client.Webhooks.List(ctx)
		if err != nil {
			return nil, err
		}
		for i := range hooks {
			hooks[i].URL = redactURL(hooks[i].URL)
		}
		return ok(plural(len(hooks), "webhook", "webhooks")+".", hooks)
	})

	add(Spec{
		Name: "get_operation", Title: "Get an operation",
		Description: "Get the state of an asynchronous change: submitting and submitted while it runs, succeeded or failed (with a reason) when done.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in operationInput) (*Result, error) {
		if err := checkIDs("operation_id", in.OperationID); err != nil {
			return nil, err
		}
		op, err := c.Client.Operations.Get(ctx, in.OperationID)
		if err != nil {
			return nil, err
		}
		text := fmt.Sprintf("Operation %s (%s on %s) is %s.", op.ID, op.Kind, op.ResourceID, op.Status)
		if op.Failure != nil {
			text += fmt.Sprintf(" Failure: %s (%s).", op.Failure.Reason, op.Failure.Code)
		}
		return ok(text, op)
	})

	add(Spec{
		Name: "wait_for_operation", Title: "Wait for an operation",
		Description: "Wait up to about a minute for an operation to finish, with progress updates. If it is still running, call again.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in operationInput) (*Result, error) {
		if err := checkIDs("operation_id", in.OperationID); err != nil {
			return nil, err
		}
		op, err := c.waitOperation(ctx, in.OperationID, time.Now().Add(c.env.waitBudget()), newProgress(c.Req))
		switch {
		case err == nil:
			return ok(fmt.Sprintf("Operation %s (%s) succeeded.", op.ID, op.Kind), op)
		case errors.Is(err, errStillRunning):
			return ok(fmt.Sprintf("Operation %s is still %s. Call wait_for_operation again to keep waiting.", op.ID, op.Status), op)
		default:
			return nil, err // a failed operation, or the API's error
		}
	})
}
