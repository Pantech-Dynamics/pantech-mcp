package tools

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

// Pantech DNS hosts the organization's zones on Pantech's own name servers:
// each zone gets its own pair of two. A zone is served only once the
// domain's registrar delegates to that pair, which proves who owns the name;
// records written before then stay pending and go live seconds after. Every
// write answers at once with the zone or record in status pending, and is
// live within about a minute; the tools wait for that within their budget.
// The first 5 live zones are free; each further zone has a monthly price,
// so creating one is confirmed with that price. Zone descriptions, record values and comments are the customer's
// own text, so they go only in the fenced data. Changing or deleting a live
// record is confirmed, and the token is bound to the record's version, so a
// record changed by someone else after the preview is not overwritten.

type dnsZone struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Status        string   `json:"status"`
	FailureCode   *string  `json:"failure_code"`
	NameServers   []string `json:"name_servers"`
	DomainID      *string  `json:"domain_id"`
	RecordCount   int      `json:"record_count"`
	LastCheckedAt *string  `json:"last_checked_at"`
	// DelegationDeadlineAt is when a zone still awaiting delegation is
	// removed (14 days after it was created).
	DelegationDeadlineAt *string `json:"delegation_deadline_at"`
	ActivatedAt          *string `json:"activated_at"`
	SuspendedReason      *string `json:"suspended_reason"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
	Version              int64   `json:"version"`
}

type dnsRecord struct {
	ID           string   `json:"id"`
	ZoneID       string   `json:"zone_id"`
	Name         string   `json:"name"`
	FQDN         string   `json:"fqdn"`
	Type         string   `json:"type"`
	TTL          int      `json:"ttl"`
	Values       []string `json:"values"`
	AllowPrivate bool     `json:"allow_private"`
	Comment      string   `json:"comment"`
	Managed      bool     `json:"managed"`
	Status       string   `json:"status"`
	FailureCode  *string  `json:"failure_code"`
	Version      int64    `json:"version"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

var (
	zoneStatuses = []string{"pending", "awaiting_delegation", "active", "suspended", "deleting", "deleted", "failed"}
	recordTypes  = []string{"A", "AAAA", "CNAME", "MX", "TXT", "SRV", "CAA", "NS", "PTR"}
)

// dnsNamePattern is a lower-case DNS name of at least two labels, without a
// trailing dot: a zone, or a domain to buy. The API checks the rest (public
// suffixes, reserved names, IDNA).
var dnsNamePattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// dnsName normalizes a model-supplied zone or domain name, or says what is
// wrong with it.
func dnsName(field, raw string) (string, error) {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if len(name) > 253 || !dnsNamePattern.MatchString(name) {
		return "", errorf("%s must be a domain name such as example.com (letters, digits and hyphens; non-Latin names in their xn-- form).", field)
	}
	return name, nil
}

type listDNSZonesInput struct {
	Q      string `json:"q,omitempty" jsonschema:"Only zones whose name contains this text."`
	Status string `json:"status,omitempty" jsonschema:"Only zones in this status: pending, awaiting_delegation, active, suspended, deleting, deleted or failed."`
	pageInput
}

type dnsZoneInput struct {
	ZoneID string `json:"zone_id" jsonschema:"The zone's id, e.g. dnsz_…, from list_dns_zones."`
}

type createDNSZoneInput struct {
	Name        string `json:"name" jsonschema:"The domain to host, e.g. example.com or a subdomain such as shop.example.com. Not a public suffix (com, co.uk)."`
	Description string `json:"description,omitempty" jsonschema:"A note for the team, e.g. 'Marketing site'."`
	Confirmation
}

type deleteDNSZoneInput struct {
	ZoneID string `json:"zone_id" jsonschema:"The zone's id, e.g. dnsz_…"`
	Confirmation
}

type listDNSRecordsInput struct {
	ZoneID string `json:"zone_id" jsonschema:"The zone's id, e.g. dnsz_…"`
	Name   string `json:"name,omitempty" jsonschema:"Only record sets with exactly this name, relative to the zone: @ for the apex, www, _dmarc."`
	Q      string `json:"q,omitempty" jsonschema:"Only record sets whose name or values contain this text."`
	Type   string `json:"type,omitempty" jsonschema:"Only this type: A, AAAA, CNAME, MX, TXT, SRV, CAA, NS or PTR."`
	pageInput
}

