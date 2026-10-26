package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	Agent           bool              `json:"agent,omitempty" jsonschema:"Install the Pantech agent at first boot, so start_tools_session can work in the instance with no login to it. Set at creation only."`
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
			Data:  map[string]any{"request": in.request(), "plan": plan, "agent": in.Agent},
		}
		if in.Agent {
			p.Action += " The Pantech agent is installed at first boot, so an AI app can work in it through a tools session."
		}
		if note := c.creditNote(ctx); note != "" {
			p.Warnings = append(p.Warnings, note)
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in createInstanceInput) (*Result, error) {
		order, err := c.createInstance(ctx, in)
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
		Description: "Delete an instance. It is stopped, a safety snapshot of its root disk is kept for 24 hours, then it is removed. Attached volumes are detached, not deleted. Needs an AI app connected with Manage and delete access.",
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
			Action:   fmt.Sprintf("Permanently delete instance %s. Everything on its root disk is lost after the 24-hour safety snapshot expires.", vm.ID),
			Warnings: []string{deleteAccessNote},
			Data:     map[string]any{"instance": vm},
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

// createInstance places the order. The SDK's request has no agent_enabled
// yet, so an instance with the agent is sent through Do with the field added.
func (c *Call) createInstance(ctx context.Context, in createInstanceInput) (*pantech.OrderAccepted, error) {
	if !in.Agent {
		return c.Client.Instances.Create(ctx, in.request(), c.writeOpts()...)
	}
	raw, err := json.Marshal(in.request())
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	body["agent_enabled"] = true
	var order pantech.OrderAccepted
	if err := c.Client.Do(ctx, "POST", "/instances", nil, body, &order, c.writeOpts()...); err != nil {
		return nil, err
	}
	return &order, nil
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

// deleteAccessNote goes on the preview of every change the API treats as
// destructive: an AI app connected without delete access is refused.
const deleteAccessNote = `This AI app needs "Manage and delete" access for this: with Read only or Manage, Pantech refuses it. Reconnect the app with Manage and delete, or do it in the console.`

type rebuildInstanceInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance's id."`
	ImageSlug  string `json:"image_slug,omitempty" jsonschema:"The operating system to install, from list_images. Default: the instance's current image."`
	Confirmation
}

type restoreInstanceInput struct {
	InstanceID  string `json:"instance_id" jsonschema:"The instance's id."`
	SnapshotID  string `json:"snapshot_id" jsonschema:"A snapshot of this instance's root disk, from list_instance_snapshots."`
	ConfirmName string `json:"confirm_name" jsonschema:"The instance's current name, exactly as the user types it, to confirm that everything written since the snapshot is lost."`
	Confirmation
}

type instanceMetricsInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance's id."`
	Interval   string `json:"interval,omitempty" jsonschema:"The window: 1h, 24h (default), 7d or 30d."`
}

type instanceListInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance's id."`
	pageInput
}

type createInstanceSnapshotInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance's id."`
	Name       string `json:"name" jsonschema:"The snapshot's name, unique among this instance's snapshots."`
	Confirmation
}

type orderInput struct {
	OrderID string `json:"order_id" jsonschema:"The order's id, e.g. ord_…"`
	Wait    bool   `json:"wait,omitempty" jsonschema:"Wait up to about a minute for the order to be provisioned and its create operation to finish."`
}

