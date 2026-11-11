package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
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
	lbTLSInput
	Protocol string `json:"protocol,omitempty" jsonschema:"tcp (default: traffic passes through unchanged) or https (appliance only: TLS ends on the load balancer with certificate_id, and instances get plain HTTP on private_port)."`
	Confirmation
}

// lbTLSInput are the https listener settings of an appliance load balancer.
type lbTLSInput struct {
	CertificateID string `json:"certificate_id,omitempty" jsonschema:"https only, and required there: an issued certificate from list_certificates (cert_…). While in use it cannot be deleted or revoked; its renewals reach the load balancer by themselves."`
	TLSPolicy     string `json:"tls_policy,omitempty" jsonschema:"https only: modern (default: TLS 1.2 and 1.3, forward-secret ciphers) or tls13 (TLS 1.3 only)."`
	RedirectHTTP  *bool  `json:"redirect_http,omitempty" jsonschema:"https only: also answer port 80, redirecting every request to HTTPS."`
}

// lbTLS is what a load balancer reports about its listener's TLS. The
// typed SDK this server is built on predates https listeners, so these are
// read from the raw answer.
type lbTLS struct {
	Protocol          string  `json:"protocol"`
	CertificateID     *string `json:"certificate_id"`
	TLSPolicy         *string `json:"tls_policy"`
	RedirectHTTP      *bool   `json:"redirect_http"`
	CertificateSHA256 *string `json:"certificate_fingerprint_sha256"`
}

func (in lbTLSInput) given() bool {
	return in.CertificateID != "" || in.TLSPolicy != "" || in.RedirectHTTP != nil
}

func (in lbTLSInput) check() error {
	if in.CertificateID != "" {
		if err := checkIDs("certificate_id", in.CertificateID); err != nil {
			return err
		}
	}
	if in.TLSPolicy != "" && in.TLSPolicy != "modern" && in.TLSPolicy != "tls13" {
		return errorf("tls_policy must be modern or tls13.")
	}
	return nil
}

func (in lbTLSInput) fields(body map[string]any) {
	if in.CertificateID != "" {
		body["certificate_id"] = in.CertificateID
	}
	if in.TLSPolicy != "" {
		body["tls_policy"] = in.TLSPolicy
	}
	if in.RedirectHTTP != nil {
		body["redirect_http"] = *in.RedirectHTTP
	}
}

func (in createLoadBalancerInput) check() error {
	switch in.Protocol {
	case "", "tcp":
		if in.lbTLSInput.given() {
			return errorf("certificate_id, tls_policy and redirect_http are for protocol https only.")
		}
	case "https":
		if in.Kind != "appliance" {
			return errorf("protocol https needs kind appliance: a vpc_native load balancer passes TCP through and cannot end TLS.")
		}
		if in.CertificateID == "" {
			return errorf("protocol https needs certificate_id: an issued certificate from list_certificates.")
		}
	default:
		return errorf("protocol must be tcp or https.")
	}
	return in.lbTLSInput.check()
}

// body is the create request: the typed SDK request plus the https fields.
func (in createLoadBalancerInput) body() (map[string]any, error) {
	body, err := asMap(in.request())
	if err != nil {
		return nil, err
	}
	if in.Protocol != "" {
		body["protocol"] = in.Protocol
	}
	in.lbTLSInput.fields(body)
	return body, nil
}

// asMap turns a typed request into a JSON object to add fields to.
func asMap(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	return m, json.Unmarshal(raw, &m)
}