type createDNSRecordInput struct {
	ZoneID       string   `json:"zone_id" jsonschema:"The zone's id, e.g. dnsz_…"`
	Name         string   `json:"name" jsonschema:"The name relative to the zone: @ for the apex, www, *.dev (a wildcard only as the left-most label), _dmarc. A full name ending in the zone's name is accepted too."`
	Type         string   `json:"type" jsonschema:"A, AAAA, CNAME, MX, TXT, SRV, CAA, NS (subdomains only) or PTR. A CNAME cannot be at the apex or share its name with another record."`
	TTL          *int     `json:"ttl,omitempty" jsonschema:"Seconds resolvers may cache the answer, 60 to 86400. Default 300."`
	Values       []string `json:"values" jsonschema:"1 to 100 values in the type's format: A 203.0.113.10; AAAA 2001:db8::1; CNAME, NS and PTR a host name; MX '10 mail.example.com'; SRV '10 5 5060 sip.example.com'; CAA '0 issue \"ca.example.net\"'; TXT any text up to 4000 bytes."`
	AllowPrivate bool     `json:"allow_private,omitempty" jsonschema:"Allow private and carrier-grade NAT addresses (10.0.0.0/8, 192.168.0.0/16, 100.64.0.0/10…) in A and AAAA values, for names only reachable inside a private network. Refused otherwise."`
	Comment      string   `json:"comment,omitempty" jsonschema:"A note for the team."`
}

type updateDNSRecordInput struct {
	ZoneID       string   `json:"zone_id" jsonschema:"The zone's id, e.g. dnsz_…"`
	RecordID     string   `json:"record_id" jsonschema:"The record set's id, e.g. dnsr_…, from list_dns_records."`
	TTL          *int     `json:"ttl,omitempty" jsonschema:"New TTL in seconds, 60 to 86400."`
	Values       []string `json:"values,omitempty" jsonschema:"The new values, 1 to 100, replacing all current ones, in the type's format (see create_dns_record)."`
	AllowPrivate *bool    `json:"allow_private,omitempty" jsonschema:"Allow private and carrier-grade NAT addresses."`
	Comment      *string  `json:"comment,omitempty" jsonschema:"A new note for the team."`
	Confirmation
}

type deleteDNSRecordInput struct {
	ZoneID   string `json:"zone_id" jsonschema:"The zone's id, e.g. dnsz_…"`
	RecordID string `json:"record_id" jsonschema:"The record set's id, e.g. dnsr_…"`
	Confirmation
}

type importDNSZoneInput struct {
	ZoneID   string `json:"zone_id" jsonschema:"The zone's id, e.g. dnsz_…"`
	ZoneFile string `json:"zone_file" jsonschema:"A BIND zone file. Names are relative to the zone ($ORIGIN is honoured); the apex SOA and NS in it are ignored. At most 1000 changes."`
	Replace  bool   `json:"replace,omitempty" jsonschema:"true deletes every record set the file does not have (the apex SOA and NS stay); needs Manage and delete access and is confirmed. Default false: the file's records are added or updated and nothing is deleted."`
	Confirmation
}

func checkRecordType(t string) error {
	if !slices.Contains(recordTypes, t) {
		return errorf("type must be one of A, AAAA, CNAME, MX, TXT, SRV, CAA, NS or PTR (upper case).")
	}
	return nil
}

func checkTTL(ttl *int) error {
	if ttl != nil && (*ttl < 60 || *ttl > 86400) {
		return errorf("ttl must be from 60 to 86400 seconds.")
	}
	return nil
}

func checkValues(values []string) error {
	if len(values) < 1 || len(values) > 100 {
		return errorf("values must have 1 to 100 entries.")
	}
	for i, v := range values {
		if strings.TrimSpace(v) == "" {
			return errorf("values[%d] is empty.", i)
		}
	}
	return nil
}

