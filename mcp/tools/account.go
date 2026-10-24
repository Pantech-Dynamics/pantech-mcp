package tools

import (
	"context"
	"fmt"
	"slices"
	"strconv"
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
		Description: "Price a whole configuration before building it: machines by plan and running hours, extra storage, VPC networks and snapshot storage. Nothing is created or charged. The result is an ESTIMATE of a month's bill, not a charge or a quote: say so when reporting it. Its amounts are exact minor-unit values converted to the currency's major unit; report them exactly as given (as an estimate), never rounded or approximated.",
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
		text := "Estimated monthly total: " + money(est.PricedSubtotalMinor, est.Currency) + " (an estimate of a month's bill, not a charge; the figure itself is exact, report it as given)."
		if !est.PricingComplete {
			text += " Some lines could not be priced; see unpriced_reason in the data."
		}
		return ok(text, est)
	})

	add(Spec{
		Name: "get_usage", Title: "Get usage",
		Description: "Measured usage for the whole organization in the current billing period: running hours, storage, memory and network totals, with per-resource entries. These are measured quantities, not money: report them exactly as given, never rounded or approximated. For what each resource cost, use get_usage_by_resource.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /usage"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		u, err := c.Client.Billing.GetUsage(ctx)
		if err != nil {
			return nil, err
		}
		const maxEntries = 200
		text := fmt.Sprintf("Measured usage this period (exact): %s running hours, %s storage GB-hours, %s of usage entries.",
			exactNumber(u.Totals.RunningHours), exactNumber(u.Totals.StorageGBHours), plural(len(u.Entries), "entry", "entries"))
		if len(u.Entries) > maxEntries {
			u.Entries = u.Entries[:maxEntries]
			text += fmt.Sprintf(" Only the first %d entries are shown.", maxEntries)
		}
		return ok(text, u)
	})

	add(Spec{
		Name: "get_credit_balance", Title: "Get credit balance",
		Description: "The organization's prepaid credit: what is available now (exact), pending and prepaid, the next invoice so far and its date (a forecast that grows with usage until the bill date), and the account's standing. " + exactAmounts,
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /billing/credit"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		cr, err := c.Client.Billing.GetCredit(ctx)
		if err != nil {
			return nil, err
		}
		text := fmt.Sprintf("Available credit: %s (exact, as of now). Next invoice so far: %s on %s — a forecast that grows with usage until the bill date. Standing: %s.",
			money(cr.AvailableMinor, cr.Currency), money(cr.EstimatedNextInvoiceMinor, cr.Currency),
			cr.NextInvoiceAt.UTC().Format("2006-01-02"), arg(cr.Standing))
		if cr.PendingMinor != 0 || cr.PrepaidMinor != 0 {
			text += fmt.Sprintf(" Pending purchases: %s; prepaid instance payments: %s (both exact).", money(cr.PendingMinor, cr.Currency), money(cr.PrepaidMinor, cr.Currency))
		}
		text += " Report these amounts exactly as given."
		return ok(text, cr)
	})

	add(Spec{
		Name: "get_usage_by_resource", Title: "Get cost by resource",
		Description: "What each resource cost over a period: the last 30d (default) or 7d, this month, or last_month, with its rate and days active. " + exactAmounts,
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /usage/by-resource"},
	}, func(ctx context.Context, c *Call, in usageByResourceInput) (*Result, error) {
		if in.Period != "" && !slices.Contains([]string{"30d", "7d", "month", "last_month"}, in.Period) {
			return nil, errorf("period must be 30d, 7d, month or last_month.")
		}
		u, err := c.Client.Billing.GetUsageByResource(ctx, &pantech.UsageByResourceParams{Period: in.Period})
		if err != nil {
			return nil, err
		}
		var total int64
		for _, r := range u.Data {
			total += r.CostMinor
		}
		return ok(fmt.Sprintf("%s; together %s (exact charges for measured usage in the period). Report these amounts exactly as given.",
			plural(len(u.Data), "resource", "resources"), money(total, u.Currency)), u)
	})

	add(Spec{
		Name: "list_receipts", Title: "List receipts",
		Description: "Receipts for money that moved on the account (payments, top-ups, invoices paid, credit used for new resources, refunds), newest first. Paginated. " + exactAmounts,
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /billing/receipts"},
	}, func(ctx context.Context, c *Call, in pageInput) (*Result, error) {
		page, err := c.Client.Billing.ListReceipts(ctx, in.params())
		if err != nil {
			return nil, err
		}
		for i := range page.Data {
			// The code that proves a printed copy genuine stays on the receipt.
			page.Data[i].Verification = ""
		}
		res, err := pageResult(page, "receipt", "receipts")
		if res != nil {
			res.Text += " amount_minor is exact, in minor units (kobo or cents); report amounts exactly."
		}
		return res, err
	})
}

type usageByResourceInput struct {
	Period string `json:"period,omitempty" jsonschema:"30d (default), 7d, month (this month so far) or last_month."`
}

// pageInput is a page of a list without a name filter.
type pageInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"Items per page, 1 to 100 (default 50)."`
	Cursor string `json:"cursor,omitempty" jsonschema:"The next_cursor of the previous page, to get the next one."`
}

func (in pageInput) params() *pantech.PageParams {
	return &pantech.PageParams{Limit: listLimit(in.Limit), Cursor: in.Cursor}
}

// exactNumber prints a measured quantity without rounding it.
func exactNumber(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