// tlsNotes describes an https listener's certificate for a preview, and
// warns when the certificate cannot be served.
func (c *Call) tlsNotes(ctx context.Context, in lbTLSInput) []string {
	var notes []string
	if in.CertificateID != "" {
		ct, err := c.getCertificate(ctx, in.CertificateID)
		switch {
		case err != nil:
			notes = append(notes, "Certificate "+in.CertificateID+" could not be read now; Pantech refuses it unless it is issued and in this organization.")
		case ct.Status != "issued" && ct.Status != "renewing":
			notes = append(notes, fmt.Sprintf("Certificate %s is %s, not issued, so Pantech refuses it.", ct.ID, arg(ct.Status)))
		default:
			notes = append(notes, fmt.Sprintf("HTTPS with certificate %s (%s). While the load balancer uses it, it cannot be deleted or revoked; its renewals reach the load balancer by themselves.", ct.ID, plural(len(ct.Names), "name", "names")))
		}
	}
	if in.RedirectHTTP != nil && *in.RedirectHTTP {
		notes = append(notes, "redirect_http also opens port 80 to the same clients, answering every request with a redirect to HTTPS.")
	}
	return notes
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
		q := url.Values{}
		setIf(q, "network_id", in.NetworkID)
		setIf(q, "public_ip_id", in.PublicIPID)
		var lbs []json.RawMessage
		cursor := ""
		for range 20 {
			setIf(q, "cursor", cursor)
			var page pantech.Page[json.RawMessage]
			if err := c.Client.Do(ctx, "GET", "/load-balancers", q, nil, &page); err != nil {
				return nil, err
			}
			lbs = append(lbs, page.Data...)
			if !page.HasMore() || *page.NextCursor == cursor {
				break
			}
			cursor = *page.NextCursor
		}
		return ok(plural(len(lbs), "load balancer", "load balancers")+".", lbs)
	})

	addConfirmed(Spec{
		Name: "create_load_balancer", Title: "Create a load balancer",
		Description: "Spread one port of a public address across instances. The port becomes reachable from the internet (limit it with cidr_list). With protocol https (appliance only), TLS ends on the load balancer with a Certificate Manager certificate and instances get plain HTTP. An appliance load balancer is a VM billed as such.",
		Risk:        Cost,
		Routes:      []string{"POST /load-balancers", "GET /operations/{operation_id}", "GET /billing/credit", "GET /plans", "POST /pricing/estimate", "GET /certificates/{certificate_id}"},
	}, func(ctx context.Context, c *Call, in createLoadBalancerInput) (*Preview, error) {
		if err := in.check(); err != nil {
			return nil, err
		}
		body, err := in.body()
		if err != nil {
			return nil, err
		}
		price := "no extra charge: a vpc_native load balancer uses the public IP you already pay for"
		if in.Kind == "appliance" {
			price = c.appliancePrice(ctx)
		}
		p := &Preview{
			Action: fmt.Sprintf("Create a %s %s load balancer on public port %d spreading traffic across %s.",
				arg(valueOr(in.Kind, "vpc_native")), arg(valueOr(in.Protocol, "tcp")), in.PublicPort, plural(len(in.InstanceIDs), "instance", "instances")),
			Price:    price,
			Warnings: c.tlsNotes(ctx, in.lbTLSInput),
			Data:     map[string]any{"request": body},
		}
		if len(in.CIDRList) == 0 {
			p.Warnings = append(p.Warnings, "No cidr_list: anyone on the internet can connect to the port.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in createLoadBalancerInput) (*Result, error) {
		body, err := in.body()
		if err != nil {
			return nil, err
		}
		var acc pantech.Accepted
		if err := c.Client.Do(ctx, "POST", "/load-balancers", nil, body, &acc, c.writeOpts()...); err != nil {
			return nil, err
		}
		return c.finish(ctx, &acc, "load balancer "+acc.ResourceID+" created")
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
	lbTLSInput
	Confirmation
}

func init() {
	addConfirmed(Spec{
		Name: "update_load_balancer", Title: "Change a load balancer",
		Description: "Rename a load balancer, change its algorithm, replace its targets (instance_ids, when given, even empty, is the whole new set), or, on an https load balancer, change its certificate, tls_policy or redirect_http. Ports, protocol, public IP, subnet and cidr_list cannot change. Get the current targets with list_load_balancers first.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /load-balancers/{load_balancer_id}", "PATCH /load-balancers/{load_balancer_id}", "GET /operations/{operation_id}", "GET /certificates/{certificate_id}"},
	}, func(ctx context.Context, c *Call, in updateLoadBalancerInput) (*Preview, error) {
		if err := checkIDs("load_balancer_id", in.LoadBalancerID); err != nil {
			return nil, err
		}
		if in.Name == "" && in.Algorithm == "" && in.InstanceIDs == nil && !in.lbTLSInput.given() {
			return nil, errorf("Nothing to change: give name, algorithm, instance_ids, certificate_id, tls_policy or redirect_http.")
		}
		if err := in.lbTLSInput.check(); err != nil {
			return nil, err
		}
		lb, tls, raw, err := c.getLoadBalancer(ctx, in.LoadBalancerID)
		if err != nil {
			return nil, err
		}
		if in.lbTLSInput.given() && tls.Protocol != "https" {
			return nil, errorf("Load balancer %s is not an https load balancer: certificate_id, tls_policy and redirect_http apply to https only, and the protocol cannot change.", lb.ID)
		}
		body, err := in.body()
		if err != nil {
			return nil, err
		}
		p := &Preview{Data: map[string]any{"load_balancer": raw, "request": body}}
		var changes []string
		if in.CertificateID != "" {
			changes = append(changes, "serve certificate "+in.CertificateID+" instead of "+arg(str(tls.CertificateID))+" (the old one is released)")
		}
		if in.TLSPolicy != "" {
			changes = append(changes, "use TLS policy "+in.TLSPolicy)
		}
		if in.RedirectHTTP != nil {
			changes = append(changes, "turn the port 80 redirect "+onOff(*in.RedirectHTTP))
		}
		p.Warnings = append(p.Warnings, c.tlsNotes(ctx, in.lbTLSInput)...)
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
			p.Data = map[string]any{"load_balancer": raw, "request": body, "added": added, "removed": removed}
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
		body, err := in.body()
		if err != nil {
			return nil, err
		}
		var acc pantech.Accepted
		if err := c.Client.Do(ctx, "PATCH", "/load-balancers/"+in.LoadBalancerID, nil, body, &acc, c.writeOpts()...); err != nil {
			return nil, err
		}
		return c.finish(ctx, &acc, "load balancer "+in.LoadBalancerID+" changed")
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

// body is the update request: the typed SDK request plus the https fields.
func (in updateLoadBalancerInput) body() (map[string]any, error) {
	body, err := asMap(in.request())
	if err != nil {
		return nil, err
	}
	in.lbTLSInput.fields(body)
	return body, nil
}

// getLoadBalancer reads a load balancer once, as the typed SDK value, its
// TLS settings and the whole answer for the data.
func (c *Call) getLoadBalancer(ctx context.Context, id string) (*pantech.LoadBalancer, *lbTLS, json.RawMessage, error) {
	var raw []byte
	if err := c.Client.Do(ctx, "GET", "/load-balancers/"+id, nil, nil, &raw); err != nil {
		return nil, nil, nil, err
	}
	var lb pantech.LoadBalancer
	var tls lbTLS
	if err := json.Unmarshal(raw, &lb); err != nil {
		return nil, nil, nil, err
	}
	if err := json.Unmarshal(raw, &tls); err != nil {
		return nil, nil, nil, err
	}
	return &lb, &tls, json.RawMessage(raw), nil
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
