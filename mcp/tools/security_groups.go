package tools

import (
	"context"
	"fmt"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

type securityGroupRule struct {
	Direction string `json:"direction" jsonschema:"ingress (into the instance) or egress (out of it)."`
	Protocol  string `json:"protocol" jsonschema:"tcp, udp, icmp or all."`
	PortRange string `json:"port_range,omitempty" jsonschema:"For tcp/udp: one port (22) or an inclusive range (8000-8100)."`
	CIDR      string `json:"cidr" jsonschema:"One IPv4 CIDR: the source for ingress, the destination for egress, e.g. 203.0.113.4/32."`
}

type updateSecurityGroupRulesInput struct {
	SecurityGroupID string              `json:"security_group_id" jsonschema:"The security group's id."`
	Rules           []securityGroupRule `json:"rules" jsonschema:"The COMPLETE new rule list: it replaces every current rule. Include the rules to keep. At least one."`
	Confirmation
}

func init() {
	add(Spec{
		Name: "list_security_groups", Title: "List security groups",
		Description: "List the security groups (instance-level firewalls for standard instances) with their rules.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /security-groups"},
	}, func(ctx context.Context, c *Call, in listInput) (*Result, error) {
		page, err := c.Client.SecurityGroups.List(ctx, in.params())
		if err != nil {
			return nil, err
		}
		return pageResult(page, "security group", "security groups")
	})

	addConfirmed(Spec{
		Name: "update_security_group_rules", Title: "Replace a security group's rules",
		Description: "Replace a security group's complete rule list, for every instance (and database) using it. Inbound traffic no rule allows is dropped, so leaving a rule out removes it. Get the current rules with list_security_groups first.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /security-groups/{security_group_id}", "PUT /security-groups/{security_group_id}/rules", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in updateSecurityGroupRulesInput) (*Preview, error) {
		if err := checkIDs("security_group_id", in.SecurityGroupID); err != nil {
			return nil, err
		}
		sg, err := c.Client.SecurityGroups.Get(ctx, in.SecurityGroupID)
		if err != nil {
			return nil, err
		}
		added, removed := diffSGRules(sg.Rules, in.request().Rules)
		p := &Preview{
			Action: fmt.Sprintf("Replace the %s of security group %s with %s: %d added, %d removed. Every instance and database using the group is updated.",
				plural(len(sg.Rules), "rule", "rules"), sg.ID, plural(len(in.Rules), "rule", "rules"), len(added), len(removed)),
			Data: map[string]any{"current_rules": sg.Rules, "new_rules": in.Rules, "added": added, "removed": removed},
		}
		if len(removed) > 0 {
			p.Warnings = append(p.Warnings, "Traffic the removed rules allowed will be dropped. Make sure the rule the user connects through (e.g. SSH on 22) is kept.")
		}
		for _, r := range in.Rules {
			if r.Direction == "ingress" && isAnywhere(r.CIDR) {
				p.Warnings = append(p.Warnings, fmt.Sprintf("A new rule allows %s from the whole internet.", describeRange(r)))
			}
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in updateSecurityGroupRulesInput) (*Result, error) {
		acc, err := c.Client.SecurityGroups.ReplaceRules(ctx, in.SecurityGroupID, in.request(), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "security group "+in.SecurityGroupID+" rules replaced")
	})
}

func (in updateSecurityGroupRulesInput) request() pantech.ReplaceSecurityGroupRulesRequest {
	rules := make([]pantech.SecurityGroupRule, 0, len(in.Rules))
	for _, r := range in.Rules {
		rules = append(rules, pantech.SecurityGroupRule{Direction: r.Direction, Protocol: r.Protocol, PortRange: r.PortRange, CIDR: r.CIDR})
	}
	return pantech.ReplaceSecurityGroupRulesRequest{Rules: rules}
}

func describeRange(r securityGroupRule) string {
	if r.PortRange == "" {
		return arg(r.Protocol)
	}
	return arg(r.Protocol) + " " + arg(r.PortRange)
}

func diffSGRules(current, next []pantech.SecurityGroupRule) (added, removed []pantech.SecurityGroupRule) {
	in := func(list []pantech.SecurityGroupRule, r pantech.SecurityGroupRule) bool {
		for _, x := range list {
			if x == r {
				return true
			}
		}
		return false
	}
	for _, r := range next {
		if !in(current, r) {
			added = append(added, r)
		}
	}
	for _, r := range current {
		if !in(next, r) {
			removed = append(removed, r)
		}
	}
	return added, removed
}

type createSecurityGroupInput struct {
	Name  string              `json:"name" jsonschema:"1 to 255 characters, unique among your security groups."`
	Rules []securityGroupRule `json:"rules" jsonschema:"The group's rules; at least one. Inbound traffic no rule allows is dropped. Prefer narrow sources (e.g. the user's own IP as /32)."`
}

type deleteSecurityGroupInput struct {
	SecurityGroupID string `json:"security_group_id" jsonschema:"The security group's id. No instance or database may use it."`
	Confirmation
}

func init() {
	// Creating a group opens nothing: it applies only once attached to an
	// instance, which create_instance shows.
	add(Spec{
		Name: "create_security_group", Title: "Create a security group",
		Description: "Create a reusable instance firewall for standard instances, with at least one rule. It protects nothing until attached (create_instance's security_group_id). Free.",
		Risk:        Write,
		Routes:      []string{"POST /security-groups", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in createSecurityGroupInput) (*Result, error) {
		rules := updateSecurityGroupRulesInput{Rules: in.Rules}.request().Rules
		acc, err := c.Client.SecurityGroups.Create(ctx, pantech.CreateSecurityGroupRequest{Name: in.Name, Rules: &rules}, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "security group "+acc.ResourceID+" created")
	})

	addConfirmed(Spec{
		Name: "delete_security_group", Title: "Delete a security group",
		Description: "Delete a security group and its rules. No instance or database may use it, and the organization's default group cannot be deleted. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /security-groups/{security_group_id}", "DELETE /security-groups/{security_group_id}", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in deleteSecurityGroupInput) (*Preview, error) {
		if err := checkIDs("security_group_id", in.SecurityGroupID); err != nil {
			return nil, err
		}
		sg, err := c.Client.SecurityGroups.Get(ctx, in.SecurityGroupID)
		if err != nil {
			return nil, err
		}
		return &Preview{
			Action:   fmt.Sprintf("Permanently delete security group %s and its %s. This cannot be undone.", sg.ID, plural(len(sg.Rules), "rule", "rules")),
			Warnings: []string{"Pantech refuses while an instance or database uses it.", deleteAccessNote},
			Data:     map[string]any{"security_group": sg},
		}, nil
	}, func(ctx context.Context, c *Call, in deleteSecurityGroupInput) (*Result, error) {
		acc, err := c.Client.SecurityGroups.Delete(ctx, in.SecurityGroupID, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "security group "+in.SecurityGroupID+" deleted")
	})
}
