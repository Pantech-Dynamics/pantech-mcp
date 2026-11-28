package tools

import (
	"context"
	"fmt"
	"time"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

const (
	dynamicIPOnDemandSurcharge    = 95
	dynamicIPDefaultOnDemandLimit = 10
)

var dynamicIPSurcharge = map[string]int{
	"every_12_hours": 60,
	"daily":          50,
	"weekly":         40,
	"monthly":        30,
}

func checkDynamicIPSchedule(field, schedule string) (int, error) {
	pct, ok := dynamicIPSurcharge[schedule]
	if !ok {
		return 0, errorf("%s must be every_12_hours, daily, weekly or monthly.", field)
	}
	return pct, nil
}

func checkDynamicIPConnections(field, connections string) (string, error) {
	if connections == "" {
		return "keep", nil
	}
	if connections != "keep" && connections != "move" {
		return "", errorf("%s must be keep or move.", field)
	}
	return connections, nil
}

func dynamicIPText(dip *pantech.DynamicIP) string {
	text := fmt.Sprintf("Dynamic IP on instance %s is %s (schedule %s, connections %s, +%g%% surcharge).",
		dip.InstanceID, arg(dip.State), arg(dip.Schedule), arg(dip.Connections), dip.SurchargePercent)
	if dip.CurrentAddress != nil && *dip.CurrentAddress != "" {
		text += fmt.Sprintf(" Current address: %s.", arg(*dip.CurrentAddress))
	}
	if dip.OnDemand {
		limit := dip.OnDemandLimit
		if limit <= 0 {
			limit = dynamicIPDefaultOnDemandLimit
		}
		text += fmt.Sprintf(" On-demand rotations: %d/%d used in the last 24 hours.", dip.OnDemandUsed, limit)
	}
	if dip.NextRotationAt != nil {
		text += fmt.Sprintf(" Next rotation: %s.", dip.NextRotationAt.UTC().Format(time.RFC3339))
	}
	if dip.FailureReason != nil && *dip.FailureReason != "" {
		text += " See failure_reason in the data."
	}
	return text
}

type setInstanceDynamicIPInput struct {
	InstanceID  string `json:"instance_id" jsonschema:"The standard instance's id, e.g. vm_…"`
	Schedule    string `json:"schedule" jsonschema:"How often the rotating public address changes: every_12_hours (+60%), daily (+50%), weekly (+40%) or monthly (+30%)."`
	Connections string `json:"connections,omitempty" jsonschema:"How open outbound connections behave on rotation: keep (default: finish on the old address) or move (move at once)."`
	OnDemand    bool   `json:"on_demand,omitempty" jsonschema:"Allow rotating the address on demand (up to 10 per rolling 24 hours). Replaces the schedule surcharge with +95% on the instance's hourly price."`
	Confirmation
}

type rotateInstanceDynamicIPInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance's id, e.g. vm_…"`
	Confirmation
}

type disableInstanceDynamicIPInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance's id, e.g. vm_…"`
	Confirmation
}

