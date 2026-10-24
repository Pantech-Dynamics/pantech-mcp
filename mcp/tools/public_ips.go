package tools

import (
	"context"
	"fmt"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

type listPublicIPsInput struct {
	NetworkID string `json:"network_id,omitempty" jsonschema:"Only the public IPs of this VPC."`
}

type createPublicIPInput struct {
	NetworkID  string `json:"network_id" jsonschema:"The VPC network to allocate the address in."`
	Purpose    string `json:"purpose,omitempty" jsonschema:"static_nat (default: every port to one instance), port_forwarding (per-port rules) or load_balancer."`
	InstanceID string `json:"instance_id,omitempty" jsonschema:"For static_nat only: an instance in network_id without a public IP."`
	Confirmation
}

type attachPublicIPInput struct {
	PublicIPID string `json:"public_ip_id" jsonschema:"The static NAT public IP's id."`
	InstanceID string `json:"instance_id" jsonschema:"An instance in the same VPC, without a static NAT address of its own."`
	Confirmation
}

type publicIPInput struct {
	PublicIPID string `json:"public_ip_id" jsonschema:"The public IP's id."`
}

type createPortForwardingInput struct {
	PublicIPID       string `json:"public_ip_id" jsonschema:"A port_forwarding public IP's id."`
	InstanceID       string `json:"instance_id" jsonschema:"The instance in the IP's VPC to forward to."`
	Protocol         string `json:"protocol" jsonschema:"tcp or udp."`
	PublicPortStart  int    `json:"public_port_start" jsonschema:"First public port."`
	PublicPortEnd    int    `json:"public_port_end,omitempty" jsonschema:"Last public port (default: public_port_start)."`
	PrivatePortStart int    `json:"private_port_start,omitempty" jsonschema:"First port on the instance (default: the public one)."`
	PrivatePortEnd   int    `json:"private_port_end,omitempty" jsonschema:"Last port on the instance."`
	Confirmation
}

func init() {
	add(Spec{
		Name: "list_public_ips", Title: "List public IPs",
		Description: "List the organization's public IPv4 addresses, what they point at (instance, port forwarding or load balancer) and their state.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /public-ips"},
	}, func(ctx context.Context, c *Call, in listPublicIPsInput) (*Result, error) {
		if err := checkOptionalIDs("network_id", in.NetworkID); err != nil {
			return nil, err
		}
		ips, err := c.Client.PublicIPs.List(ctx, &pantech.ListPublicIPsParams{NetworkID: in.NetworkID})
		if err != nil {
			return nil, err
		}
		return ok(plural(len(ips), "public IP", "public IPs")+".", ips)
	})

	addConfirmed(Spec{
		Name: "create_public_ip", Title: "Allocate a public IP",
		Description: "Allocate a public IPv4 address for a VPC. static_nat maps every port to one instance; port_forwarding carries per-port rules; load_balancer carries load balancers. A public IP is billed monthly, and it makes servers reachable from the internet.",
		Risk:        Cost,
		Routes:      []string{"GET /public-ips/quote", "POST /public-ips", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in createPublicIPInput) (*Preview, error) {
		if err := checkIDs("network_id", in.NetworkID); err != nil {
			return nil, err
		}
		if err := checkOptionalIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		q, err := c.Client.PublicIPs.Quote(ctx)
		if err != nil {
			return nil, err
		}
		action := fmt.Sprintf("Allocate a %s public IPv4 address in network %s", arg(valueOr(in.Purpose, "static_nat")), in.NetworkID)
		if in.InstanceID != "" {
			action += " pointing every port at instance " + in.InstanceID
		}
		return &Preview{
			Action:   action + ".",
			Price:    money(q.MonthlyMinor, q.Currency) + " a month",
			Warnings: []string{"The address is reachable from the internet; the subnet's firewall rules decide what gets through."},
			Data:     map[string]any{"request": in.request(), "quote": q},
		}, nil
	}, func(ctx context.Context, c *Call, in createPublicIPInput) (*Result, error) {
		acc, err := c.Client.PublicIPs.Create(ctx, in.request(), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "public IP "+acc.ResourceID+" allocated")
	})

	addConfirmed(Spec{
		Name: "attach_public_ip", Title: "Attach a public IP",
		Description: "Point a static NAT public IP at an instance in the same VPC, keeping the address (it moves if attached elsewhere). This exposes the instance to the internet.",
		Risk:        Exposure, Idempotent: true,
		Routes: []string{"POST /public-ips/{public_ip_id}/attach", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in attachPublicIPInput) (*Preview, error) {
		if err := checkIDs("public_ip_id", in.PublicIPID, "instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		return &Preview{
			Action:   fmt.Sprintf("Point public IP %s at instance %s, so every port of the address reaches it (if the IP is attached elsewhere, it moves).", in.PublicIPID, in.InstanceID),
			Warnings: []string{"The instance becomes reachable from the internet; the subnet's firewall rules decide what gets through."},
		}, nil
	}, func(ctx context.Context, c *Call, in attachPublicIPInput) (*Result, error) {
		acc, err := c.Client.PublicIPs.Attach(ctx, in.PublicIPID, pantech.AttachPublicIPRequest{InstanceID: in.InstanceID}, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, fmt.Sprintf("public IP %s attached to instance %s", in.PublicIPID, in.InstanceID))
	})

	add(Spec{
		Name: "detach_public_ip", Title: "Detach a public IP",
		Description: "Unmap a static NAT public IP from its instance. The address stays allocated (and billed) until attached elsewhere or released; the instance keeps running without a public address.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"POST /public-ips/{public_ip_id}/detach", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in publicIPInput) (*Result, error) {
		if err := checkIDs("public_ip_id", in.PublicIPID); err != nil {
			return nil, err
		}
		acc, err := c.Client.PublicIPs.Detach(ctx, in.PublicIPID, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "public IP "+in.PublicIPID+" detached")
	})

	addConfirmed(Spec{
		Name: "create_port_forwarding_rule", Title: "Add a port forwarding rule",
		Description: "Forward a port or range of a port_forwarding public IP to an instance in its VPC. This opens the port to the internet.",
		Risk:        Exposure,
		Routes:      []string{"POST /public-ips/{public_ip_id}/port-forwarding-rules", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in createPortForwardingInput) (*Preview, error) {
		if err := checkIDs("public_ip_id", in.PublicIPID, "instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		return &Preview{
			Action:   fmt.Sprintf("Forward %s on public IP %s to instance %s.", describePorts(in.Protocol, in.PublicPortStart, in.PublicPortEnd), in.PublicIPID, in.InstanceID),
			Warnings: []string{"Anyone on the internet can reach that port unless the subnet's firewall rules block them."},
			Data:     map[string]any{"request": in.request()},
		}, nil
	}, func(ctx context.Context, c *Call, in createPortForwardingInput) (*Result, error) {
		acc, err := c.Client.PortForwardingRules.Create(ctx, in.PublicIPID, in.request(), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, fmt.Sprintf("port forwarding rule %s added on public IP %s", acc.ResourceID, in.PublicIPID))
	})
}

func (in createPublicIPInput) request() pantech.CreatePublicIPRequest {
	req := pantech.CreatePublicIPRequest{NetworkID: in.NetworkID}
	if in.Purpose != "" {
		req.Purpose = pantech.Ptr(in.Purpose)
	}
	if in.InstanceID != "" {
		req.InstanceID = pantech.Ptr(in.InstanceID)
	}
	return req
}

func (in createPortForwardingInput) request() pantech.CreatePortForwardingRuleRequest {
	req := pantech.CreatePortForwardingRuleRequest{InstanceID: in.InstanceID, Protocol: in.Protocol, PublicPortStart: in.PublicPortStart}
	if in.PublicPortEnd != 0 {
		req.PublicPortEnd = pantech.Ptr(in.PublicPortEnd)
	}
	if in.PrivatePortStart != 0 {
		req.PrivatePortStart = pantech.Ptr(in.PrivatePortStart)
	}
	if in.PrivatePortEnd != 0 {
		req.PrivatePortEnd = pantech.Ptr(in.PrivatePortEnd)
	}
	return req
}