func init() {
	addConfirmed(Spec{
		Name: "rebuild_instance", Title: "Rebuild an instance",
		Description: "Reinstall an instance's operating system, from image_slug or its current image. EVERYTHING on the root disk is erased; attached volumes, addresses and the plan are kept. Suggest create_instance_snapshot first. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true,
		Routes: []string{"GET /instances/{instance_id}", "POST /instances/{instance_id}/rebuild", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in rebuildInstanceInput) (*Preview, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		vm, err := c.Client.Instances.Get(ctx, in.InstanceID)
		if err != nil {
			return nil, err
		}
		image := in.ImageSlug
		if image == "" {
			image = str(vm.ImageSlug)
		}
		return &Preview{
			Action: fmt.Sprintf("Erase the root disk of instance %s and reinstall it with image %s. All data on the root disk is permanently lost; attached volumes, addresses and the plan are kept.", vm.ID, arg(valueOr(image, "(its current image)"))),
			Warnings: []string{
				"Data loss: files, databases and settings on the root disk cannot be recovered afterwards. Take a snapshot first (create_instance_snapshot) if anything may be needed.",
				deleteAccessNote,
			},
			Data: map[string]any{"instance": vm, "image_slug": image},
		}, nil
	}, func(ctx context.Context, c *Call, in rebuildInstanceInput) (*Result, error) {
		req := pantech.RebuildRequest{}
		if in.ImageSlug != "" {
			req.ImageSlug = pantech.Ptr(in.ImageSlug)
		}
		acc, err := c.Client.Instances.Rebuild(ctx, in.InstanceID, req, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, fmt.Sprintf("instance %s rebuilt", in.InstanceID))
	})

	addConfirmed(Spec{
		Name: "restore_instance_snapshot", Title: "Restore an instance from a snapshot",
		Description: "Roll an instance's root disk back to one of its own snapshots, then reboot it. Everything written to the root disk since the snapshot is lost; attached volumes are not changed. confirm_name must be the instance's current name, typed by the user. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true,
		Routes: []string{"GET /instances/{instance_id}", "GET /snapshots/{snapshot_id}", "POST /instances/{instance_id}/restore", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in restoreInstanceInput) (*Preview, error) {
		if err := checkIDs("instance_id", in.InstanceID, "snapshot_id", in.SnapshotID); err != nil {
			return nil, err
		}
		vm, err := c.Client.Instances.Get(ctx, in.InstanceID)
		if err != nil {
			return nil, err
		}
		if in.ConfirmName != vm.Name {
			return nil, errorf("confirm_name does not match instance %s's current name. Ask the user to type the instance's name exactly (see get_instance), then call again.", vm.ID)
		}
		snap, err := c.Client.Snapshots.Get(ctx, in.SnapshotID)
		if err != nil {
			return nil, err
		}
		if snap.InstanceID == nil || *snap.InstanceID != vm.ID {
			return nil, errorf("Snapshot %s is not a snapshot of instance %s's root disk. Call list_instance_snapshots for its snapshots.", snap.ID, vm.ID)
		}
		taken := "an unknown time"
		if snap.CompletedAt != nil {
			taken = snap.CompletedAt.UTC().Format(time.RFC3339)
		} else if snap.CreatedAt != nil {
			taken = snap.CreatedAt.UTC().Format(time.RFC3339)
		}
		return &Preview{
			Action: fmt.Sprintf("Roll the root disk of instance %s back to snapshot %s (taken %s), then reboot it. Everything written to the root disk since then is permanently lost.", vm.ID, snap.ID, taken),
			Warnings: []string{
				"Data loss: changes on the root disk since the snapshot cannot be recovered. Take a new snapshot first if they may be needed. Attached volumes are not changed.",
				deleteAccessNote,
			},
			Data: map[string]any{"instance": vm, "snapshot": snap},
		}, nil
	}, func(ctx context.Context, c *Call, in restoreInstanceInput) (*Result, error) {
		acc, err := c.Client.Instances.Restore(ctx, in.InstanceID, pantech.RestoreRequest{SnapshotID: in.SnapshotID, ConfirmName: in.ConfirmName}, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, fmt.Sprintf("instance %s restored from snapshot %s", in.InstanceID, in.SnapshotID))
	})

	add(Spec{
		Name: "get_instance_metrics", Title: "Get instance metrics",
		Description: "CPU, memory, network and disk activity of an instance over 1h, 24h (default), 7d or 30d, one point per bucket. available is false when no samples exist yet.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}/metrics"},
	}, func(ctx context.Context, c *Call, in instanceMetricsInput) (*Result, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		interval := valueOr(in.Interval, "24h")
		if !slices.Contains([]string{"1h", "24h", "7d", "30d"}, interval) {
			return nil, errorf("interval must be 1h, 24h, 7d or 30d.")
		}
		m, err := c.Client.Instances.GetMetrics(ctx, in.InstanceID, &pantech.InstanceMetricsParams{Interval: interval})
		if err != nil {
			return nil, err
		}
		if !m.Available {
			return ok(fmt.Sprintf("No metrics for instance %s yet (no samples over %s).", in.InstanceID, interval), m)
		}
		return ok(fmt.Sprintf("Metrics of instance %s over %s: %s.", in.InstanceID, interval, plural(len(m.Points), "point", "points")), m)
	})

	add(Spec{
		Name: "get_instance_usage", Title: "Get instance usage",
		Description: "Measured usage of one instance in the current billing period: running hours, storage, memory and network. These are measured quantities: report them exactly as given.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}/usage"},
	}, func(ctx context.Context, c *Call, in instanceInput) (*Result, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		u, err := c.Client.Instances.GetUsage(ctx, in.InstanceID)
		if err != nil {
			return nil, err
		}
		return usageResult("instance "+in.InstanceID, u)
	})

	add(Spec{
		Name: "list_instance_volumes", Title: "List an instance's volumes",
		Description: "List the volumes attached to an instance.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}/volumes"},
	}, func(ctx context.Context, c *Call, in instanceListInput) (*Result, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		page, err := c.Client.Instances.ListVolumes(ctx, in.InstanceID, &pantech.ListParams{Limit: listLimit(in.Limit), Cursor: in.Cursor})
		if err != nil {
			return nil, err
		}
		return pageResult(page, "volume", "volumes")
	})

	add(Spec{
		Name: "list_instance_snapshots", Title: "List an instance's snapshots",
		Description: "List the snapshots of an instance's root disk (manual and automatic). A snapshot id is what restore_instance_snapshot takes.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}/snapshots"},
	}, func(ctx context.Context, c *Call, in instanceListInput) (*Result, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		page, err := c.Client.Instances.ListSnapshots(ctx, in.InstanceID, &pantech.ListParams{Limit: listLimit(in.Limit), Cursor: in.Cursor})
		if err != nil {
			return nil, err
		}
		return pageResult(page, "snapshot", "snapshots")
	})

	addConfirmed(Spec{
		Name: "create_instance_snapshot", Title: "Snapshot an instance",
		Description: "Take a crash-consistent snapshot of an instance's root disk. Billed hourly on its stored size until deleted.",
		Risk:        Cost,
		Routes:      []string{"GET /instances/{instance_id}", "GET /snapshots/quote", "POST /instances/{instance_id}/snapshots", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in createInstanceSnapshotInput) (*Preview, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		vm, err := c.Client.Instances.Get(ctx, in.InstanceID)
		if err != nil {
			return nil, err
		}
		sizeGB := 0
		if vm.Spec != nil {
			sizeGB = vm.Spec.DiskGB
		}
		return &Preview{
			Action: fmt.Sprintf("Take a snapshot of the root disk of instance %s.", vm.ID),
			Price:  c.snapshotPrice(ctx, sizeGB),
			Data:   map[string]any{"instance": vm, "name": in.Name},
		}, nil
	}, func(ctx context.Context, c *Call, in createInstanceSnapshotInput) (*Result, error) {
		acc, err := c.Client.Instances.CreateSnapshot(ctx, in.InstanceID, pantech.CreateSnapshotRequest{Name: in.Name}, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "snapshot "+acc.ResourceID+" of instance "+in.InstanceID+" taken")
	})

	add(Spec{
		Name: "get_instance_order", Title: "Get an instance order",
		Description: "Get how a new instance is being paid for and provisioned: awaiting_payment, paid, provisioning, provisioned, or payment_failed / failed with a reason. With wait, follows it (and its create operation) for up to a minute. " + exactAmounts,
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /instance-orders/{order_id}", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in orderInput) (*Result, error) {
		if err := checkIDs("order_id", in.OrderID); err != nil {
			return nil, err
		}
		o, err := c.Client.Instances.GetOrder(ctx, in.OrderID)
		if err != nil {
			return nil, err
		}
		if in.Wait && o.Status != pantech.OrderFailed && o.Status != pantech.OrderPaymentFailed {
			return c.followOrder(ctx, "instance", o.ID, o.InstanceID, func(ctx context.Context) (string, *string, *string, error) {
				o, err := c.Client.Instances.GetOrder(ctx, in.OrderID)
				if err != nil {
					return "", nil, nil, err
				}
				return o.Status, o.OperationID, o.FailureCode, nil
			})
		}
		return ok(orderText("instance", o.ID, o.InstanceID, o.Status, o.FailureCode, o.AmountMinor, o.Currency), o)
	})
}

