package mcp

import (
	"context"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type prompt struct {
	name, title, description string
	args                     []*sdk.PromptArgument
	text                     func(args map[string]string) string
}

var prompts = []prompt{
	{
		name: "deploy_web_app", title: "Deploy a web app",
		description: "Plan and build a small production setup for a web app: an instance, a firewall that allows only what is needed, and optionally a database.",
		args: []*sdk.PromptArgument{
			{Name: "app", Description: "What the app is (e.g. a Node.js API, a WordPress site).", Required: false},
			{Name: "region", Description: "Preferred region code, e.g. af-abj.", Required: false},
		},
		text: func(a map[string]string) string {
			var b strings.Builder
			b.WriteString("Help me deploy a web app on Pantech Dynamics")
			if a["app"] != "" {
				b.WriteString(" (the app, as I describe it: " + quoteArg(a["app"]) + ")")
			}
			if a["region"] != "" {
				b.WriteString(" in region " + quoteArg(a["region"]))
			}
			b.WriteString(`.

1. Call whoami, list_regions, list_plans and list_images, and list_ssh_keys (ask me for a public key if I have none).
2. Propose the smallest plan that fits, with its monthly price, and an image. Use estimate_price for the whole setup.
3. Create the instance (show me the preview and wait for my yes). Then lock down access: allow SSH (22) only from my IP, and 80/443 from anywhere only if the app is public.
4. If the app needs a database, propose create_database with access only from the instance.
5. Finish with what was created, what it costs per month, and how to connect.
Never confirm a two-step tool without my explicit agreement.`)
			return b.String()
		},
	},
	{
		name: "explain_my_bill", title: "Explain my bill",
		description: "Explain the organization's current charges and suggest savings.",
		text: func(map[string]string) string {
			return `Explain my Pantech Dynamics bill.

1. Call get_credit_balance and get_usage.
2. List what is running and billed: list_instances, list_volumes, list_snapshots, list_public_ips, list_databases, list_kubernetes_clusters.
3. Summarise in plain language: available credit, the estimated next invoice and its date, and which resources cost the most.
4. Suggest savings (stopped instances still pay for storage, unattached volumes, old snapshots, unused public IPs), but change nothing: just list the steps.`
		},
	},
	{
		name: "lock_down_firewall", title: "Lock down a firewall",
		description: "Review firewall rules and security groups and propose a least-privilege set.",
		args: []*sdk.PromptArgument{
			{Name: "my_ip", Description: "Your public IPv4 address, to keep SSH open for you.", Required: false},
		},
		text: func(a map[string]string) string {
			ip := "my IP address (ask me for it)"
			if a["my_ip"] != "" {
				ip = quoteArg(a["my_ip"]) + "/32"
			}
			return `Review and tighten the network access of my Pantech Dynamics account.

1. Call list_security_groups, list_networks, list_subnets and list_firewall_rules for each subnet, and list_public_ips.
2. Flag every rule open to 0.0.0.0/0, especially SSH (22), RDP (3389) and database ports (5432, 3306).
3. Propose a least-privilege rule set: SSH only from ` + ip + `, web ports only where a public service runs.
4. Apply changes one tool call at a time, showing me each preview; never remove the rule that keeps my own SSH access.`
		},
	},
}

// quoteArg keeps a prompt argument on one line and clearly delimited.
func quoteArg(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return `"` + strings.ReplaceAll(s, `"`, `'`) + `"`
}

func addPrompts(s *sdk.Server) {
	for _, p := range prompts {
		p := p
		s.AddPrompt(&sdk.Prompt{Name: p.name, Title: p.title, Description: p.description, Arguments: p.args},
			func(_ context.Context, req *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
				args := map[string]string{}
				if req.Params != nil {
					args = req.Params.Arguments
				}
				return &sdk.GetPromptResult{
					Description: p.description,
					Messages:    []*sdk.PromptMessage{{Role: "user", Content: &sdk.TextContent{Text: p.text(args)}}},
				}, nil
			})
	}
}