// zoneText describes a zone's status and the next step. Name servers are
// the API's own host names; anything that is not a plain host name is left
// out of our text (it stays in the data).
func zoneText(z *dnsZone) string {
	switch z.Status {
	case "pending":
		return fmt.Sprintf("Zone %s is being created on Pantech DNS, usually within a minute. Call get_dns_zone shortly for its name servers.", z.ID)
	case "awaiting_delegation":
		var ns []string
		for _, n := range z.NameServers {
			if argPattern.MatchString(n) {
				ns = append(ns, n)
			}
		}
		return fmt.Sprintf("Zone %s is waiting for delegation: at the domain's registrar, set its name servers to exactly these %d: %s. "+
			"Pantech checks regularly (verify_dns_zone checks now) and the zone becomes active once they match; records can be added meanwhile and go live when it does. "+
			"A zone still waiting after 14 days is removed (failed, delegation_timeout). A domain registered through Pantech is delegated for you.",
			z.ID, len(ns), strings.Join(ns, ", "))
	case "active":
		return fmt.Sprintf("Zone %s is active: Pantech DNS answers for it. It has %s.", z.ID, plural(z.RecordCount, "record set", "record sets"))
	case "failed":
		if failureCodeOf(z.FailureCode) == "provider_limit" {
			return fmt.Sprintf("Zone %s failed (provider_limit): Pantech DNS had no free name-server pair for it just now. Try creating it again later.", z.ID)
		}
		return fmt.Sprintf("Zone %s failed (%s). A delegation_timeout zone can be created again.", z.ID, arg(failureCodeOf(z.FailureCode)))
	case "suspended":
		return fmt.Sprintf("Zone %s is suspended by Pantech and cannot be changed; the user should contact support.", z.ID)
	}
	return fmt.Sprintf("Zone %s is %s.", z.ID, arg(z.Status))
}

func failureCodeOf(p *string) string {
	if p == nil || *p == "" {
		return "unknown"
	}
	return *p
}

// recordText describes a record set's status.
func recordText(r *dnsRecord, verb string) string {
	id := "Record set " + arg(r.ID) + " (" + arg(r.Type) + ")"
	switch r.Status {
	case "pending":
		return fmt.Sprintf("%s %s: the change is accepted and goes live within about a minute, or, if the zone is still waiting for delegation, as soon as the zone is active. Call list_dns_records to check it is active.", id, verb)
	case "active":
		return fmt.Sprintf("%s %s and is live on Pantech DNS (resolvers may keep the old answer until its TTL runs out).", id, verb)
	case "failed":
		return fmt.Sprintf("%s was not applied (%s); it keeps its last good values. Fix the values and update it again.", id, arg(failureCodeOf(r.FailureCode)))
	case "deleting":
		return fmt.Sprintf("%s is being deleted; it stops answering within about a minute.", id)
	}
	return fmt.Sprintf("%s is %s.", id, arg(r.Status))
}

func zonePath(zoneID string) string { return "/dns/zones/" + zoneID }

func recordPath(zoneID, recordID string) string {
	return "/dns/zones/" + zoneID + "/records/" + recordID
}

