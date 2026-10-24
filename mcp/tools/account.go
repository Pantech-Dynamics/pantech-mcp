package tools

import (
	"context"
	"fmt"
	"strings"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

type noInput struct{}

type listPlansInput struct {
	Placement string `json:"placement,omitempty" jsonschema:"standard (default: an instance with its own public address) or vpc (an instance in a VPC subnet). Prices differ."`
}

type estimateMachine struct {
	PlanSlug     string `json:"plan_slug" jsonschema:"A plan slug from list_plans."`
	Quantity     int    `json:"quantity" jsonschema:"How many machines of this plan."`
	RunningHours int    `json:"running_hours,omitempty" jsonschema:"Hours each runs per month; default 730 (all month)."`
	Placement    string `json:"placement,omitempty" jsonschema:"standard or vpc."`
}

type estimateInput struct {
	Currency       string            `json:"currency,omitempty" jsonschema:"NGN or USD. Default: the organization's billing currency."`
	Machines       []estimateMachine `json:"machines,omitempty" jsonschema:"Instances to price, up to 50 lines."`
	ExtraStorageGB int               `json:"extra_storage_gb,omitempty" jsonschema:"Extra volume storage in GB."`
	NetworkCount   int               `json:"network_count,omitempty" jsonschema:"Number of VPC networks."`
	SnapshotGB     int               `json:"snapshot_gb,omitempty" jsonschema:"Snapshot storage in GB."`
}

func init() {
	add(Spec{
		Name: "whoami", Title: "Who am I",
		Description: "Describe the Pantech API key this connection uses: its organization, project, kind (standard or ai_app), scopes (read, write, delete) and expiry. Call it first to see what you are allowed to do.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /me"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		me, err := c.Client.Me.Get(ctx)
		if err != nil {
			return nil, err
		}
		text := fmt.Sprintf("Organization %s, project %s. Key %s (%s) with scopes: %s.",
			me.OrganizationID, me.ProjectID, me.APIKey.ID, me.APIKey.Kind, strings.Join(me.APIKey.Scopes, ", "))
		if me.APIKey.ExpiresAt != nil {
			text += " Expires " + me.APIKey.ExpiresAt.UTC().Format("2006-01-02") + "."
		}
		return ok(text, me)
	})

	add(Spec{
		Name: "list_regions", Title: "List regions",
		Description: "List the regions and zones where resources can be created, and whether each zone offers standard instances or VPC networks.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /regions"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		regions, err := c.Client.Catalog.ListRegions(ctx)
		if err != nil {
			return nil, err
		}
		return ok(plural(len(regions), "region", "regions")+".", regions)
	})

	add(Spec{
		Name: "list_plans", Title: "List plans",
		Description: "List instance sizes (vCPU, memory, disk) with their monthly price and initial payment, in the organization's currency. A plan's slug is the plan_slug for create_instance, resize_instance, create_database and Kubernetes node plans.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /plans"},
	}, func(ctx context.Context, c *Call, in listPlansInput) (*Result, error) {
		plans, err := c.Client.Catalog.ListPlans(ctx, &pantech.ListPlansParams{Placement: in.Placement})
		if err != nil {
			return nil, err
		}
		return ok(plural(len(plans), "plan", "plans")+". Prices are in minor units (kobo or cents).", plans)
	})

	add(Spec{
		Name: "list_images", Title: "List images",
		Description: "List the operating system images an instance can be created from. An image's slug is the image_slug for create_instance.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /images"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		images, err := c.Client.Catalog.ListImages(ctx)
		if err != nil {
			return nil, err
		}
		return ok(plural(len(images), "image", "images")+".", images)
	})

	add(Spec{
		Name: "estimate_price", Title: "Estimate a monthly bill",
		Description: "Price a whole configuration before building it: machines by plan and running hours, extra storage, VPC networks and snapshot storage. Nothing is created or charged.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /billing/credit", "POST /pricing/estimate"},
	}, func(ctx context.Context, c *Call, in estimateInput) (*Result, error) {
		currency := in.Currency
		if currency == "" {
			credit, err := c.Client.Billing.GetCredit(ctx)
			if err != nil {
				return nil, err
			}
			currency = credit.Currency
		}
		req := pantech.PricingEstimateRequest{Currency: currency}
		if len(in.Machines) > 0 {
			ms := make([]pantech.PricingEstimateMachineRequest, 0, len(in.Machines))
			for _, m := range in.Machines {
				hours := m.RunningHours
				if hours == 0 {
					hours = 730
				}
				mr := pantech.PricingEstimateMachineRequest{PlanSlug: m.PlanSlug, Quantity: m.Quantity, RunningHours: hours}
				if m.Placement != "" {
					mr.Placement = pantech.Ptr(m.Placement)
				}
				ms = append(ms, mr)
			}
			req.Machines = &ms
		}
		if in.ExtraStorageGB > 0 {
			req.ExtraStorageGB = pantech.Ptr(in.ExtraStorageGB)
		}
		if in.NetworkCount > 0 {
			req.NetworkCount = pantech.Ptr(in.NetworkCount)
		}
		if in.SnapshotGB > 0 {
			req.SnapshotGB = pantech.Ptr(in.SnapshotGB)
		}
		est, err := c.Client.Catalog.EstimatePrice(ctx, req)
		if err != nil {
			return nil, err
		}
		text := "Estimated monthly total: " + money(est.PricedSubtotalMinor, est.Currency) + "."
		if !est.PricingComplete {
			text += " Some lines could not be priced; see unpriced_reason in the data."
		}
		return ok(text, est)
	})

	add(Spec{
		Name: "get_usage", Title: "Get usage",
		Description: "Measured usage for the whole organization in the current billing period: running hours, storage, memory and network totals, with per-resource entries.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /usage"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		u, err := c.Client.Billing.GetUsage(ctx)
		if err != nil {
			return nil, err
		}
		const maxEntries = 200
		text := fmt.Sprintf("Usage this period: %.1f running hours, %.1f storage GB-hours, %s of usage entries.",
			u.Totals.RunningHours, u.Totals.StorageGBHours, plural(len(u.Entries), "entry", "entries"))
		if len(u.Entries) > maxEntries {
			u.Entries = u.Entries[:maxEntries]
			text += fmt.Sprintf(" Only the first %d entries are shown.", maxEntries)
		}
		return ok(text, u)
	})

	add(Spec{
		Name: "get_credit_balance", Title: "Get credit balance",
		Description: "The organization's prepaid credit: what is available, pending and prepaid, the estimated next invoice and its date, and the account's standing.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /billing/credit"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		cr, err := c.Client.Billing.GetCredit(ctx)
		if err != nil {
			return nil, err
		}
		text := fmt.Sprintf("Available credit %s; estimated next invoice %s on %s; standing: %s.",
			money(cr.AvailableMinor, cr.Currency), money(cr.EstimatedNextInvoiceMinor, cr.Currency),
			cr.NextInvoiceAt.UTC().Format("2006-01-02"), cr.Standing)
		return ok(text, cr)
	})
}
