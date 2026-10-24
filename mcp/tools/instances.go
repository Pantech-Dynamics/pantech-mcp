package tools

import (
	"context"
	"errors"
	"fmt"
	"time"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

type listInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"Items per page, 1 to 100 (default 50)."`
	Cursor string `json:"cursor,omitempty" jsonschema:"The next_cursor of the previous page, to get the next one."`
	Q      string `json:"q,omitempty" jsonschema:"Only items whose name contains this text."`
}

func (in listInput) params() *pantech.ListParams {
	return &pantech.ListParams{Limit: listLimit(in.Limit), Cursor: in.Cursor, Q: in.Q}
}

type instanceInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance's id, e.g. vm_…"`
}

type createInstanceInput struct {
	Name            string            `json:"name" jsonschema:"1 to 255 lowercase letters, digits and hyphens, unique among your instances."`
	PlanSlug        string            `json:"plan_slug" jsonschema:"The size, from list_plans."`
	ImageSlug       string            `json:"image_slug" jsonschema:"The operating system, from list_images."`
	SSHKeyID        string            `json:"ssh_key_id" jsonschema:"An SSH key from list_ssh_keys, installed for the default user."`
	Region          string            `json:"region,omitempty" jsonschema:"A region code from list_regions, e.g. af-abj."`
	SubnetID        string            `json:"subnet_id,omitempty" jsonschema:"Place the instance in this VPC subnet (private address only)."`
	NetworkID       string            `json:"network_id,omitempty" jsonschema:"The subnet's VPC."`
	SecurityGroupID string            `json:"security_group_id,omitempty" jsonschema:"Standard instances only: one of your security groups."`
	Encrypted       bool              `json:"encrypted,omitempty" jsonschema:"Encrypt the disk."`
	Tags            map[string]string `json:"tags,omitempty" jsonschema:"Your own labels."`
	Confirmation
}

type resizeInstanceInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance's id."`
	PlanSlug   string `json:"plan_slug" jsonschema:"A bigger plan than the current one, from list_plans. Instances only grow."`
	Confirmation
}

type renameInstanceInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance's id."`
	Name       string `json:"name" jsonschema:"The new name: lowercase letters, digits and hyphens, unique among your instances."`
}

type deleteInstanceInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance's id."`
	Confirmation
}

type changeSSHKeyInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance's id. It must be running."`
	SSHKeyID   string `json:"ssh_key_id" jsonschema:"The SSH key to log in with from now on, from list_ssh_keys."`
	Confirmation
}