func init() {
	add(Spec{
		Name: "list_dns_zones", Title: "List DNS zones",
		Description: "List the organization's DNS zones on Pantech DNS with their status (awaiting_delegation until the domain's registrar points at Pantech's name servers, then active), name servers and record counts. Paginated.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /dns/zones"},
	}, func(ctx context.Context, c *Call, in listDNSZonesInput) (*Result, error) {
		if in.Status != "" && !slices.Contains(zoneStatuses, in.Status) {
			return nil, errorf("status must be one of %s.", strings.Join(zoneStatuses, ", "))
		}
		q := pageQuery(in.pageInput)
		setIf(q, "q", in.Q)
		setIf(q, "status", in.Status)
		var page pantech.Page[dnsZone]
		if err := c.Client.Do(ctx, "GET", "/dns/zones", q, nil, &page); err != nil {
			return nil, err
		}
		return pageResult(&page, "zone", "zones")
	})

	add(Spec{
		Name: "get_dns_zone", Title: "Get a DNS zone",
		Description: "Get a DNS zone: its status and what to do next, the two name servers to set at the domain's registrar, its record count and when delegation was last checked.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /dns/zones/{zone_id}"},
	}, func(ctx context.Context, c *Call, in dnsZoneInput) (*Result, error) {
		if err := checkIDs("zone_id", in.ZoneID); err != nil {
			return nil, err
		}
		z, err := c.getZone(ctx, in.ZoneID)
		if err != nil {
			return nil, err
		}
		return ok(zoneText(z), z)
	})

	addConfirmedWith(Spec{
		Name: "create_dns_zone", Title: "Create a DNS zone",
		Description: "Host a domain's DNS on Pantech DNS, on Pantech's own name servers. The zone gets its own two name servers to set at the domain's registrar; it is served once the registrar delegates to exactly them (that proves the domain is yours), and is removed after 14 days without that. Records can be added meanwhile; they go live when the zone does. The first 5 live zones are free; each further zone has a monthly price, shown first. A domain registered through Pantech gets its zone automatically. Owners and admins only; at most 25 zones and 10 new zones a day.",
		Risk:        Cost,
		ConfirmWhen: "when the organization already has 5 or more live zones, so the new one is billed monthly",
		Routes:      []string{"GET /dns/zones/quote", "POST /dns/zones", "GET /dns/zones/{zone_id}"},
	}, confirmOptions[createDNSZoneInput]{
		whenLive: func(ctx context.Context, c *Call, in createDNSZoneInput) (bool, error) {
			q, err := c.zoneQuote(ctx)
			if err != nil {
				return false, err
			}
			return q.LiveZones >= q.FreeZones, nil
		},
	}, func(ctx context.Context, c *Call, in createDNSZoneInput) (*Preview, error) {
		name, err := dnsName("name", in.Name)
		if err != nil {
			return nil, err
		}
		q, err := c.zoneQuote(ctx)
		if err != nil {
			return nil, err
		}
		if !currencyCode.MatchString(q.Currency) {
			return nil, errorf("Pantech did not return the zone price; try again later.")
		}
		return &Preview{
			Action: fmt.Sprintf("Create the DNS zone %s. The organization has %d live zones and %d are free, so this one is billed.", name, q.LiveZones, q.FreeZones),
			Price: fmt.Sprintf("%s a month for this zone (billed by the hour on the monthly invoice, from when it starts waiting for delegation until it is deleted); the zones beyond the free ones then cost %s a month in all",
				money(q.MonthlyPerZoneMinor, q.Currency), money(q.MonthlyEstimateMinor+q.MonthlyPerZoneMinor, q.Currency)),
			Warnings: []string{exactAmounts},
			Data:     map[string]any{"name": name, "quote": q},
		}, nil
	}, func(ctx context.Context, c *Call, in createDNSZoneInput) (*Result, error) {
		name, err := dnsName("name", in.Name)
		if err != nil {
			return nil, err
		}
		body := map[string]any{"name": name}
		if in.Description != "" {
			body["description"] = in.Description
		}
		var z dnsZone
		if err := c.Client.Do(ctx, "POST", "/dns/zones", nil, body, &z, c.writeOpts()...); err != nil {
			return nil, err
		}
		last := c.waitZone(ctx, &z, func(z *dnsZone) bool { return z.Status != "pending" })
		return ok(zoneText(last), last)
	})

	add(Spec{
		Name: "verify_dns_zone", Title: "Check a zone's delegation now",
		Description: "Ask Pantech to check now whether the domain's registrar delegates to the zone's name servers, instead of waiting for the next regular check. The check runs within seconds; this waits for it. Name server changes at a registrar can take hours to show.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"POST /dns/zones/{zone_id}/verify", "GET /dns/zones/{zone_id}"},
	}, func(ctx context.Context, c *Call, in dnsZoneInput) (*Result, error) {
		if err := checkIDs("zone_id", in.ZoneID); err != nil {
			return nil, err
		}
		var z dnsZone
		if err := c.Client.Do(ctx, "POST", zonePath(in.ZoneID)+"/verify", nil, nil, &z, c.writeOpts()...); err != nil {
			return nil, err
		}
		checked := str(z.LastCheckedAt)
		last := c.waitZone(ctx, &z, func(n *dnsZone) bool { return n.Status != z.Status || str(n.LastCheckedAt) != checked })
		text := zoneText(last)
		if last.Status == "awaiting_delegation" && str(last.LastCheckedAt) != checked {
			text = "Checked: the registrar does not delegate to these name servers yet. " + text
		}
		return ok(text, last)
	})

	addConfirmed(Spec{
		Name: "delete_dns_zone", Title: "Delete a DNS zone",
		Description: "Delete a zone and all its records from Pantech DNS. It stops answering at once, so the domain's sites and email stop resolving as caches expire. A zone a Pantech domain points at cannot be deleted until the domain uses other name servers. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /dns/zones/{zone_id}", "DELETE /dns/zones/{zone_id}"},
	}, func(ctx context.Context, c *Call, in deleteDNSZoneInput) (*Preview, error) {
		if err := checkIDs("zone_id", in.ZoneID); err != nil {
			return nil, err
		}
		z, err := c.getZone(ctx, in.ZoneID)
		if err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Delete zone %s and its %s from Pantech DNS. It stops answering at once; this cannot be undone.", z.ID, plural(z.RecordCount, "record set", "record sets")),
			Warnings: []string{
				deleteAccessNote,
				"Every name in the zone stops resolving as resolvers' caches expire: websites, email and anything else using it.",
				"Export the records first with export_dns_zone if they may be needed again.",
			},
			Data: map[string]any{"zone": z},
		}
		if z.DomainID != nil && *z.DomainID != "" {
			p.Warnings = append(p.Warnings, fmt.Sprintf("Domain %s on Pantech points at this zone: Pantech refuses the delete until the domain uses other name servers (set_domain_name_servers).", arg(*z.DomainID)))
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in deleteDNSZoneInput) (*Result, error) {
		var z dnsZone
		if err := c.Client.Do(ctx, "DELETE", zonePath(in.ZoneID), nil, nil, &z, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Zone %s is being deleted: Pantech DNS stops answering for it within about a minute.", in.ZoneID), z)
	})

	add(Spec{
		Name: "list_dns_records", Title: "List DNS records",
		Description: "List a zone's record sets, by name then type: each one's values, TTL and status (pending until live, usually within a minute). The apex SOA and NS are shown as managed and cannot be changed. Values and comments are the account's data. Paginated.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /dns/zones/{zone_id}/records"},
	}, func(ctx context.Context, c *Call, in listDNSRecordsInput) (*Result, error) {
		if err := checkIDs("zone_id", in.ZoneID); err != nil {
			return nil, err
		}
		if in.Type != "" {
			if err := checkRecordType(in.Type); err != nil {
				return nil, err
			}
		}
		q := pageQuery(in.pageInput)
		setIf(q, "name", in.Name)
		setIf(q, "q", in.Q)
		setIf(q, "type", in.Type)
		var page pantech.Page[dnsRecord]
		if err := c.Client.Do(ctx, "GET", zonePath(in.ZoneID)+"/records", q, nil, &page); err != nil {
			return nil, err
		}
		return pageResult(&page, "record set", "record sets")
	})

	add(Spec{
		Name: "create_dns_record", Title: "Add a DNS record",
		Description: "Add a record set (one name and type, with its values) to a zone. It goes live on Pantech DNS within about a minute (this waits for it); in a zone still waiting for delegation it stays pending until the zone is active. A name and type that already exist must be changed with update_dns_record instead. Free; at most 10,000 record sets per zone.",
		Risk:        Write,
		Routes:      []string{"POST /dns/zones/{zone_id}/records", "GET /dns/zones/{zone_id}/records/{record_id}"},
	}, func(ctx context.Context, c *Call, in createDNSRecordInput) (*Result, error) {
		if err := checkIDs("zone_id", in.ZoneID); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Name) == "" {
			return nil, errorf("name is required: @ for the apex, or a name such as www.")
		}
		if err := checkRecordType(in.Type); err != nil {
			return nil, err
		}
		if err := checkTTL(in.TTL); err != nil {
			return nil, err
		}
		if err := checkValues(in.Values); err != nil {
			return nil, err
		}
		body := map[string]any{"name": in.Name, "type": in.Type, "values": in.Values}
		if in.TTL != nil {
			body["ttl"] = *in.TTL
		}
		if in.AllowPrivate {
			body["allow_private"] = true
		}
		if in.Comment != "" {
			body["comment"] = in.Comment
		}
		var r dnsRecord
		if err := c.Client.Do(ctx, "POST", zonePath(in.ZoneID)+"/records", nil, body, &r, c.writeOpts()...); err != nil {
			return nil, err
		}
		last := c.waitRecord(ctx, in.ZoneID, &r)
		return ok(recordText(last, "was added"), last)
	})

	addConfirmedWith(Spec{
		Name: "update_dns_record", Title: "Change a DNS record",
		Description: "Change a record set's TTL, values (all of them are replaced), allow_private or comment. The preview shows the current values next to the new ones; if the record changes after the preview, the confirmation is refused. Live within about a minute; this waits for it.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /dns/zones/{zone_id}/records/{record_id}", "PATCH /dns/zones/{zone_id}/records/{record_id}"},
	}, confirmOptions[updateDNSRecordInput]{
		bind: func(ctx context.Context, c *Call, in updateDNSRecordInput) (string, error) {
			r, err := c.changeableRecord(ctx, in.ZoneID, in.RecordID)
			if err != nil {
				return "", err
			}
			return recordBinding(r), nil
		},
	}, func(ctx context.Context, c *Call, in updateDNSRecordInput) (*Preview, error) {
		if err := checkIDs("zone_id", in.ZoneID, "record_id", in.RecordID); err != nil {
			return nil, err
		}
		if err := checkTTL(in.TTL); err != nil {
			return nil, err
		}
		if in.Values != nil {
			if err := checkValues(in.Values); err != nil {
				return nil, err
			}
		}
		if in.TTL == nil && in.Values == nil && in.AllowPrivate == nil && in.Comment == nil {
			return nil, errorf("Nothing to change: give ttl, values, allow_private or comment.")
		}
		r, err := c.changeableRecord(ctx, in.ZoneID, in.RecordID)
		if err != nil {
			return nil, err
		}
		change := recordChange(in)
		return &Preview{
			Action: fmt.Sprintf("Change record set %s (%s, version %d) in zone %s; the new values replace the current ones (both in the data).", r.ID, arg(r.Type), r.Version, in.ZoneID),
			Warnings: []string{
				"Resolvers may keep giving the old answer until its current TTL runs out.",
			},
			Data:  map[string]any{"current": r, "change": change},
			bound: recordBinding(r),
		}, nil
	}, func(ctx context.Context, c *Call, in updateDNSRecordInput) (*Result, error) {
		var r dnsRecord
		opts := append(c.writeOpts(), ifMatch(c.bound))
		if err := c.Client.Do(ctx, "PATCH", recordPath(in.ZoneID, in.RecordID), nil, recordChange(in), &r, opts...); err != nil {
			return nil, err
		}
		last := c.waitRecord(ctx, in.ZoneID, &r)
		return ok(recordText(last, "was changed"), last)
	})

	addConfirmedWith(Spec{
		Name: "delete_dns_record", Title: "Delete a DNS record",
		Description: "Delete a record set from a zone. It stops answering within about a minute. The apex SOA and NS cannot be deleted. If the record changes after the preview, the confirmation is refused. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /dns/zones/{zone_id}/records/{record_id}", "DELETE /dns/zones/{zone_id}/records/{record_id}"},
	}, confirmOptions[deleteDNSRecordInput]{
		bind: func(ctx context.Context, c *Call, in deleteDNSRecordInput) (string, error) {
			r, err := c.changeableRecord(ctx, in.ZoneID, in.RecordID)
			if err != nil {
				return "", err
			}
			return recordBinding(r), nil
		},
	}, func(ctx context.Context, c *Call, in deleteDNSRecordInput) (*Preview, error) {
		if err := checkIDs("zone_id", in.ZoneID, "record_id", in.RecordID); err != nil {
			return nil, err
		}
		r, err := c.changeableRecord(ctx, in.ZoneID, in.RecordID)
		if err != nil {
			return nil, err
		}
		return &Preview{
			Action: fmt.Sprintf("Delete record set %s (%s, %s) from zone %s. This cannot be undone.", r.ID, arg(r.Type), plural(len(r.Values), "value", "values"), in.ZoneID),
			Warnings: []string{
				deleteAccessNote,
				"Whatever uses this name (a website, email, a verification) stops resolving as resolvers' caches expire.",
			},
			Data:  map[string]any{"record": r},
			bound: recordBinding(r),
		}, nil
	}, func(ctx context.Context, c *Call, in deleteDNSRecordInput) (*Result, error) {
		var r dnsRecord
		opts := append(c.writeOpts(), ifMatch(c.bound))
		if err := c.Client.Do(ctx, "DELETE", recordPath(in.ZoneID, in.RecordID), nil, nil, &r, opts...); err != nil {
			return nil, err
		}
		if r.ID == "" {
			r = dnsRecord{ID: in.RecordID, ZoneID: in.ZoneID, Status: "deleting"}
		}
		return ok(recordText(&r, "is being deleted"), r)
	})

	addConfirmedWith(Spec{
		Name: "import_dns_zone", Title: "Import a BIND zone file",
		Description: "Import a BIND zone file into a zone: every record set in it is added, or updated when its name and type exist, all or nothing, up to 1000 changes. With replace true, record sets the file does not have are deleted too (the apex SOA and NS stay); that needs an AI app connected with Manage and delete access. Live within about a minute.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		ConfirmWhen: "when replace is true",
		Routes:      []string{"GET /dns/zones/{zone_id}", "POST /dns/zones/{zone_id}/import"},
	}, confirmOptions[importDNSZoneInput]{
		when: func(in importDNSZoneInput) bool { return in.Replace },
	}, func(ctx context.Context, c *Call, in importDNSZoneInput) (*Preview, error) {
		if err := checkImport(in); err != nil {
			return nil, err
		}
		z, err := c.getZone(ctx, in.ZoneID)
		if err != nil {
			return nil, err
		}
		return &Preview{
			Action: fmt.Sprintf("Replace the records of zone %s (now %s) with the zone file's: every record set the file does not have is deleted, the rest are added or updated, all or nothing.", z.ID, plural(z.RecordCount, "record set", "record sets")),
			Warnings: []string{
				deleteAccessNote,
				"Names that are only in the zone today stop resolving within about a minute. Export the zone first with export_dns_zone to keep a copy.",
			},
			Data: map[string]any{"zone": z},
		}, nil
	}, func(ctx context.Context, c *Call, in importDNSZoneInput) (*Result, error) {
		if err := checkImport(in); err != nil {
			return nil, err
		}
		var out struct {
			Data []dnsRecord `json:"data"`
		}
		body := map[string]any{"zone_file": in.ZoneFile, "replace": in.Replace}
		if err := c.Client.Do(ctx, "POST", zonePath(in.ZoneID)+"/import", nil, body, &out, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Imported into zone %s: %s changed, live within about a minute.", in.ZoneID, plural(len(out.Data), "record set", "record sets")), out)
	})

	add(Spec{
		Name: "export_dns_zone", Title: "Export a zone as a BIND file",
		Description: "Export a zone's records as a BIND zone file, to keep a copy or move them. The file is the account's data.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /dns/zones/{zone_id}/export"},
	}, func(ctx context.Context, c *Call, in dnsZoneInput) (*Result, error) {
		if err := checkIDs("zone_id", in.ZoneID); err != nil {
			return nil, err
		}
		var raw []byte
		if err := c.Client.Do(ctx, "GET", zonePath(in.ZoneID)+"/export", nil, nil, &raw, pantech.WithRequestHeader("Accept", "text/dns")); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Zone file for zone %s (%d bytes), in the data.", in.ZoneID, len(raw)),
			map[string]any{"zone_id": in.ZoneID, "zone_file": string(raw)})
	})
}