// orderText summarises an order in our own words.
func orderText(kind, orderID, resourceID, status string, failure *string, amountMinor int64, currency string) string {
	text := fmt.Sprintf("The %s order %s for %s is %s; its initial payment is %s (exact).", kind, orderID, resourceID, arg(status), money(amountMinor, currency))
	if failure != nil && *failure != "" {
		text += " Failure: " + arg(*failure) + "; nothing was provisioned, and any payment taken is returned to your credit."
	}
	return text
}

// usageResult summarises a resource's measured usage.
func usageResult(what string, u *pantech.UsageSummary) (*Result, error) {
	return ok(fmt.Sprintf("Measured usage of %s this period (exact): %s running hours, %s storage GB-hours, %s memory MB-hours.",
		what, exactNumber(u.Totals.RunningHours), exactNumber(u.Totals.StorageGBHours), exactNumber(u.Totals.AllocatedMemoryMBHours)), u)
}

// snapshotPrice prices a snapshot of up to sizeGB, or says how it is billed.
func (c *Call) snapshotPrice(ctx context.Context, sizeGB int) string {
	price := "billed hourly on the snapshot's stored size"
	if sizeGB > 0 {
		if q, err := c.Client.Snapshots.Quote(ctx, sizeGB); err == nil && q.Priced && q.MonthlyMinor != nil && q.Currency != nil {
			price = fmt.Sprintf("at most about %s a month if it stores the full %d GB (billed on stored size)", money(*q.MonthlyMinor, *q.Currency), sizeGB)
		}
	}
	return price
}
