package tools

import (
	"context"
	"fmt"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

// findPlan looks a plan up in the price list for placement ("standard" or
// "vpc"). A plan that is not there is an error the model can fix.
func (c *Call) findPlan(ctx context.Context, slug, placement string) (*pantech.Plan, error) {
	plans, err := c.Client.Catalog.ListPlans(ctx, &pantech.ListPlansParams{Placement: placement})
	if err != nil {
		return nil, err
	}
	for i := range plans {
		if plans[i].Slug == slug {
			return &plans[i], nil
		}
	}
	return nil, errorf("There is no plan %s. Call list_plans for the plan slugs.", arg(slug))
}

// planPrice describes a plan's price, or says why it has none.
func planPrice(p *pantech.Plan) string {
	if p.Price == nil {
		if p.UnpricedReason != nil {
			return "not priced (" + *p.UnpricedReason + ")"
		}
		return "not priced"
	}
	s := fmt.Sprintf("about %s a month", money(p.Price.MonthlyEstimateMinor, p.Price.Currency))
	if p.Price.InitialPaymentMinor > 0 {
		s += fmt.Sprintf(", with an initial payment of %s taken from credit (or the saved card) now", money(p.Price.InitialPaymentMinor, p.Price.Currency))
	}
	return s
}

func placementOf(subnetID, networkID string) string {
	if subnetID != "" || networkID != "" {
		return "vpc"
	}
	return "standard"
}

// creditNote says how much credit is available, best effort.
func (c *Call) creditNote(ctx context.Context) string {
	cr, err := c.Client.Billing.GetCredit(ctx)
	if err != nil {
		return ""
	}
	return "Available credit now: " + money(cr.AvailableMinor, cr.Currency) + "."
}
