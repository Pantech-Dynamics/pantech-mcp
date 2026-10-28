package tools

import (
	"context"
	"fmt"
	"strings"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

type listLoadBalancersInput struct {
	NetworkID  string `json:"network_id,omitempty" jsonschema:"Only the load balancers of this VPC."`
	PublicIPID string `json:"public_ip_id,omitempty" jsonschema:"Only the load balancers on this public IP."`
}

type createLoadBalancerInput struct {
	Name        string   `json:"name" jsonschema:"1 to 63 characters."`
	Kind        string   `json:"kind,omitempty" jsonschema:"vpc_native (a VPC zone: needs public_ip_id and subnet_id) or appliance (a standard zone: needs zone_id; a load balancer VM with its own public address, billed as that VM)."`
	ZoneID      string   `json:"zone_id,omitempty" jsonschema:"The zone, for appliance load balancers."`
	PublicIPID  string   `json:"public_ip_id,omitempty" jsonschema:"An active public IP with purpose load_balancer (vpc_native)."`
	SubnetID    string   `json:"subnet_id,omitempty" jsonschema:"A subnet of the public IP's VPC."`
	Algorithm   string   `json:"algorithm,omitempty" jsonschema:"roundrobin (default), leastconn or source."`
	PublicPort  int      `json:"public_port" jsonschema:"The TCP port clients connect to."`
	PrivatePort int      `json:"private_port,omitempty" jsonschema:"The port on each target (default: public_port)."`
	CIDRList    []string `json:"cidr_list,omitempty" jsonschema:"Source IPv4 CIDRs allowed to connect (default: anywhere)."`
	InstanceIDs []string `json:"instance_ids,omitempty" jsonschema:"Instances to spread traffic across."`
	Confirmation
}

func init() {
	add(Spec{
		Name: "list_load_balancers", Title: "List load balancers",
		Description: "List the organization's load balancers: their public IP and port, targets and state.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /load-balancers"},
	}, func(ctx context.Context, c *Call, in listLoadBalancersInput) (*Result, error) {
		if err := checkOptionalIDs("network_id", in.NetworkID, "public_ip_id", in.PublicIPID); err != nil {
			return nil, err
		}
		lbs, err := c.Client.LoadBalancers.List(ctx, &pantech.ListLoadBalancersParams{NetworkID: in.NetworkID, PublicIPID: in.PublicIPID})
		if err != nil {
			return nil, err
		}
		return ok(plural(len(lbs), "load balancer", "load balancers")+".", lbs)
	})

	addConfirmed(Spec{
		Name: "create_load_balancer", Title: "Create a load balancer",
		Description: "Spread one TCP port of a public address across instances. The port becomes reachable from the internet (limit it with cidr_list). An appliance load balancer is a VM billed as such.",
		Risk:        Cost,
		Routes:      []string{"POST /load-balancers", "GET /operations/{operation_id}", "GET /billing/credit", "GET /plans", "POST /pricing/estimate"},
	}, func(ctx context.Context, c *Call, in createLoadBalancerInput) (*Preview, error) {
		price := "no extra charge: a vpc_native load balancer uses the public IP you already pay for"
		if in.Kind == "appliance" {
			price = c.appliancePrice(ctx)
		}
		p := &Preview{
			Action: fmt.Sprintf("Create a %s load balancer on public port %d spreading traffic across %s.",
				arg(valueOr(in.Kind, "vpc_native")), in.PublicPort, plural(len(in.InstanceIDs), "instance", "instances")),
			Price: price,
			Data:  map[string]any{"request": in.request()},
		}
		if len(in.CIDRList) == 0 {
			p.Warnings = append(p.Warnings, "No cidr_list: anyone on the internet can connect to the port.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in createLoadBalancerInput) (*Result, error) {
		acc, err := c.Client.LoadBalancers.Create(ctx, in.request(), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "load balancer "+acc.ResourceID+" created")
	})
}

func (in createLoadBalancerInput) request() pantech.CreateLoadBalancerRequest {
	req := pantech.CreateLoadBalancerRequest{Name: in.Name, PublicPort: in.PublicPort}
	set := func(dst **string, v string) {
		if v != "" {
			*dst = pantech.Ptr(v)
		}
	}
	set(&req.Kind, in.Kind)
	set(&req.ZoneID, in.ZoneID)
	set(&req.PublicIPID, in.PublicIPID)
	set(&req.SubnetID, in.SubnetID)
	set(&req.Algorithm, in.Algorithm)
	if in.PrivatePort != 0 {
		req.PrivatePort = pantech.Ptr(in.PrivatePort)
	}
	if len(in.CIDRList) > 0 {
		req.CIDRList = &in.CIDRList
	}
	if len(in.InstanceIDs) > 0 {
		req.InstanceIDs = &in.InstanceIDs
	}
	return req
}

type loadBalancerInput struct {
	LoadBalancerID string `json:"load_balancer_id" jsonschema:"The load balancer's id, e.g. lb_…"`
	Confirmation
}

type updateLoadBalancerInput struct {
	LoadBalancerID string    `json:"load_balancer_id" jsonschema:"The load balancer's id."`
	Name           string    `json:"name,omitempty" jsonschema:"A new name, 1 to 63 characters."`
	Algorithm      string    `json:"algorithm,omitempty" jsonschema:"roundrobin, leastconn or source."`
	InstanceIDs    *[]string `json:"instance_ids,omitempty" jsonschema:"The COMPLETE new set of target instances (at most 50): it replaces every current target. Include the ones to keep; [] removes them all. Leave it out to keep the targets."`
	Confirmation
}

func init() {
	addConfirmed(Spec{
		Name: "update_load_balancer", Title: "Change a load balancer",
		Description: "Rename a load balancer, change its algorithm, or replace its targets: instance_ids, when given (even empty), is the whole new set. Ports, public IP, subnet and cidr_list cannot change. Get the current targets with list_load_balancers first.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /load-balancers/{load_balancer_id}", "PATCH /load-balancers/{load_balancer_id}", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in updateLoadBalancerInput) (*Preview, error) {
		if err := checkIDs("load_balancer_id", in.LoadBalancerID); err != nil {
			return nil, err
		}
		if in.Name == "" && in.Algorithm == "" && in.InstanceIDs == nil {
			return nil, errorf("Nothing to change: give name, algorithm or instance_ids.")
		}
		lb, err := c.Client.LoadBalancers.Get(ctx, in.LoadBalancerID)
		if err != nil {
			return nil, err
		}
		p := &Preview{Data: map[string]any{"load_balancer": lb, "request": in.request()}}
		var changes []string
		if in.Name != "" {
			changes = append(changes, "rename it")
		}
		if in.Algorithm != "" {
			changes = append(changes, fmt.Sprintf("change its algorithm from %s to %s", arg(lb.Algorithm), arg(in.Algorithm)))
		}
		if in.InstanceIDs != nil {
			var current []string
			for _, m := range lb.Members {
				if m.DesiredState != "deleted" {
					current = append(current, m.InstanceID)
				}
			}
			added, removed := diffStrings(current, *in.InstanceIDs)
			changes = append(changes, fmt.Sprintf("replace its %s with %s (%d added, %d removed)",
				plural(len(current), "target", "targets"), plural(len(*in.InstanceIDs), "target", "targets"), len(added), len(removed)))
			p.Data = map[string]any{"load_balancer": lb, "request": in.request(), "added": added, "removed": removed}
			if len(removed) > 0 {
				p.Warnings = append(p.Warnings, "Removed targets stop receiving traffic once the change is done.")
			}
			if len(*in.InstanceIDs) == 0 {
				p.Warnings = append(p.Warnings, "No targets: the load balancer will serve nothing until targets are added.")
			}
		}
		p.Action = fmt.Sprintf("Change load balancer %s: %s.", lb.ID, strings.Join(changes, "; "))
		return p, nil
	}, func(ctx context.Context, c *Call, in updateLoadBalancerInput) (*Result, error) {
		acc, err := c.Client.LoadBalancers.Update(ctx, in.LoadBalancerID, in.request(), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "load balancer "+in.LoadBalancerID+" changed")
	})

	addConfirmed(Spec{
		Name: "delete_load_balancer", Title: "Delete a load balancer",
		Description: "Delete a load balancer. Traffic to its port stops; the instances and the public IP are kept. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /load-balancers/{load_balancer_id}", "DELETE /load-balancers/{load_balancer_id}", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in loadBalancerInput) (*Preview, error) {
		if err := checkIDs("load_balancer_id", in.LoadBalancerID); err != nil {
			return nil, err
		}
		lb, err := c.Client.LoadBalancers.Get(ctx, in.LoadBalancerID)
		if err != nil {
			return nil, err
		}
		return &Preview{
			Action:   fmt.Sprintf("Permanently delete load balancer %s (public port %d, %s). Traffic to that port stops; the instances and the public IP are kept.", lb.ID, lb.PublicPort, plural(len(lb.Members), "target", "targets")),
			Warnings: []string{deleteAccessNote},
			Data:     map[string]any{"load_balancer": lb},
		}, nil
	}, func(ctx context.Context, c *Call, in loadBalancerInput) (*Result, error) {
		acc, err := c.Client.LoadBalancers.Delete(ctx, in.LoadBalancerID, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "load balancer "+in.LoadBalancerID+" deleted")
	})
}

func (in updateLoadBalancerInput) request() pantech.UpdateLoadBalancerRequest {
	req := pantech.UpdateLoadBalancerRequest{InstanceIDs: in.InstanceIDs}
	if in.Name != "" {
		req.Name = pantech.Ptr(in.Name)
	}
	if in.Algorithm != "" {
		req.Algorithm = pantech.Ptr(in.Algorithm)
	}
	return req
}

// The appliance VM's size. The platform bills an appliance load balancer as
// exactly these, plus its public IPv4, every hour it exists (its billable
// hours use the same meters as a machine), so a machine estimate of this size
// is its price.
const (
	applianceVCPU     = 1
	applianceMemoryMB = 1024
	applianceDiskGB   = 25
)

// appliancePrice estimates an appliance load balancer's month: a 1 vCPU,
// 1 GB plan in a standard zone running all month, plus the root disk the
// plan lacks. Best effort: without a price it says how it is billed.
func (c *Call) appliancePrice(ctx context.Context) string {
	const unquoted = "billed as a 1 vCPU, 1 GB VM with a 25 GB disk and a public IPv4, every hour it exists"
	credit, err := c.Client.Billing.GetCredit(ctx)
	if err != nil {
		return unquoted
	}
	plans, err := c.Client.Catalog.ListPlans(ctx, &pantech.ListPlansParams{Placement: "standard"})
	if err != nil {
		return unquoted
	}
	var plan *pantech.Plan
	for i := range plans {
		p := &plans[i]
		if p.VCPU == applianceVCPU && p.MemoryMB == applianceMemoryMB && p.DiskGB <= applianceDiskGB &&
			(plan == nil || p.DiskGB > plan.DiskGB) {
			plan = p
		}
	}
	if plan == nil {
		return unquoted
	}
	req := pantech.PricingEstimateRequest{Currency: credit.Currency, Machines: &[]pantech.PricingEstimateMachineRequest{{
		PlanSlug: plan.Slug, Quantity: 1, RunningHours: 730, Placement: pantech.Ptr("standard"),
	}}}
	if extra := applianceDiskGB - plan.DiskGB; extra > 0 {
		req.ExtraStorageGB = pantech.Ptr(extra)
	}
	est, err := c.Client.Catalog.EstimatePrice(ctx, req)
	if err != nil || !est.PricingComplete {
		return unquoted
	}
	return fmt.Sprintf("about %s a month (an estimate: %s, running all month)", money(est.PricedSubtotalMinor, est.Currency), unquoted)
}