// dnsZoneQuote is GET /dns/zones/quote: the free allowance and the price of
// each zone beyond it.
type dnsZoneQuote struct {
	FreeZones            int    `json:"free_zones"`
	LiveZones            int    `json:"live_zones"`
	BillableZones        int    `json:"billable_zones"`
	Currency             string `json:"currency"`
	MonthlyPerZoneMinor  int64  `json:"monthly_per_zone_minor"`
	MonthlyEstimateMinor int64  `json:"monthly_estimate_minor"`
	NominalMonthHours    int    `json:"nominal_month_hours"`
}

func (c *Call) zoneQuote(ctx context.Context) (*dnsZoneQuote, error) {
	var q dnsZoneQuote
	if err := c.Client.Do(ctx, "GET", "/dns/zones/quote", nil, nil, &q); err != nil {
		return nil, err
	}
	return &q, nil
}

func checkImport(in importDNSZoneInput) error {
	if err := checkIDs("zone_id", in.ZoneID); err != nil {
		return err
	}
	if strings.TrimSpace(in.ZoneFile) == "" {
		return errorf("zone_file is required: the text of a BIND zone file.")
	}
	return nil
}

// recordChange is the PATCH body: only the fields given.
func recordChange(in updateDNSRecordInput) map[string]any {
	body := map[string]any{}
	if in.TTL != nil {
		body["ttl"] = *in.TTL
	}
	if in.Values != nil {
		body["values"] = in.Values
	}
	if in.AllowPrivate != nil {
		body["allow_private"] = *in.AllowPrivate
	}
	if in.Comment != nil {
		body["comment"] = *in.Comment
	}
	return body
}