func init() {
	add(Spec{
		Name: "list_instances", Title: "List instances",
		Description: "List the organization's instances (virtual servers), with their state, plan, addresses and tags. Paginated: pass next_cursor as cursor for more.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /instances"},
	}, func(ctx context.Context, c *Call, in listInput) (*Result, error) {
		page, err := c.Client.Instances.List(ctx, in.params())
		if err != nil {
			return nil, err
		}
		return pageResult(page, "instance", "instances")
	})

	add(Spec{
		Name: "get_instance", Title: "Get an instance",
		Description: "Get one instance: its state, plan, image, addresses, network, security group, tags and any failure.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}"},
	}, func(ctx context.Context, c *Call, in instanceInput) (*Result, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		vm, err := c.Client.Instances.Get(ctx, in.InstanceID)
		if err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Instance %s is %s (desired %s).", vm.ID, vm.ObservedState, vm.DesiredState), vm)
	})

	addConfirmed(Spec{
		Name: "create_instance", Title: "Create an instance",
		Description: "Create a virtual server. It is paid first: the initial payment is taken from credit (or the saved card), then it is provisioned. Waits up to a minute, then returns the order and operation ids.",
		Risk:        Cost,
		Routes:      []string{"GET /plans", "GET /billing/credit", "POST /instances", "GET /instance-orders/{order_id}", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in createInstanceInput) (*Preview, error) {
		placement := placementOf(in.SubnetID, in.NetworkID)
		plan, err := c.findPlan(ctx, in.PlanSlug, placement)
		if err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Create a new %s instance on plan %s (%d vCPU, %d MB memory, %d GB disk) with image %s.",
				placement, plan.Slug, plan.VCPU, plan.MemoryMB, plan.DiskGB, arg(in.ImageSlug)),
			Price: planPrice(plan),
			Data:  map[string]any{"request": in.request(), "plan": plan},
		}
		if note := c.creditNote(ctx); note != "" {
			p.Warnings = append(p.Warnings, note)
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in createInstanceInput) (*Result, error) {
		order, err := c.Client.Instances.Create(ctx, in.request(), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.followOrder(ctx, "instance", order.OrderID, order.InstanceID, func(ctx context.Context) (string, *string, *string, error) {
			o, err := c.Client.Instances.GetOrder(ctx, order.OrderID)
			if err != nil {
				return "", nil, nil, err
			}
			return o.Status, o.OperationID, o.FailureCode, nil
		})
	})

	instanceAction := func(name, title, desc, verb string, idempotent bool, route string, call func(*pantech.InstancesService) func(context.Context, string, ...pantech.RequestOption) (*pantech.Accepted, error)) {
		add(Spec{
			Name: name, Title: title, Description: desc,
			Risk: Write, Idempotent: idempotent,
			Routes: []string{route, "GET /operations/{operation_id}"},
		}, func(ctx context.Context, c *Call, in instanceInput) (*Result, error) {
			if err := checkIDs("instance_id", in.InstanceID); err != nil {
				return nil, err
			}
			acc, err := call(c.Client.Instances)(ctx, in.InstanceID, c.writeOpts()...)
			if err != nil {
				return nil, err
			}
			return c.finish(ctx, acc, fmt.Sprintf("instance %s %s", in.InstanceID, verb))
		})
	}
	instanceAction("start_instance", "Start an instance", "Start a stopped instance. Waits up to a minute for it.", "started", true,
		"POST /instances/{instance_id}/start", func(s *pantech.InstancesService) func(context.Context, string, ...pantech.RequestOption) (*pantech.Accepted, error) {
			return s.Start
		})
	instanceAction("stop_instance", "Stop an instance", "Stop a running instance (it keeps its disk, addresses and storage billing). Waits up to a minute for it.", "stopped", true,
		"POST /instances/{instance_id}/stop", func(s *pantech.InstancesService) func(context.Context, string, ...pantech.RequestOption) (*pantech.Accepted, error) {
			return s.Stop
		})
	instanceAction("reboot_instance", "Reboot an instance", "Reboot a running instance. Waits up to a minute for it.", "rebooted", false,
		"POST /instances/{instance_id}/reboot", func(s *pantech.InstancesService) func(context.Context, string, ...pantech.RequestOption) (*pantech.Accepted, error) {
			return s.Reboot
		})

	addConfirmed(Spec{
		Name: "resize_instance", Title: "Resize an instance",
		Description: "Move an instance to a bigger plan. It is stopped, resized and started again. Instances only grow; the bigger plan is billed hourly from the change.",
		Risk:        Cost, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}", "GET /plans", "POST /instances/{instance_id}/resize", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in resizeInstanceInput) (*Preview, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		vm, err := c.Client.Instances.Get(ctx, in.InstanceID)
		if err != nil {
			return nil, err
		}
		placement := placementOf(str(vm.SubnetID), str(vm.NetworkID))
		plan, err := c.findPlan(ctx, in.PlanSlug, placement)
		if err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Resize instance %s from plan %s to %s (%d vCPU, %d MB memory, %d GB disk). It is stopped, resized and started again, so it is offline for a few minutes.",
				vm.ID, str(vm.PlanSlug), plan.Slug, plan.VCPU, plan.MemoryMB, plan.DiskGB),
			Price:    "new plan " + planPrice(plan) + ", billed hourly from the change (no upfront charge)",
			Warnings: []string{"Instances only grow: this cannot be undone by resizing down."},
			Data:     map[string]any{"instance": vm, "new_plan": plan},
		}
		if vm.PlanSlug != nil {
			if cur, err := c.findPlan(ctx, *vm.PlanSlug, placement); err == nil {
				p.Price += "; current plan " + planPrice(cur)
			}
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in resizeInstanceInput) (*Result, error) {
		acc, err := c.Client.Instances.Resize(ctx, in.InstanceID, pantech.ResizeRequest{PlanSlug: in.PlanSlug}, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, fmt.Sprintf("instance %s resized to %s", in.InstanceID, in.PlanSlug))
	})

	add(Spec{
		Name: "rename_instance", Title: "Rename an instance",
		Description: "Change an instance's name.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"POST /instances/{instance_id}/rename", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in renameInstanceInput) (*Result, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		acc, err := c.Client.Instances.Rename(ctx, in.InstanceID, pantech.RenameRequest{Name: in.Name}, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, fmt.Sprintf("instance %s renamed", in.InstanceID))
	})

	addConfirmed(Spec{
		Name: "delete_instance", Title: "Delete an instance",
		Description: "Delete an instance. It is stopped, a safety snapshot of its root disk is kept for 24 hours, then it is removed. Attached volumes are detached, not deleted. Needs a key with delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}", "GET /instances/{instance_id}/volumes", "DELETE /instances/{instance_id}", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in deleteInstanceInput) (*Preview, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		vm, err := c.Client.Instances.Get(ctx, in.InstanceID)
		if err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Permanently delete instance %s. Everything on its root disk is lost after the 24-hour safety snapshot expires.", vm.ID),
			Data:   map[string]any{"instance": vm},
		}
		if vols, err := c.Client.Instances.ListVolumes(ctx, vm.ID, &pantech.ListParams{Limit: 100}); err == nil && len(vols.Data) > 0 {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s attached: detached and kept, still billed.", plural(len(vols.Data), "volume is", "volumes are")))
			p.Data = map[string]any{"instance": vm, "attached_volumes": vols.Data}
		}
		if vm.DeletionProtection {
			p.Warnings = append(p.Warnings, "Deletion protection is on: the API will refuse until it is turned off in the console.")
		}
		if vm.PublicIPv4 != nil {
			p.Warnings = append(p.Warnings, "It has a public IP: release it and delete port forwarding rules that point at it first, or the delete is refused.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in deleteInstanceInput) (*Result, error) {
		acc, err := c.Client.Instances.Delete(ctx, in.InstanceID, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, fmt.Sprintf("instance %s deleted", in.InstanceID))
	})

	addConfirmed(Spec{
		Name: "change_instance_ssh_key", Title: "Change an instance's SSH key",
		Description: "Replace the SSH key used to log in to an instance by another key on the account (for example after losing the private key). The instance is stopped, given the new key and started again (about two minutes); the previous key stops working.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}", "POST /instances/{instance_id}/change-ssh-key", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in changeSSHKeyInput) (*Preview, error) {
		if err := checkIDs("instance_id", in.InstanceID, "ssh_key_id", in.SSHKeyID); err != nil {
			return nil, err
		}
		vm, err := c.Client.Instances.Get(ctx, in.InstanceID)
		if err != nil {
			return nil, err
		}
		return &Preview{
			Action: fmt.Sprintf("Replace the login SSH key of instance %s with key %s. The instance restarts (about two minutes of downtime) and the previous key stops working.", vm.ID, in.SSHKeyID),
			Data:   map[string]any{"instance": vm},
		}, nil
	}, func(ctx context.Context, c *Call, in changeSSHKeyInput) (*Result, error) {
		acc, err := c.Client.Instances.ChangeSSHKey(ctx, in.InstanceID, pantech.ChangeSSHKeyRequest{SSHKeyID: in.SSHKeyID}, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, fmt.Sprintf("instance %s now accepts SSH key %s", in.InstanceID, in.SSHKeyID))
	})
}

