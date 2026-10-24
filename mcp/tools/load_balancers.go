package tools

import (
	"context"
	"fmt"

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
		Routes:      []string{"POST /load-balancers", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in createLoadBalancerInput) (*Preview, error) {
		p := &Preview{
			Action: fmt.Sprintf("Create a %s load balancer on public port %d spreading traffic across %s.",
				arg(valueOr(in.Kind, "vpc_native")), in.PublicPort, plural(len(in.InstanceIDs), "instance", "instances")),
			Price: "not quoted by the API: a vpc_native load balancer uses the public IP you already pay for; an appliance load balancer is billed as a VM (see list_plans)",
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