// recordBinding is what a record change's token is bound to: the record's
// version, sent back as If-Match.
func recordBinding(r *dnsRecord) string { return strconv.FormatInt(r.Version, 10) }

func ifMatch(version string) pantech.RequestOption {
	return pantech.WithRequestHeader("If-Match", version)
}

func pageQuery(in pageInput) url.Values {
	q := url.Values{"limit": {strconv.Itoa(listLimit(in.Limit))}}
	setIf(q, "cursor", in.Cursor)
	return q
}

func setIf(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}

func (c *Call) getZone(ctx context.Context, id string) (*dnsZone, error) {
	var z dnsZone
	if err := c.Client.Do(ctx, "GET", zonePath(id), nil, nil, &z); err != nil {
		return nil, err
	}
	return &z, nil
}

func (c *Call) getRecord(ctx context.Context, zoneID, recordID string) (*dnsRecord, error) {
	var r dnsRecord
	if err := c.Client.Do(ctx, "GET", recordPath(zoneID, recordID), nil, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// changeableRecord reads a record set and refuses the managed apex SOA and
// NS before anything is previewed.
func (c *Call) changeableRecord(ctx context.Context, zoneID, recordID string) (*dnsRecord, error) {
	if err := checkIDs("zone_id", zoneID, "record_id", recordID); err != nil {
		return nil, err
	}
	r, err := c.getRecord(ctx, zoneID, recordID)
	if err != nil {
		return nil, err
	}
	if r.Managed {
		return nil, errorf("Record set %s is the zone's apex %s, which Pantech manages: it cannot be changed or deleted.", arg(r.ID), arg(r.Type))
	}
	return r, nil
}

// waitZone polls a zone within the wait budget until done reports true.
func (c *Call) waitZone(ctx context.Context, z *dnsZone, done func(*dnsZone) bool) *dnsZone {
	last := z
	if checkIDs("zone_id", z.ID) != nil {
		return last
	}
	c.poll(ctx, func() (string, bool) {
		next, err := c.getZone(ctx, z.ID)
		if err != nil {
			return "", true
		}
		last = next
		return "zone " + z.ID + " is " + next.Status, done(next)
	})
	return last
}

// waitRecord polls a record set within the wait budget until it is no
// longer pending.
func (c *Call) waitRecord(ctx context.Context, zoneID string, r *dnsRecord) *dnsRecord {
	last := r
	if r.Status != "pending" || checkIDs("record_id", r.ID) != nil {
		return last
	}
	c.poll(ctx, func() (string, bool) {
		next, err := c.getRecord(ctx, zoneID, r.ID)
		if err != nil {
			return "", true
		}
		last = next
		return "record set " + r.ID + " is " + next.Status, next.Status != "pending"
	})
	return last
}
