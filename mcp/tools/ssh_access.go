package tools

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

// Temporary SSH access opens port 22 of a standard instance, for 15 minutes,
// to the address the API sees the request come from. A grant carries no
// secret (only a host, a port and an expiry): the person logs in with their
// own SSH key, which this server never sees.

// webAppWarning is shown on every open_ssh_access preview.
const webAppWarning = "If you are using a web-based AI app (in a browser), this is that app's server address, not yours — open SSH from the console or CLI instead."

type openSSHAccessInput struct {
	InstanceID string `json:"instance_id" jsonschema:"A running standard instance that is the only one using its security group."`
	Confirmation
}

type sshGrantInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance's id."`
	GrantID    string `json:"grant_id" jsonschema:"The SSH access grant's id, e.g. sshg_…, from open_ssh_access."`
}

func init() {
	addConfirmed(Spec{
		Name: "open_ssh_access", Title: "Open SSH to an instance for 15 minutes",
		Description: "Open port 22 of a standard instance for 15 minutes to the address Pantech sees this request come from, so the user can SSH in without leaving the port open. Through a local server (stdio) that is the user's own computer; through a hosted or web-based AI app it is the app's server, which is not useful: suggest the console or CLI then. Waits until the port is open and returns the ready-to-run ssh command. The user logs in with their own SSH key, which this server never sees.",
		Risk:        Exposure,
		Routes: []string{"GET /instances/{instance_id}", "POST /instances/{instance_id}/ssh-access",
			"GET /operations/{operation_id}", "GET /instances/{instance_id}/ssh-access/{grant_id}"},
	}, func(ctx context.Context, c *Call, in openSSHAccessInput) (*Preview, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		vm, err := c.Client.Instances.Get(ctx, in.InstanceID)
		if err != nil {
			return nil, err
		}
		addr := c.env.callerAddress(c.Req)
		p := &Preview{
			Action: fmt.Sprintf("Open SSH (port 22) on instance %s for 15 minutes, only to the address Pantech will open SSH to, as this server sees it: %s.",
				vm.ID, addr.Description),
			Warnings: []string{
				webAppWarning,
				"The port closes by itself after 15 minutes; revoke_ssh_access closes it sooner.",
			},
			Data: map[string]any{"instance": vm, "address": addr.IP},
		}
		if vm.ObservedState != "running" {
			p.Warnings = append(p.Warnings, "The instance is not running: start it first, or Pantech refuses.")
		}
		if vm.SubnetID != nil {
			p.Warnings = append(p.Warnings, "This instance is in a VPC: temporary SSH access is not available there yet (use a public IP with a port forwarding rule).")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in openSSHAccessInput) (*Result, error) {
		acc, err := c.Client.Instances.OpenSSHAccess(ctx, in.InstanceID, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.waitSSHGrant(ctx, in.InstanceID, acc)
	})

	add(Spec{
		Name: "get_ssh_access", Title: "Get an SSH access grant",
		Description: "Get a temporary SSH access grant: pending, active (with the host and port to connect to and when it closes), revoking, revoked, expired or failed.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}/ssh-access/{grant_id}"},
	}, func(ctx context.Context, c *Call, in sshGrantInput) (*Result, error) {
		if err := checkIDs("instance_id", in.InstanceID, "grant_id", in.GrantID); err != nil {
			return nil, err
		}
		g, err := c.Client.Instances.GetSSHAccess(ctx, in.InstanceID, in.GrantID)
		if err != nil {
			return nil, err
		}
		if g.Status == "active" {
			return grantResult(g)
		}
		return ok(fmt.Sprintf("SSH access grant %s on instance %s is %s.", g.ID, g.InstanceID, arg(g.Status)), g)
	})

	add(Spec{
		Name: "revoke_ssh_access", Title: "Close temporary SSH access",
		Description: "Close the port a temporary SSH access grant opened, before it expires by itself. It only takes access away, so Manage access is enough.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"DELETE /instances/{instance_id}/ssh-access/{grant_id}", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in sshGrantInput) (*Result, error) {
		if err := checkIDs("instance_id", in.InstanceID, "grant_id", in.GrantID); err != nil {
			return nil, err
		}
		acc, err := c.Client.Instances.RevokeSSHAccess(ctx, in.InstanceID, in.GrantID, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, fmt.Sprintf("SSH access grant %s on instance %s closed", in.GrantID, in.InstanceID))
	})
}

// waitSSHGrant follows an accepted grant: its operation, then the grant until
// it is active, within the wait budget, with progress.
func (c *Call) waitSSHGrant(ctx context.Context, instanceID string, acc *pantech.Accepted) (*Result, error) {
	deadline := time.Now().Add(c.env.waitBudget())
	p := newProgress(c.Req)
	grantID := acc.ResourceID
	data := map[string]any{"operation_id": acc.OperationID, "grant_id": grantID, "instance_id": instanceID}
	pending := func() (*Result, error) {
		data["status"] = "pending"
		return ok(fmt.Sprintf("SSH access is being opened on instance %s (grant %s, operation %s). Call get_ssh_access in a moment for the host, port and command.",
			instanceID, arg(grantID), acc.OperationID), data)
	}
	if acc.OperationID != "" {
		op, err := c.waitOperation(ctx, acc.OperationID, deadline, p)
		switch {
		case errors.As(err, new(*pantech.OperationError)):
			return nil, err
		case err != nil:
			return pending()
		}
		if grantID == "" {
			grantID = op.ResourceID
			data["grant_id"] = grantID
		}
	}
	if checkIDs("grant_id", grantID) != nil {
		return pending()
	}
	for {
		g, err := c.Client.Instances.GetSSHAccess(ctx, instanceID, grantID)
		if err != nil {
			return pending()
		}
		switch g.Status {
		case "active":
			return grantResult(g)
		case "failed", "revoked", "expired":
			return &Result{IsError: true, Text: fmt.Sprintf("SSH access grant %s on instance %s ended as %s: port 22 is not open.", g.ID, instanceID, arg(g.Status)), Data: g}, nil
		}
		p.send(ctx, fmt.Sprintf("SSH access grant %s is %s", grantID, g.Status))
		if time.Now().Add(c.env.pollInterval()).After(deadline) {
			return pending()
		}
		if err := sleep(ctx, c.env.pollInterval()); err != nil {
			return pending()
		}
	}
}

// grantResult describes an active grant and the command to connect with.
func grantResult(g *pantech.SSHAccessGrant) (*Result, error) {
	host := str(g.Host)
	port := 22
	if g.Port != nil {
		port = *g.Port
	}
	expires := "in 15 minutes"
	if g.ExpiresAt != nil {
		expires = "at " + g.ExpiresAt.UTC().Format(time.RFC3339)
	}
	command := "ssh root@" + host + " -p " + strconv.Itoa(port)
	data := map[string]any{"status": "active", "grant": g, "host": host, "port": port, "command": command}
	if g.ExpiresAt != nil {
		data["expires_at"] = g.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if argPattern.MatchString(host) {
		return ok(fmt.Sprintf("SSH is open on instance %s (grant %s) until it closes by itself %s. Host %s, port %d. Run:\n\n    %s\n\n"+
			"root is the default login; if the image uses another default user, use that instead. It logs in with the user's own SSH key, which this server never sees. Close it sooner with revoke_ssh_access.",
			g.InstanceID, g.ID, expires, host, port, command), data)
	}
	return ok(fmt.Sprintf("SSH is open on instance %s (grant %s) until %s. The host, port and command are in the data. root is the default login unless the image uses another user; it logs in with the user's own SSH key, which this server never sees.",
		g.InstanceID, g.ID, expires), data)
}