func init() {
	add(Spec{
		Name: "get_instance_dynamic_ip", Title: "Get an instance's Dynamic IP",
		Description: "Get an instance's Dynamic IP configuration and status: state (enabling, active, degraded, disabling, off or failed), current rotating address, schedule, connection handling (keep or move), surcharge percentage, on-demand rotation count, next rotation time and recent events.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}/address-rotation"},
	}, func(ctx context.Context, c *Call, in instanceInput) (*Result, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		dip, err := c.Client.Instances.GetDynamicIP(ctx, in.InstanceID)
		if err != nil {
			return nil, err
		}
		return ok(dynamicIPText(dip), dip)
	})

	addConfirmed(Spec{
		Name: "set_instance_dynamic_ip", Title: "Set an instance's Dynamic IP",
		Description: "Turn on or update Dynamic IP (scheduled outbound IPv4 rotation) on a standard instance. Adds a percentage surcharge to the instance's hourly price while running: +30% (monthly), +40% (weekly), +50% (daily), +60% (every_12_hours), or +95% when on_demand is true (up to 10 manual rotations per rolling 24 hours). Requires a running standard instance with the private network attached, a custom security group allowing nothing from the private network range, and the Pantech agent.",
		Risk:        Cost, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}", "PUT /instances/{instance_id}/address-rotation"},
	}, func(ctx context.Context, c *Call, in setInstanceDynamicIPInput) (*Preview, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		schedulePct, err := checkDynamicIPSchedule("schedule", in.Schedule)
		if err != nil {
			return nil, err
		}
		conns, err := checkDynamicIPConnections("connections", in.Connections)
		if err != nil {
			return nil, err
		}
		vm, err := c.Client.Instances.Get(ctx, in.InstanceID)
		if err != nil {
			return nil, err
		}
		pct := schedulePct
		action := fmt.Sprintf("Set Dynamic IP on instance %s: schedule %s, connections %s, on-demand rotation off.", vm.ID, in.Schedule, conns)
		price := fmt.Sprintf("+%d%% surcharge on the instance's hourly price while running (%s schedule; no surcharge while stopped)", pct, in.Schedule)
		if in.OnDemand {
			pct = dynamicIPOnDemandSurcharge
			action = fmt.Sprintf("Set Dynamic IP on instance %s: schedule %s, connections %s, on-demand rotation enabled (up to %d manual rotations per rolling 24 hours).",
				vm.ID, in.Schedule, conns, dynamicIPDefaultOnDemandLimit)
			price = fmt.Sprintf("+%d%% surcharge on the instance's hourly price while running (on-demand tier replaces the +%d%% %s surcharge; no surcharge while stopped)",
				pct, schedulePct, in.Schedule)
		}
		req := pantech.PutDynamicIPRequest{
			Schedule:    in.Schedule,
			Connections: pantech.Ptr(conns),
			OnDemand:    pantech.Ptr(in.OnDemand),
		}
		p := &Preview{
			Action: action,
			Price:  price,
			Warnings: []string{
				"Requires a standard instance with the private network attached, a custom security group allowing nothing from the private network range, and the Pantech agent.",
			},
			Data: map[string]any{"instance": vm, "request": req, "surcharge_percent": pct},
		}
		if vm.ObservedState != "running" {
			p.Warnings = append(p.Warnings, "The instance is not running: start it first, or Pantech refuses.")
		}
		if vm.SubnetID != nil {
			p.Warnings = append(p.Warnings, "This instance is in a VPC: Dynamic IP is only available on standard instances.")
		}
		if conns == "move" {
			p.Warnings = append(p.Warnings, "With connections set to move, active outbound connections are moved immediately when the address rotates, which can drop open TCP sessions.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in setInstanceDynamicIPInput) (*Result, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		if _, err := checkDynamicIPSchedule("schedule", in.Schedule); err != nil {
			return nil, err
		}
		conns, err := checkDynamicIPConnections("connections", in.Connections)
		if err != nil {
			return nil, err
		}
		req := pantech.PutDynamicIPRequest{
			Schedule:    in.Schedule,
			Connections: pantech.Ptr(conns),
			OnDemand:    pantech.Ptr(in.OnDemand),
		}
		dip, err := c.Client.Instances.SetDynamicIP(ctx, in.InstanceID, req, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return ok(dynamicIPText(dip), dip)
	})

	addConfirmed(Spec{
		Name: "rotate_instance_dynamic_ip", Title: "Rotate an instance's Dynamic IP now",
		Description: "Rotate an instance's Dynamic IP to a new outbound IPv4 address immediately. Requires Dynamic IP with on_demand enabled and consumes 1 of the 10 on-demand rotations allowed per rolling 24 hours. Non-replaying: do not retry automatically if a request times out; check get_instance_dynamic_ip first.",
		Risk:        Destructive, Destructive: true, Idempotent: false,
		Routes: []string{"GET /instances/{instance_id}/address-rotation", "POST /instances/{instance_id}/address-rotation/rotate"},
	}, func(ctx context.Context, c *Call, in rotateInstanceDynamicIPInput) (*Preview, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		dip, err := c.Client.Instances.GetDynamicIP(ctx, in.InstanceID)
		if err != nil {
			return nil, err
		}
		cur := "(none yet)"
		if dip.CurrentAddress != nil && *dip.CurrentAddress != "" {
			cur = arg(*dip.CurrentAddress)
		}
		limit := dip.OnDemandLimit
		if limit <= 0 {
			limit = dynamicIPDefaultOnDemandLimit
		}
		p := &Preview{
			Action: fmt.Sprintf("Rotate the Dynamic IP of instance %s immediately from %s to a new outbound IPv4 address (on-demand rotations used: %d/%d per rolling 24 hours).",
				dip.InstanceID, cur, dip.OnDemandUsed, limit),
			Warnings: []string{
				fmt.Sprintf("Non-replaying: this uses 1 of the %d on-demand rotations allowed per rolling 24 hours. If the request times out, check get_instance_dynamic_ip before trying again.", limit),
			},
			Data: map[string]any{"dynamic_ip": dip},
		}
		if !dip.OnDemand {
			p.Warnings = append(p.Warnings, "On-demand rotation is off for this instance: Pantech will refuse (ADDRESS_ROTATION_ON_DEMAND_OFF) until on_demand is enabled with set_instance_dynamic_ip.")
		}
		if dip.OnDemandUsed >= limit {
			p.Warnings = append(p.Warnings, fmt.Sprintf("On-demand rotation limit reached (%d/%d in the last 24 hours): Pantech will refuse (ADDRESS_ROTATION_LIMIT_REACHED) until an earlier rotation ages past 24 hours.",
				dip.OnDemandUsed, limit))
		}
		if dip.Connections == "move" {
			p.Warnings = append(p.Warnings, "Connections is set to move: active outbound connections will move immediately, which can drop open TCP sessions.")
		}
		if dip.State != "active" {
			p.Warnings = append(p.Warnings, fmt.Sprintf("Dynamic IP state is currently %s; rotation may be refused until it is active.", arg(dip.State)))
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in rotateInstanceDynamicIPInput) (*Result, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		dip, err := c.Client.Instances.RotateDynamicIP(ctx, in.InstanceID, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return ok("Rotated Dynamic IP. "+dynamicIPText(dip), dip)
	})

	addConfirmed(Spec{
		Name: "disable_instance_dynamic_ip", Title: "Turn off an instance's Dynamic IP",
		Description: "Turn off Dynamic IP on an instance. Outbound traffic returns to the instance's static public IPv4 address and the Dynamic IP surcharge stops once turned off. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}/address-rotation", "DELETE /instances/{instance_id}/address-rotation"},
	}, func(ctx context.Context, c *Call, in disableInstanceDynamicIPInput) (*Preview, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		dip, err := c.Client.Instances.GetDynamicIP(ctx, in.InstanceID)
		if err != nil {
			return nil, err
		}
		action := fmt.Sprintf("Turn off Dynamic IP on instance %s (state %s, schedule %s). Outbound traffic will return to the instance's static public IPv4 address and the Dynamic IP surcharge will stop.",
			dip.InstanceID, arg(dip.State), arg(dip.Schedule))
		if dip.CurrentAddress != nil && *dip.CurrentAddress != "" {
			action = fmt.Sprintf("Turn off Dynamic IP on instance %s (currently %s, schedule %s, +%g%% surcharge). Outbound traffic will return to the instance's static public IPv4 address and the Dynamic IP surcharge will stop.",
				dip.InstanceID, arg(*dip.CurrentAddress), arg(dip.Schedule), dip.SurchargePercent)
		}
		return &Preview{
			Action:   action,
			Warnings: []string{deleteAccessNote},
			Data:     map[string]any{"dynamic_ip": dip},
		}, nil
	}, func(ctx context.Context, c *Call, in disableInstanceDynamicIPInput) (*Result, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		dip, err := c.Client.Instances.DisableDynamicIP(ctx, in.InstanceID, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return ok("Disabled Dynamic IP. "+dynamicIPText(dip), dip)
	})
}