func (in createInstanceInput) request() pantech.CreateInstanceRequest {
	req := pantech.CreateInstanceRequest{Name: in.Name, PlanSlug: in.PlanSlug, ImageSlug: in.ImageSlug, SSHKeyID: in.SSHKeyID}
	if in.Region != "" {
		req.Region = pantech.Ptr(in.Region)
	}
	if in.SubnetID != "" {
		req.SubnetID = pantech.Ptr(in.SubnetID)
	}
	if in.NetworkID != "" {
		req.NetworkID = pantech.Ptr(in.NetworkID)
	}
	if in.SecurityGroupID != "" {
		req.SecurityGroupID = pantech.Ptr(in.SecurityGroupID)
	}
	if in.Encrypted {
		req.Encrypted = pantech.Ptr(true)
	}
	if len(in.Tags) > 0 {
		req.Tags = &in.Tags
	}
	return req
}

// followOrder waits for an instance or database order to be provisioned and
// then for its create operation, within the wait budget.
func (c *Call) followOrder(ctx context.Context, kind, orderID, resourceID string,
	get func(context.Context) (status string, opID, failure *string, err error),
) (*Result, error) {
	deadline := time.Now().Add(c.env.waitBudget())
	p := newProgress(c.Req)
	data := map[string]any{"order_id": orderID, kind + "_id": resourceID}
	running := func(status string) (*Result, error) {
		data["status"] = status
		return ok(fmt.Sprintf("The %s order %s was placed and is %s. The %s will be %s; check it later with get_%s.",
			kind, orderID, status, kind, resourceID, kind), data)
	}
	for {
		status, opID, failure, err := get(ctx)
		if err != nil {
			// The order exists: report it rather than an error that invites a retry.
			return running("placed")
		}
		switch status {
		case pantech.OrderProvisioned:
			if opID == nil || *opID == "" {
				data["status"] = "provisioned"
				return ok(fmt.Sprintf("The %s %s is provisioned (order %s).", kind, resourceID, orderID), data)
			}
			data["operation_id"] = *opID
			op, err := c.waitOperation(ctx, *opID, deadline, p)
			if op != nil {
				data["operation"] = op
			}
			switch {
			case err == nil:
				data["status"] = "succeeded"
				return ok(fmt.Sprintf("Done: the %s %s is ready (order %s, operation %s).", kind, resourceID, orderID, *opID), data)
			case errors.As(err, new(*pantech.OperationError)):
				return nil, err
			default:
				data["status"] = "running"
				return ok(fmt.Sprintf("The %s %s is paid for and being created (operation %s); call wait_for_operation with that id to follow it.", kind, resourceID, *opID), data)
			}
		case pantech.OrderFailed, pantech.OrderPaymentFailed:
			e := &pantech.OrderError{Kind: kind, OrderID: orderID, Status: status}
			if failure != nil {
				e.FailureCode = *failure
			}
			return nil, e
		}
		p.send(ctx, fmt.Sprintf("%s order %s is %s", kind, orderID, status))
		if time.Now().Add(c.env.pollInterval()).After(deadline) {
			return running(status)
		}
		if err := sleep(ctx, c.env.pollInterval()); err != nil {
			return running(status)
		}
	}
}

// pageResult summarises one page of a list.
func pageResult[T any](page *pantech.Page[T], one, many string) (*Result, error) {
	text := plural(len(page.Data), one, many) + "."
	if page.HasMore() {
		text += " There are more: call again with cursor set to next_cursor."
	}
	return ok(text, page)
}
