package tools

import (
	"context"
	"fmt"
	"strings"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

type createNetworkInput struct {
	Name   string `json:"name" jsonschema:"The VPC's name."`
	CIDR   string `json:"cidr" jsonschema:"Its private IPv4 range, e.g. 10.0.0.0/16."`
	Region string `json:"region,omitempty" jsonschema:"A region code from list_regions."`
	Confirmation
}

type listSubnetsInput struct {
	NetworkID string `json:"network_id" jsonschema:"The VPC's id."`
	listInput
}

type createSubnetInput struct {
	NetworkID string `json:"network_id" jsonschema:"The VPC's id."`
	Name      string `json:"name" jsonschema:"The subnet's name."`
	CIDR      string `json:"cidr" jsonschema:"A range inside the VPC's CIDR, e.g. 10.0.1.0/24."`
}

type subnetInput struct {
	SubnetID string `json:"subnet_id" jsonschema:"The subnet's id."`
}

type addFirewallRuleInput struct {
	SubnetID  string `json:"subnet_id" jsonschema:"The subnet the rule protects."`
	Number    int    `json:"number" jsonschema:"Evaluation order, 100 to 9999, unique on the subnet; lower numbers are evaluated first and the first match decides."`
	Direction string `json:"direction,omitempty" jsonschema:"ingress (into the subnet, the default) or egress."`
	Protocol  string `json:"protocol" jsonschema:"tcp, udp, icmp or all."`
	PortStart int    `json:"port_start,omitempty" jsonschema:"First port, 1 to 65535 (tcp/udp)."`
	PortEnd   int    `json:"port_end,omitempty" jsonschema:"Last port, at least port_start (tcp/udp)."`
	ICMPType  *int   `json:"icmp_type,omitempty" jsonschema:"ICMP type, for icmp only, e.g. 8 for echo request."`
	ICMPCode  *int   `json:"icmp_code,omitempty" jsonschema:"ICMP code, for icmp only."`
	CIDR      string `json:"cidr" jsonschema:"One IPv4 CIDR: the source for ingress, the destination for egress. Prefer the narrowest range (e.g. the user's own IP as /32) over 0.0.0.0/0."`
	Action    string `json:"action,omitempty" jsonschema:"allow (default) or deny."`
	Confirmation
}

type deleteFirewallRuleInput struct {
	RuleID   string `json:"rule_id" jsonschema:"The firewall rule's id."`
	SubnetID string `json:"subnet_id" jsonschema:"The subnet the rule is on (to show it in the preview)."`
	Confirmation
}

func init() {
	add(Spec{
		Name: "list_networks", Title: "List VPC networks",
		Description: "List the organization's VPC networks (private networks) with their CIDR, region and state.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /networks"},
	}, func(ctx context.Context, c *Call, in listInput) (*Result, error) {
		page, err := c.Client.Networks.List(ctx, in.params())
		if err != nil {
			return nil, err
		}
		return pageResult(page, "network", "networks")
	})

	addConfirmed(Spec{
		Name: "create_network", Title: "Create a VPC network",
		Description: "Create a VPC: a private network with a CIDR such as 10.0.0.0/16. Then add subnets, and instances or databases to them. A VPC is billed monthly.",
		Risk:        Cost,
		Routes:      []string{"GET /networks/quote", "POST /networks", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in createNetworkInput) (*Preview, error) {
		q, err := c.Client.Networks.Quote(ctx)
		if err != nil {
			return nil, err
		}
		return &Preview{
			Action: fmt.Sprintf("Create a VPC network with range %s.", arg(in.CIDR)),
			Price:  money(q.MonthlyMinor, q.Currency) + " a month",
			Data:   map[string]any{"request": in.request(), "quote": q},
		}, nil
	}, func(ctx context.Context, c *Call, in createNetworkInput) (*Result, error) {
		acc, err := c.Client.Networks.Create(ctx, in.request(), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "network "+acc.ResourceID+" created")
	})

	add(Spec{
		Name: "list_subnets", Title: "List subnets",
		Description: "List the subnets of a VPC network.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /networks/{network_id}/subnets"},
	}, func(ctx context.Context, c *Call, in listSubnetsInput) (*Result, error) {
		if err := checkIDs("network_id", in.NetworkID); err != nil {
			return nil, err
		}
		page, err := c.Client.Subnets.List(ctx, in.NetworkID, in.params())
		if err != nil {
			return nil, err
		}
		return pageResult(page, "subnet", "subnets")
	})

	add(Spec{
		Name: "create_subnet", Title: "Create a subnet",
		Description: "Create a subnet inside a VPC's range, such as 10.0.1.0/24. Instances and databases are placed in subnets; each subnet has its own firewall rules.",
		Risk:        Write,
		Routes:      []string{"POST /networks/{network_id}/subnets", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in createSubnetInput) (*Result, error) {
		if err := checkIDs("network_id", in.NetworkID); err != nil {
			return nil, err
		}
		acc, err := c.Client.Subnets.Create(ctx, in.NetworkID, pantech.CreateSubnetRequest{Name: in.Name, CIDR: in.CIDR}, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "subnet "+acc.ResourceID+" created")
	})

	add(Spec{
		Name: "list_firewall_rules", Title: "List firewall rules",
		Description: "List a subnet's firewall rules, in evaluation order, plus the system rules that always apply.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /subnets/{subnet_id}/firewall-rules"},
	}, func(ctx context.Context, c *Call, in subnetInput) (*Result, error) {
		if err := checkIDs("subnet_id", in.SubnetID); err != nil {
			return nil, err
		}
		list, err := c.Client.FirewallRules.List(ctx, in.SubnetID)
		if err != nil {
			return nil, err
		}
		return ok(plural(len(list.Data), "firewall rule", "firewall rules")+fmt.Sprintf(" on subnet %s.", in.SubnetID), list)
	})

	addConfirmed(Spec{
		Name: "add_firewall_rule", Title: "Add a firewall rule",
		Description: "Add a rule to a subnet's firewall. An allow rule opens a path to the servers in the subnet, so it is two-step.",
		Risk:        Exposure,
		Routes:      []string{"POST /subnets/{subnet_id}/firewall-rules", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in addFirewallRuleInput) (*Preview, error) {
		if err := checkIDs("subnet_id", in.SubnetID); err != nil {
			return nil, err
		}
		req := in.request()
		p := &Preview{
			Action: fmt.Sprintf("Add firewall rule %d on subnet %s: %s %s %s %s.", req.Number, in.SubnetID,
				arg(valueOr(in.Action, "allow")), arg(valueOr(in.Direction, "ingress")), describePorts(in.Protocol, in.PortStart, in.PortEnd), cidrPhrase(valueOr(in.Direction, "ingress"), in.CIDR)),
			Data: map[string]any{"request": req},
		}
		if valueOr(in.Action, "allow") == "allow" && isAnywhere(in.CIDR) {
			p.Warnings = append(p.Warnings, "This allows the whole internet. Prefer the user's own address (/32) unless the service is meant to be public.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in addFirewallRuleInput) (*Result, error) {
		acc, err := c.Client.FirewallRules.Create(ctx, in.SubnetID, in.request(), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, fmt.Sprintf("firewall rule %s added on subnet %s", acc.ResourceID, in.SubnetID))
	})

	addConfirmed(Spec{
		Name: "delete_firewall_rule", Title: "Delete a firewall rule",
		Description: "Delete one subnet firewall rule. Traffic it allowed is dropped unless another rule allows it. Needs a key with delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /subnets/{subnet_id}/firewall-rules/{rule_id}", "DELETE /firewall-rules/{rule_id}", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in deleteFirewallRuleInput) (*Preview, error) {
		if err := checkIDs("rule_id", in.RuleID, "subnet_id", in.SubnetID); err != nil {
			return nil, err
		}
		rule, err := c.Client.FirewallRules.Get(ctx, in.SubnetID, in.RuleID)
		if err != nil {
			return nil, err
		}
		return &Preview{
			Action: fmt.Sprintf("Delete firewall rule %s (number %d: %s %s %s %s) from subnet %s.", rule.ID, rule.Number,
				rule.Action, rule.Direction, describePorts(rule.Protocol, deref(rule.PortStart), deref(rule.PortEnd)), cidrPhrase(rule.Direction, rule.CIDR), in.SubnetID),
			Warnings: []string{"Traffic this rule allowed is dropped once the change is done, unless another rule allows it."},
			Data:     map[string]any{"rule": rule},
		}, nil
	}, func(ctx context.Context, c *Call, in deleteFirewallRuleInput) (*Result, error) {
		acc, err := c.Client.FirewallRules.Delete(ctx, in.RuleID, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "firewall rule "+in.RuleID+" deleted")
	})
}

func (in createNetworkInput) request() pantech.CreateNetworkRequest {
	req := pantech.CreateNetworkRequest{Name: in.Name, CIDR: in.CIDR}
	if in.Region != "" {
		req.Region = pantech.Ptr(in.Region)
	}
	return req
}

func (in addFirewallRuleInput) request() pantech.CreateFirewallRuleRequest {
	req := pantech.CreateFirewallRuleRequest{Number: in.Number, Protocol: in.Protocol, CIDR: in.CIDR, ICMPType: in.ICMPType, ICMPCode: in.ICMPCode}
	if in.Direction != "" {
		req.Direction = pantech.Ptr(in.Direction)
	}
	if in.PortStart != 0 {
		req.PortStart = pantech.Ptr(in.PortStart)
	}
	if in.PortEnd != 0 {
		req.PortEnd = pantech.Ptr(in.PortEnd)
	}
	if in.Action != "" {
		req.Action = pantech.Ptr(in.Action)
	}
	return req
}

func valueOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func describePorts(protocol string, start, end int) string {
	switch {
	case start == 0:
		return arg(protocol)
	case end == 0 || end == start:
		return fmt.Sprintf("%s port %d", arg(protocol), start)
	default:
		return fmt.Sprintf("%s ports %d-%d", arg(protocol), start, end)
	}
}

func cidrPhrase(direction, cidr string) string {
	if direction == "egress" {
		return "to " + arg(cidr)
	}
	return "from " + arg(cidr)
}

func isAnywhere(cidr string) bool {
	return strings.HasSuffix(strings.TrimSpace(cidr), "/0")
}
