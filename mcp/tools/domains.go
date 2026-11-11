package tools

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

// Domains are bought, transferred in and renewed with the organization's
// Pantech credit: the order's full amount is taken from credit before the
// registrar is asked, and returns to credit if the order fails. A registered
// domain cannot be returned, so every paid tool is confirmed, its preview
// names the exact total, and its token is bound to that total: if the price
// changes after the preview, the confirmation is refused. Confirming also
// accepts the registration agreement, which the tool sends as accept_terms.
//
// The registrant contact is personal data. It is sent to the API, shown back
// only in the fenced data, and never put in our own text. An auth code (to
// transfer a domain in or out) is never put in our text or in a preview.

type tldOffer struct {
	TLD           string `json:"tld"`
	Currency      string `json:"currency"`
	RegisterMinor int64  `json:"register_minor"`
	RenewMinor    int64  `json:"renew_minor"`
	TransferMinor int64  `json:"transfer_minor"`
	Featured      bool   `json:"featured"`
	// PrivacyAlwaysOn: the TLD's registry hides every registrant's contact
	// details itself (.ng and its second levels), so privacy cannot be off.
	PrivacyAlwaysOn bool `json:"privacy_always_on"`
}

type domainSearchResult struct {
	Name   string    `json:"name"`
	TLD    string    `json:"tld"`
	Status string    `json:"status"`
	Price  *tldOffer `json:"price"`
}

type domainTransferCheck struct {
	Name         string    `json:"name"`
	Transferable bool      `json:"transferable"`
	Reason       *string   `json:"reason"`
	Price        *tldOffer `json:"price"`
}

type registeredDomain struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	TLD             string   `json:"tld"`
	Status          string   `json:"status"`
	FailureCode     *string  `json:"failure_code"`
	AutoRenew       bool     `json:"auto_renew"`
	TransferLock    bool     `json:"transfer_lock"`
	Privacy         bool     `json:"privacy"`
	NameServerMode  string   `json:"name_server_mode"`
	NameServers     []string `json:"name_servers"`
	ZoneID          *string  `json:"zone_id"`
	RegisteredAt    *string  `json:"registered_at"`
	ExpiresAt       *string  `json:"expires_at"`
	ContactVerified bool     `json:"contact_verified"`
	SettingsPending bool     `json:"settings_pending"`
	AuthCodePending bool     `json:"auth_code_pending"`
	SuspendedReason *string  `json:"suspended_reason"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
	Version         int64    `json:"version"`
}

type domainOrder struct {
	ID          string  `json:"id"`
	DomainID    string  `json:"domain_id"`
	DomainName  string  `json:"domain_name"`
	Kind        string  `json:"kind"`
	Years       int     `json:"years"`
	AmountMinor int64   `json:"amount_minor"`
	Currency    string  `json:"currency"`
	Status      string  `json:"status"`
	FailureCode *string `json:"failure_code"`
	Refunded    bool    `json:"refunded"`
	// While the order is awaiting_payment (the credit falls short), it waits
	// until PaymentExpiresAt: a US-dollar order charges the saved card the
	// difference, and PaymentURL is set only when the bank asks the customer
	// to confirm that payment; a naira order waits for credit to be added.
	PaymentURL       *string `json:"payment_url"`
	PaymentExpiresAt *string `json:"payment_expires_at"`
	CreatedAt        string  `json:"created_at"`
	CompletedAt      *string `json:"completed_at"`
}

type domainOrderResult struct {
	Domain registeredDomain `json:"domain"`
	Order  domainOrder      `json:"order"`
}

// domainContact is the registrant: personal data.
type domainContact struct {
	Type             string `json:"type" jsonschema:"person or company."`
	FirstName        string `json:"first_name" jsonschema:"Given name."`
	LastName         string `json:"last_name" jsonschema:"Family name."`
	OrganizationName string `json:"organization_name,omitempty" jsonschema:"The company's name; required when type is company."`
	Email            string `json:"email" jsonschema:"The registrant's email. The registry sends a verification email here that must be clicked within 15 days."`
	Phone            string `json:"phone" jsonschema:"Phone as +CC.NUMBER, e.g. +234.8012345678."`
	AddressLine1     string `json:"address_line1" jsonschema:"Street address."`
	AddressLine2     string `json:"address_line2,omitempty" jsonschema:"More of the address."`
	City             string `json:"city" jsonschema:"City."`
	State            string `json:"state,omitempty" jsonschema:"State or region."`
	CountryCode      string `json:"country_code" jsonschema:"Two-letter country code, e.g. NG."`
	PostalCode       string `json:"postal_code,omitempty" jsonschema:"Postal code."`
}

var (
	domainStatuses = []string{"pending", "registering", "transferring", "active", "expired", "failed", "transferred_out", "suspended"}
	phonePattern   = regexp.MustCompile(`^\+[0-9]{1,3}\.[0-9]{4,14}$`)
	countryPattern = regexp.MustCompile(`^[A-Z]{2}$`)
	emailPattern   = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

func (ct domainContact) check() error {
	switch ct.Type {
	case "person":
	case "company":
		if strings.TrimSpace(ct.OrganizationName) == "" {
			return errorf("contact.organization_name is required for a company.")
		}
	default:
		return errorf("contact.type must be person or company.")
	}
	for field, v := range map[string]string{"first_name": ct.FirstName, "last_name": ct.LastName, "address_line1": ct.AddressLine1, "city": ct.City} {
		if strings.TrimSpace(v) == "" {
			return errorf("contact.%s is required.", field)
		}
	}
	if !emailPattern.MatchString(ct.Email) {
		return errorf("contact.email must be an email address.")
	}
	if !phonePattern.MatchString(ct.Phone) {
		return errorf("contact.phone must be +CC.NUMBER, e.g. +234.8012345678.")
	}
	if !countryPattern.MatchString(ct.CountryCode) {
		return errorf("contact.country_code must be a two-letter code in capitals, e.g. NG.")
	}
	return nil
}

func checkYears(years *int) (int, error) {
	if years == nil {
		return 1, nil
	}
	if *years < 1 || *years > 10 {
		return 0, errorf("years must be from 1 to 10.")
	}
	return *years, nil
}

type searchDomainsInput struct {
	Query string `json:"query" jsonschema:"A name to look for, e.g. example.com or just example: the exact name across the TLDs Pantech sells, plus suggestions."`
}

type checkDomainTransferInput struct {
	Name string `json:"name" jsonschema:"The domain to move to Pantech, e.g. example.com."`
}

type listDomainsInput struct {
	Q      string `json:"q,omitempty" jsonschema:"Only domains whose name contains this text."`
	Status string `json:"status,omitempty" jsonschema:"Only domains in this status: pending, registering, transferring, active, expired, failed, transferred_out or suspended."`
	pageInput
}

type domainInput struct {
	DomainID string `json:"domain_id" jsonschema:"The domain's id, e.g. dom_…, from list_domains."`
}

type registerDomainInput struct {
	Name      string        `json:"name" jsonschema:"The domain to register, e.g. example.com, as search_domains found it available."`
	Years     *int          `json:"years,omitempty" jsonschema:"1 to 10 years. Default 1."`
	Contact   domainContact `json:"contact" jsonschema:"The registrant: the person or company who will own the domain. Ask the user for it; never make it up."`
	AutoRenew *bool         `json:"auto_renew,omitempty" jsonschema:"Pantech renews it for a year from credit 30 days before it expires. Default true."`
	Privacy   *bool         `json:"privacy,omitempty" jsonschema:"Hide the contact details from the public registration record. Default true."`
	Confirmation
}

type transferDomainInput struct {
	Name      string        `json:"name" jsonschema:"The domain to move to Pantech, e.g. example.com."`
	AuthCode  string        `json:"auth_code" jsonschema:"The transfer (auth or EPP) code from the domain's current registrar. Unlock the domain there first."`
	Years     *int          `json:"years,omitempty" jsonschema:"1 to 10 years; a transfer includes the first. Default 1."`
	Contact   domainContact `json:"contact" jsonschema:"The registrant: the person or company who owns the domain. Ask the user for it; never make it up."`
	AutoRenew *bool         `json:"auto_renew,omitempty" jsonschema:"Pantech renews it for a year from credit 30 days before it expires. Default true."`
	Privacy   *bool         `json:"privacy,omitempty" jsonschema:"Hide the contact details from the public registration record. Default true."`
	Confirmation
}

type renewDomainInput struct {
	DomainID string `json:"domain_id" jsonschema:"The domain's id, e.g. dom_…"`
	Years    *int   `json:"years,omitempty" jsonschema:"1 to 10 years to add. Default 1."`
	Confirmation
}

type updateDomainSettingsInput struct {
	DomainID     string `json:"domain_id" jsonschema:"The domain's id, e.g. dom_…"`
	AutoRenew    *bool  `json:"auto_renew,omitempty" jsonschema:"true: Pantech renews it for a year from credit 30 days before it expires. false: it expires unless renewed by hand."`
	TransferLock *bool  `json:"transfer_lock,omitempty" jsonschema:"true blocks transfers to another registrar. Turn it off only to move the domain away."`
	Privacy      *bool  `json:"privacy,omitempty" jsonschema:"true hides the contact details from the public registration record."`
	Confirmation
}

type setNameServersInput struct {
	DomainID      string   `json:"domain_id" jsonschema:"The domain's id, e.g. dom_…"`
	UsePantechDNS bool     `json:"use_pantech_dns" jsonschema:"true points the domain at its Pantech DNS zone (zone_id, or the zone with the domain's name, created if there is none). false uses name_servers."`
	ZoneID        string   `json:"zone_id,omitempty" jsonschema:"With use_pantech_dns: the zone to use, e.g. dnsz_…; its name must be the domain's."`
	NameServers   []string `json:"name_servers,omitempty" jsonschema:"Without use_pantech_dns: 1 to 6 name server host names, e.g. ns1.example.net."`
	Confirmation
}

type updateDomainContactInput struct {
	DomainID string        `json:"domain_id" jsonschema:"The domain's id, e.g. dom_…"`
	Contact  domainContact `json:"contact" jsonschema:"The new registrant contact, replacing the current one. Ask the user for it; never make it up."`
	Confirmation
}

type domainAuthCodeInput struct {
	DomainID string `json:"domain_id" jsonschema:"The domain's id, e.g. dom_…"`
	Confirmation
}

type domainOrderInput struct {
	OrderID string `json:"order_id" jsonschema:"The order's id, e.g. dor_…, from register_domain, renew_domain or list_domain_orders."`
}

type retryDomainOrderPaymentInput struct {
	OrderID string `json:"order_id" jsonschema:"The order's id, e.g. dor_…, of an order that is awaiting_payment (get_domain_order or list_domain_orders)."`
	Confirmation
}

// domainQuote is a paid order's price, worked out from the API's per-year
// prices exactly as the API prices it.
type domainQuote struct {
	Name       string
	Years      int
	TotalMinor int64
	Currency   string
	FirstMinor int64 // the first year: register or transfer price
	RenewMinor int64
	// PrivacyAlwaysOn is the TLD offer's flag (.ng).
	PrivacyAlwaysOn bool
}

// key is what a paid tool's token is bound to.
func (q *domainQuote) key() string {
	return fmt.Sprintf("%s|%d|%d|%s", q.Name, q.Years, q.TotalMinor, q.Currency)
}

func (q *domainQuote) price(first string) string {
	s := fmt.Sprintf("%s in total for %s", money(q.TotalMinor, q.Currency), plural(q.Years, "year", "years"))
	if q.Years > 1 {
		s += fmt.Sprintf(" (%s %s, then %s for each further year)", money(q.FirstMinor, q.Currency), first, money(q.RenewMinor, q.Currency))
	}
	return s + ", before VAT. VAT is added where it applies, and the whole amount is taken from the organization's Pantech credit when the order is placed"
}

func newQuote(name string, years int, first, renew int64, currency string) (*domainQuote, error) {
	if !currencyCode.MatchString(currency) || first < 0 || renew < 0 {
		return nil, errorf("Pantech did not return a price for this domain; try again later.")
	}
	return &domainQuote{Name: name, Years: years, Currency: currency, FirstMinor: first, RenewMinor: renew,
		TotalMinor: first + renew*int64(years-1)}, nil
}

func (c *Call) registerQuote(ctx context.Context, in registerDomainInput) (*domainQuote, error) {
	name, err := dnsName("name", in.Name)
	if err != nil {
		return nil, err
	}
	years, err := checkYears(in.Years)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []domainSearchResult `json:"data"`
	}
	if err := c.Client.Do(ctx, "GET", "/domains/search", url.Values{"query": {name}}, nil, &out); err != nil {
		return nil, err
	}
	for _, r := range out.Data {
		if r.Name != name {
			continue
		}
		switch {
		case r.Status == "unknown":
			return nil, errorf("Pantech could not check whether %s is available just now. Try again in a moment.", name)
		case r.Status != "available" || r.Price == nil:
			return nil, errorf("%s is not available to register: it is taken, premium or reserved. Try search_domains for other names.", name)
		}
		q, err := newQuote(name, years, r.Price.RegisterMinor, r.Price.RenewMinor, r.Price.Currency)
		if q != nil {
			q.PrivacyAlwaysOn = r.Price.PrivacyAlwaysOn
		}
		return q, err
	}
	return nil, errorf("%s is not sold by Pantech (its TLD is not offered). list_domain_tlds lists what is.", name)
}

func (c *Call) transferQuote(ctx context.Context, in transferDomainInput) (*domainQuote, *domainTransferCheck, error) {
	name, err := dnsName("name", in.Name)
	if err != nil {
		return nil, nil, err
	}
	years, err := checkYears(in.Years)
	if err != nil {
		return nil, nil, err
	}
	chk, err := c.transferCheck(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	if !chk.Transferable || chk.Price == nil {
		return nil, chk, errorf("%s cannot be transferred to Pantech now; the reason is in check_domain_transfer's result.", name)
	}
	q, err := newQuote(name, years, chk.Price.TransferMinor, chk.Price.RenewMinor, chk.Price.Currency)
	if q != nil {
		q.PrivacyAlwaysOn = chk.Price.PrivacyAlwaysOn
	}
	return q, chk, err
}

func (c *Call) renewQuote(ctx context.Context, in renewDomainInput) (*domainQuote, *registeredDomain, error) {
	if err := checkIDs("domain_id", in.DomainID); err != nil {
		return nil, nil, err
	}
	years, err := checkYears(in.Years)
	if err != nil {
		return nil, nil, err
	}
	d, err := c.getDomain(ctx, in.DomainID)
	if err != nil {
		return nil, nil, err
	}
	offers, err := c.listTLDs(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, o := range offers {
		if o.TLD == d.TLD {
			q, err := newQuote(d.ID, years, o.RenewMinor, o.RenewMinor, o.Currency)
			return q, d, err
		}
	}
	return nil, d, errorf("Pantech does not sell renewals for this domain's TLD just now.")
}

// creditWarning says when the organization's available credit does not
// cover a quote. It is best effort: a credit read that fails says nothing.
func (c *Call) creditWarning(ctx context.Context, q *domainQuote) []string {
	cr, err := c.Client.Billing.GetCredit(ctx)
	if err != nil || cr.Currency != q.Currency {
		return nil
	}
	note := "Available credit now: " + money(cr.AvailableMinor, cr.Currency) + "."
	if cr.AvailableMinor < q.TotalMinor {
		note += " That is less than the price (before VAT), so the order will wait up to an hour for the difference: in US dollars Pantech charges the organization's saved card (the result gives a link only if the bank asks the user to confirm the payment); in naira the user adds credit. If it is not paid in time, the order fails and nothing is charged."
	}
	return []string{note}
}

const (
	termsNote   = "Confirming accepts the domain registration agreement for the registrant. Ask the user to confirm that they accept it (it is shown at checkout in the Pantech console and in Pantech's Domains documentation), not only the price."
	refundNote  = "The amount, VAT included where it applies, is taken from credit before the registrar is asked. If the credit falls short, the order waits up to an hour for the difference: the saved card is charged it in US dollars, and in naira the user adds credit. If the order fails, all of it returns to the organization's credit."
	noUndoNote  = "This cannot be undone: once the registrar accepts it, the domain cannot be returned and the payment is not refunded."
	verifyNote  = "The registry emails the registrant to verify the contact. If it is not clicked within 15 days, the registry suspends the domain."
	privacyNote = "Privacy protection: the contact details are hidden from the public registration record."
)

// ngPrivacy is the privacy note for a TLD whose registry hides contacts.
const ngPrivacy = "Privacy protection is always on: the .ng registry hides every registrant's contact details."

// isNG reports whether a TLD is .ng or one of its second levels.
func isNG(tld string) bool { return tld == "ng" || strings.HasSuffix(tld, ".ng") }

// checkNG applies the .ng registry's rules before an order is previewed:
// privacy cannot be off, the contact needs a state and postal code, and a
// name.ng registrant is a person.
func checkNG(name string, alwaysOn bool, privacy *bool, ct *domainContact) error {
	if alwaysOn && isFalse(privacy) {
		return errorf("Privacy cannot be turned off for this domain: the .ng registry hides every registrant's contact details. Leave privacy out.")
	}
	tld := name
	if i := strings.Index(name, "."); i >= 0 {
		tld = name[i+1:]
	}
	if ct == nil || !isNG(tld) {
		return nil
	}
	if strings.TrimSpace(ct.State) == "" || strings.TrimSpace(ct.PostalCode) == "" {
		return errorf("For a .ng domain, contact.state and contact.postal_code are required.")
	}
	if tld == "name.ng" && ct.Type != "person" {
		return errorf("A name.ng domain is for people: contact.type must be person.")
	}
	return nil
}

func domainOptions(body map[string]any, autoRenew, privacy *bool) []string {
	var notes []string
	if autoRenew == nil || *autoRenew {
		notes = append(notes, "Auto-renew is on: Pantech renews it for a year from credit 30 days before it expires, retrying while credit is short.")
	} else {
		notes = append(notes, "Auto-renew is off: the domain expires unless it is renewed by hand.")
	}
	if privacy == nil || *privacy {
		notes = append(notes, privacyNote)
	} else {
		notes = append(notes, "Privacy protection is off: the registrant's name, address, email and phone are shown in the public registration record.")
	}
	if autoRenew != nil {
		body["auto_renew"] = *autoRenew
	}
	if privacy != nil {
		body["privacy"] = *privacy
	}
	return notes
}

// domainText describes a domain's state and what needs attention.
func domainText(d *registeredDomain) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Domain %s is %s.", arg(d.ID), arg(d.Status))
	if d.Status == "active" && d.ExpiresAt != nil {
		fmt.Fprintf(&b, " It expires %s; auto-renew is %s.", arg(*d.ExpiresAt), onOff(d.AutoRenew))
	}
	if d.Status == "active" {
		fmt.Fprintf(&b, " Transfer lock %s, privacy %s, name servers: %s.", onOff(d.TransferLock), onOff(d.Privacy), nameServerMode(d))
	}
	if d.FailureCode != nil {
		fmt.Fprintf(&b, " Failure: %s.", arg(*d.FailureCode))
	}
	if !d.ContactVerified && (d.Status == "active" || d.Status == "registering" || d.Status == "transferring") {
		b.WriteString(" The registrant has not verified the contact yet: they must click the registry's verification email, or the registry suspends the domain 15 days after registration.")
	}
	if d.SettingsPending {
		b.WriteString(" A settings change is being applied at the registrar.")
	}
	return b.String()
}

func nameServerMode(d *registeredDomain) string {
	if d.NameServerMode == "pantech" {
		if d.ZoneID != nil {
			return "Pantech DNS (zone " + arg(*d.ZoneID) + ")"
		}
		return "Pantech DNS"
	}
	return "custom"
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// domainOrderText describes an order and, when it failed, that its money is back.
func domainOrderText(o *domainOrder) string {
	head := fmt.Sprintf("The %s order %s for %s (%s)", arg(o.Kind), arg(o.ID), plural(o.Years, "year", "years"), money(o.AmountMinor, o.Currency))
	switch o.Status {
	case "awaiting_payment":
		text := head + " is waiting for its payment: the organization's credit does not cover the price plus VAT, and it is NOT paid yet."
		switch link := paymentLink(o.PaymentURL); {
		case link != "":
			text += " The user's bank asks them to confirm the saved-card payment of the difference: give them this link: " + link + "."
		case o.PaymentURL != nil && *o.PaymentURL != "":
			text += " The user's bank asks them to confirm the saved-card payment of the difference: the link is in the data and on the order's page in the Pantech console."
		case o.Currency == "USD":
			text += " Pantech is charging the organization's saved card the difference. If the card is declined, the user can save another card in the Pantech console, then call retry_domain_order_payment."
		default:
			text += " The user should add credit in the Pantech console: the order is paid from it as soon as it arrives, or at once with retry_domain_order_payment."
		}
		if o.PaymentExpiresAt != nil {
			text += " It waits until " + arg(*o.PaymentExpiresAt) + "; unpaid by then, the order fails and nothing is charged."
		}
		return text + " Once the payment lands the order goes on by itself; check it with get_domain_order. " + exactAmounts
	case "succeeded":
		return head + " succeeded. " + exactAmounts
	case "failed":
		text := fmt.Sprintf("%s failed (%s).", head, arg(failureCodeOf(o.FailureCode)))
		if o.FailureCode != nil && *o.FailureCode == "price_changed" {
			text += " The price changed before the order went through, and nothing was charged: get a new preview."
		} else if o.Refunded {
			text += " The full amount was returned to the organization's credit."
		} else if o.FailureCode != nil && *o.FailureCode == "insufficient_credit" {
			text += " Nothing was charged: add credit in the Pantech console and try again."
		} else {
			text += " The amount is being returned to the organization's credit."
		}
		return text
	}
	return fmt.Sprintf("%s is %s: the registrar has not finished yet. Check it with get_domain_order; transfers can take several days. %s", head, arg(o.Status), exactAmounts)
}

func init() {
	add(Spec{
		Name: "list_domain_tlds", Title: "List domain prices",
		Description: "List the domain endings (TLDs) Pantech sells, with the price per year to register, renew and transfer in, in the organization's currency. Registering for n years costs the register price plus the renew price for each further year; a transfer costs the transfer price (which adds a year) plus the renew price for each further year. " + exactAmounts,
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /domains/tlds"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		offers, err := c.listTLDs(ctx)
		if err != nil {
			return nil, err
		}
		return ok(plural(len(offers), "TLD", "TLDs")+" for sale; prices are per year, in minor units with their currency. "+exactAmounts, offers)
	})

	add(Spec{
		Name: "search_domains", Title: "Search for a domain",
		Description: "Check whether a domain is available, across the TLDs Pantech sells, with suggestions and each one's price per year. A status of unknown means it could not be checked: try again. Premium names show as unavailable. " + exactAmounts,
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /domains/search"},
	}, func(ctx context.Context, c *Call, in searchDomainsInput) (*Result, error) {
		query := strings.TrimSpace(in.Query)
		if query == "" || len(query) > 253 {
			return nil, errorf("query is required: a name such as example.com or example.")
		}
		var out struct {
			Data []domainSearchResult `json:"data"`
		}
		if err := c.Client.Do(ctx, "GET", "/domains/search", url.Values{"query": {query}}, nil, &out); err != nil {
			return nil, err
		}
		available := 0
		for _, r := range out.Data {
			if r.Status == "available" {
				available++
			}
		}
		return ok(fmt.Sprintf("%s, %d available. Each price is per year: register_minor for the first year, renew_minor for each further one. Buy one with register_domain. %s",
			plural(len(out.Data), "result", "results"), available, exactAmounts), out.Data)
	})

	add(Spec{
		Name: "check_domain_transfer", Title: "Check a domain can move to Pantech",
		Description: "Check whether a domain registered elsewhere can be transferred to Pantech now, and what the transfer costs (it adds a year). " + exactAmounts,
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /domains/transfer-check"},
	}, func(ctx context.Context, c *Call, in checkDomainTransferInput) (*Result, error) {
		name, err := dnsName("name", in.Name)
		if err != nil {
			return nil, err
		}
		chk, err := c.transferCheck(ctx, name)
		if err != nil {
			return nil, err
		}
		if !chk.Transferable {
			return ok(name+" cannot be transferred to Pantech now; the reason is in the data.", chk)
		}
		text := name + " can be transferred to Pantech."
		if chk.Price != nil && currencyCode.MatchString(chk.Price.Currency) {
			text += " The transfer costs " + money(chk.Price.TransferMinor, chk.Price.Currency) + " and adds a year. " + exactAmounts
		}
		return ok(text+" Unlock it at the current registrar and get its auth code, then transfer_domain.", chk)
	})

	add(Spec{
		Name: "list_domains", Title: "List domains",
		Description: "List the domains the organization registered or transferred in through Pantech, with status, expiry, auto-renew, transfer lock, privacy and name servers. Paginated.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /domains"},
	}, func(ctx context.Context, c *Call, in listDomainsInput) (*Result, error) {
		if in.Status != "" && !slices.Contains(domainStatuses, in.Status) {
			return nil, errorf("status must be one of %s.", strings.Join(domainStatuses, ", "))
		}
		q := pageQuery(in.pageInput)
		setIf(q, "q", in.Q)
		setIf(q, "status", in.Status)
		var page pantech.Page[registeredDomain]
		if err := c.Client.Do(ctx, "GET", "/domains", q, nil, &page); err != nil {
			return nil, err
		}
		return pageResult(&page, "domain", "domains")
	})

	add(Spec{
		Name: "get_domain", Title: "Get a domain",
		Description: "Get a domain: status, expiry, auto-renew, transfer lock, privacy, name servers and its Pantech DNS zone, whether the registrant has verified the contact, and whether a change is still being applied at the registrar.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /domains/{domain_id}"},
	}, func(ctx context.Context, c *Call, in domainInput) (*Result, error) {
		if err := checkIDs("domain_id", in.DomainID); err != nil {
			return nil, err
		}
		d, err := c.getDomain(ctx, in.DomainID)
		if err != nil {
			return nil, err
		}
		return ok(domainText(d), d)
	})

	addConfirmedWith(Spec{
		Name: "register_domain", Title: "Register a domain",
		Description: "Buy a domain for 1 to 10 years, paid in full from the organization's Pantech credit. The preview gives the exact total; if the price changes after it, the confirmation is refused. Confirming accepts the registration agreement for the registrant. The domain gets a Pantech DNS zone, privacy protection and the transfer lock, and Pantech renews it from credit when auto-renew is on. A failed registration returns the money to credit; a registered domain cannot be returned. Owners and admins only.",
		Risk:        Cost,
		Routes:      []string{"GET /domains/search", "GET /billing/credit", "POST /domains", "GET /domain-orders/{order_id}"},
	}, confirmOptions[registerDomainInput]{
		bind: func(ctx context.Context, c *Call, in registerDomainInput) (string, error) {
			q, err := c.registerQuote(ctx, in)
			if err != nil {
				return "", err
			}
			return q.key(), nil
		},
	}, func(ctx context.Context, c *Call, in registerDomainInput) (*Preview, error) {
		if err := in.Contact.check(); err != nil {
			return nil, err
		}
		q, err := c.registerQuote(ctx, in)
		if err != nil {
			return nil, err
		}
		if err := checkNG(q.Name, q.PrivacyAlwaysOn, in.Privacy, &in.Contact); err != nil {
			return nil, err
		}
		notes := append([]string{refundNote, noUndoNote, termsNote}, privacyAware(domainOptions(map[string]any{}, in.AutoRenew, in.Privacy), q.PrivacyAlwaysOn)...)
		notes = append(notes, verifyNote)
		notes = append(notes, c.creditWarning(ctx, q)...)
		return &Preview{
			Action:   fmt.Sprintf("Register the domain %s for %s, with a Pantech DNS zone for it.", q.Name, plural(q.Years, "year", "years")),
			Price:    q.price("the first year"),
			Warnings: notes,
			Data:     map[string]any{"name": q.Name, "years": q.Years, "total_minor": q.TotalMinor, "currency": q.Currency, "contact": in.Contact},
			bound:    q.key(),
		}, nil
	}, func(ctx context.Context, c *Call, in registerDomainInput) (*Result, error) {
		if err := in.Contact.check(); err != nil {
			return nil, err
		}
		name, _ := dnsName("name", in.Name)
		years, _ := checkYears(in.Years)
		body := map[string]any{"name": name, "years": years, "contact": in.Contact, "accept_terms": true}
		domainOptions(body, in.AutoRenew, in.Privacy)
		return c.placeOrder(ctx, "/domains", body)
	})

	addConfirmedWith(Spec{
		Name: "transfer_domain", Title: "Transfer a domain to Pantech",
		Description: "Move a domain registered elsewhere to Pantech, paid in full from the organization's Pantech credit; the transfer adds a year. Unlock the domain at its current registrar and get its auth code first (check_domain_transfer says if it can move). The preview gives the exact total; if the price changes after it, the confirmation is refused. Confirming accepts the registration agreement for the registrant. A failed transfer returns the money to credit. Owners and admins only.",
		Risk:        Cost,
		Routes:      []string{"GET /domains/transfer-check", "GET /billing/credit", "POST /domains/transfers", "GET /domain-orders/{order_id}"},
	}, confirmOptions[transferDomainInput]{
		bind: func(ctx context.Context, c *Call, in transferDomainInput) (string, error) {
			q, _, err := c.transferQuote(ctx, in)
			if err != nil {
				return "", err
			}
			return q.key(), nil
		},
	}, func(ctx context.Context, c *Call, in transferDomainInput) (*Preview, error) {
		if err := in.Contact.check(); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.AuthCode) == "" {
			return nil, errorf("auth_code is required: the transfer code from the domain's current registrar.")
		}
		q, _, err := c.transferQuote(ctx, in)
		if err != nil {
			return nil, err
		}
		if err := checkNG(q.Name, q.PrivacyAlwaysOn, in.Privacy, &in.Contact); err != nil {
			return nil, err
		}
		notes := []string{refundNote,
			"This cannot be undone once the transfer completes: the payment is not refunded.",
			termsNote,
			"A transfer can take several days. The current registrar may ask the registrant to approve it by email. The domain keeps its name servers, so it keeps working meanwhile.",
		}
		notes = append(notes, privacyAware(domainOptions(map[string]any{}, in.AutoRenew, in.Privacy), q.PrivacyAlwaysOn)...)
		notes = append(notes, c.creditWarning(ctx, q)...)
		return &Preview{
			Action:   fmt.Sprintf("Transfer the domain %s to Pantech, adding %s to its registration.", q.Name, plural(q.Years, "year", "years")),
			Price:    q.price("for the transfer"),
			Warnings: notes,
			// The auth code is a secret: it is in the arguments only.
			Data:  map[string]any{"name": q.Name, "years": q.Years, "total_minor": q.TotalMinor, "currency": q.Currency, "contact": in.Contact},
			bound: q.key(),
		}, nil
	}, func(ctx context.Context, c *Call, in transferDomainInput) (*Result, error) {
		if err := in.Contact.check(); err != nil {
			return nil, err
		}
		name, _ := dnsName("name", in.Name)
		years, _ := checkYears(in.Years)
		body := map[string]any{"name": name, "auth_code": in.AuthCode, "years": years, "contact": in.Contact, "accept_terms": true}
		domainOptions(body, in.AutoRenew, in.Privacy)
		return c.placeOrder(ctx, "/domains/transfers", body)
	})

	addConfirmedWith(Spec{
		Name: "renew_domain", Title: "Renew a domain",
		Description: "Add 1 to 10 years to a domain now, paid in full from the organization's Pantech credit. The preview gives the exact total; if the price changes after it, the confirmation is refused. With auto-renew on, Pantech already renews a year 30 days before expiry. A failed renewal returns the money to credit. Owners and admins only.",
		Risk:        Cost,
		Routes:      []string{"GET /domains/{domain_id}", "GET /domains/tlds", "GET /billing/credit", "POST /domains/{domain_id}/renew", "GET /domain-orders/{order_id}"},
	}, confirmOptions[renewDomainInput]{
		bind: func(ctx context.Context, c *Call, in renewDomainInput) (string, error) {
			q, _, err := c.renewQuote(ctx, in)
			if err != nil {
				return "", err
			}
			return q.key(), nil
		},
	}, func(ctx context.Context, c *Call, in renewDomainInput) (*Preview, error) {
		q, d, err := c.renewQuote(ctx, in)
		if err != nil {
			return nil, err
		}
		notes := []string{refundNote, noUndoNote}
		if d.ExpiresAt != nil {
			notes = append(notes, "It now expires "+arg(*d.ExpiresAt)+"; the renewal adds the years to that date.")
		}
		notes = append(notes, c.creditWarning(ctx, q)...)
		return &Preview{
			Action:   fmt.Sprintf("Renew domain %s for %s.", d.ID, plural(q.Years, "year", "years")),
			Price:    q.price("the first year"),
			Warnings: notes,
			Data:     map[string]any{"domain": d, "years": q.Years, "total_minor": q.TotalMinor, "currency": q.Currency},
			bound:    q.key(),
		}, nil
	}, func(ctx context.Context, c *Call, in renewDomainInput) (*Result, error) {
		years, _ := checkYears(in.Years)
		return c.placeOrder(ctx, "/domains/"+in.DomainID+"/renew", map[string]any{"years": years})
	})

	addConfirmedWith(Spec{
		Name: "update_domain_settings", Title: "Change a domain's settings",
		Description: "Turn a domain's auto-renew, transfer lock or privacy protection on or off. Turning one off is confirmed: without auto-renew the domain expires unless renewed by hand, without the lock it can be moved away with its auth code (owners and admins are emailed a security notice), and without privacy the contact details are public. Applied at the registrar within minutes.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		ConfirmWhen: "when it turns auto_renew, transfer_lock or privacy off",
		Routes:      []string{"GET /domains/{domain_id}", "PATCH /domains/{domain_id}"},
	}, confirmOptions[updateDomainSettingsInput]{
		when: turnsProtectionOff,
	}, func(ctx context.Context, c *Call, in updateDomainSettingsInput) (*Preview, error) {
		body, err := settingsChange(in)
		if err != nil {
			return nil, err
		}
		d, err := c.getDomain(ctx, in.DomainID)
		if err != nil {
			return nil, err
		}
		var notes []string
		if isFalse(in.AutoRenew) {
			expiry := "on its expiry date"
			if d.ExpiresAt != nil {
				expiry = "on " + arg(*d.ExpiresAt)
			}
			notes = append(notes, "With auto-renew off, Pantech does not renew the domain: it expires "+expiry+" unless renewed by hand (renew_domain), and can then be lost.")
		}
		if isFalse(in.TransferLock) {
			notes = append(notes, "With the transfer lock off, anyone holding the domain's auth code can move it to another registrar. The organization's owners and admins are emailed a security notice. Turn it back on when no transfer is planned.")
		}
		if isFalse(in.Privacy) {
			if isNG(d.TLD) {
				return nil, errorf("Privacy cannot be turned off for domain %s: the .ng registry hides every registrant's contact details.", d.ID)
			}
			notes = append(notes, "With privacy off, the registrant's name, address, email and phone are shown in the public registration record.")
		}
		return &Preview{
			Action:   fmt.Sprintf("Change the settings of domain %s (the change is in the data).", d.ID),
			Warnings: notes,
			Data:     map[string]any{"domain": d, "change": body},
		}, nil
	}, func(ctx context.Context, c *Call, in updateDomainSettingsInput) (*Result, error) {
		body, err := settingsChange(in)
		if err != nil {
			return nil, err
		}
		var d registeredDomain
		if err := c.Client.Do(ctx, "PATCH", "/domains/"+in.DomainID, nil, body, &d, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok("Settings saved. "+domainText(&d), d)
	})

	addConfirmed(Spec{
		Name: "set_domain_name_servers", Title: "Set a domain's name servers",
		Description: "Choose who answers DNS for a domain: its Pantech DNS zone, or 1 to 6 custom name servers. This replaces the current name servers, so names served only by the old ones stop resolving as caches expire (up to two days). Applied at the registrar within minutes.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /domains/{domain_id}", "PUT /domains/{domain_id}/name-servers"},
	}, func(ctx context.Context, c *Call, in setNameServersInput) (*Preview, error) {
		body, err := nameServerChange(in)
		if err != nil {
			return nil, err
		}
		d, err := c.getDomain(ctx, in.DomainID)
		if err != nil {
			return nil, err
		}
		target := plural(len(in.NameServers), "custom name server", "custom name servers")
		if in.UsePantechDNS {
			target = "its Pantech DNS zone"
			if in.ZoneID != "" {
				target = "Pantech DNS zone " + in.ZoneID
			}
		}
		return &Preview{
			Action: fmt.Sprintf("Point domain %s at %s, replacing its current name servers (%s).", d.ID, target, nameServerMode(d)),
			Warnings: []string{
				"Make sure the new name servers already have the domain's records: anything only on the old ones stops resolving as resolvers' caches expire, which can take up to two days.",
			},
			Data: map[string]any{"domain": d, "change": body},
		}, nil
	}, func(ctx context.Context, c *Call, in setNameServersInput) (*Result, error) {
		body, err := nameServerChange(in)
		if err != nil {
			return nil, err
		}
		var d registeredDomain
		if err := c.Client.Do(ctx, "PUT", "/domains/"+in.DomainID+"/name-servers", nil, body, &d, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok("Name servers are being changed at the registrar. "+domainText(&d), d)
	})

	add(Spec{
		Name: "get_domain_contact", Title: "Get a domain's registrant contact",
		Description: "Get a domain's registrant contact: personal data, for owners and admins only (it needs Manage access). Use it only when the user asks to see or change the contact, and do not repeat it beyond what they need.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /domains/{domain_id}/contact"},
	}, func(ctx context.Context, c *Call, in domainInput) (*Result, error) {
		if err := checkIDs("domain_id", in.DomainID); err != nil {
			return nil, err
		}
		var ct domainContact
		if err := c.Client.Do(ctx, "GET", "/domains/"+in.DomainID+"/contact", nil, nil, &ct); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Registrant contact of domain %s, in the data. It is personal data: show it only to the user who asked.", in.DomainID), ct)
	})

	addConfirmed(Spec{
		Name: "update_domain_contact", Title: "Change a domain's registrant contact",
		Description: "Replace a domain's registrant contact at the registry. A new email may have to be verified again: if the registry's email is not clicked within 15 days, it suspends the domain. Owners and admins only.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /domains/{domain_id}", "PUT /domains/{domain_id}/contact"},
	}, func(ctx context.Context, c *Call, in updateDomainContactInput) (*Preview, error) {
		if err := checkIDs("domain_id", in.DomainID); err != nil {
			return nil, err
		}
		if err := in.Contact.check(); err != nil {
			return nil, err
		}
		d, err := c.getDomain(ctx, in.DomainID)
		if err != nil {
			return nil, err
		}
		if isNG(d.TLD) {
			if err := checkNG(d.Name, false, nil, &in.Contact); err != nil {
				return nil, err
			}
		}
		return &Preview{
			Action: fmt.Sprintf("Replace the registrant contact of domain %s with the one in the data.", d.ID),
			Warnings: []string{
				"The registrant is the domain's legal owner: check every detail with the user.",
				"If the email changes, the registry may send a new verification email; not clicking it within 15 days gets the domain suspended.",
			},
			Data: map[string]any{"domain": d, "contact": in.Contact},
		}, nil
	}, func(ctx context.Context, c *Call, in updateDomainContactInput) (*Result, error) {
		var d registeredDomain
		if err := c.Client.Do(ctx, "PUT", "/domains/"+in.DomainID+"/contact", nil, map[string]any{"contact": in.Contact}, &d, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok("The registrant contact is being updated at the registry. "+domainText(&d), d)
	})

	addConfirmed(Spec{
		Name: "get_domain_auth_code", Title: "Get a domain's transfer code",
		Description: "Get a domain's auth code, which lets it be transferred away from Pantech to another registrar. Turn the transfer lock off first (update_domain_settings). The organization's owners and admins are emailed a security notice. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /domains/{domain_id}", "POST /domains/{domain_id}/auth-code", "GET /domains/{domain_id}/auth-code"},
	}, func(ctx context.Context, c *Call, in domainAuthCodeInput) (*Preview, error) {
		if err := checkIDs("domain_id", in.DomainID); err != nil {
			return nil, err
		}
		d, err := c.getDomain(ctx, in.DomainID)
		if err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Get the auth code of domain %s. Whoever holds it can transfer the domain away from Pantech.", d.ID),
			Warnings: []string{
				deleteAccessNote,
				"Give the code only to the user, and only to start a transfer they want. The organization's owners and admins are emailed a security notice.",
			},
			Data: map[string]any{"domain": d},
		}
		if d.TransferLock {
			p.Warnings = append(p.Warnings, "The transfer lock is on: Pantech refuses until it is turned off with update_domain_settings.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in domainAuthCodeInput) (*Result, error) {
		var d registeredDomain
		if err := c.Client.Do(ctx, "POST", "/domains/"+in.DomainID+"/auth-code", nil, nil, &d, c.writeOpts()...); err != nil {
			return nil, err
		}
		var code struct {
			AuthCode  string `json:"auth_code"`
			ExpiresAt string `json:"expires_at"`
			Status    string `json:"status"`
		}
		c.poll(ctx, func() (string, bool) {
			code.AuthCode, code.Status = "", ""
			if err := c.Client.Do(ctx, "GET", "/domains/"+in.DomainID+"/auth-code", nil, nil, &code); err != nil {
				return "", true
			}
			return "the auth code is " + code.Status, code.AuthCode != ""
		})
		if code.AuthCode == "" {
			return ok(fmt.Sprintf("The registrar is preparing the auth code of domain %s. Call get_domain_auth_code again in a minute.", in.DomainID),
				map[string]any{"domain_id": in.DomainID, "status": "pending"})
		}
		text := fmt.Sprintf("The auth code of domain %s is in the data. Give it only to the user, for the new registrar.", in.DomainID)
		if code.ExpiresAt != "" {
			text += " It expires " + arg(code.ExpiresAt) + "."
		}
		return ok(text, map[string]any{"domain_id": in.DomainID, "auth_code": code.AuthCode, "expires_at": code.ExpiresAt})
	})

	add(Spec{
		Name: "list_domain_orders", Title: "List a domain's orders",
		Description: "List a domain's paid orders (register, renew, transfer), newest first: amount, status, and whether a failed one was returned to credit. " + exactAmounts,
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /domains/{domain_id}/orders"},
	}, func(ctx context.Context, c *Call, in domainInput) (*Result, error) {
		if err := checkIDs("domain_id", in.DomainID); err != nil {
			return nil, err
		}
		var out struct {
			Data []domainOrder `json:"data"`
		}
		if err := c.Client.Do(ctx, "GET", "/domains/"+in.DomainID+"/orders", nil, nil, &out); err != nil {
			return nil, err
		}
		return ok(plural(len(out.Data), "order", "orders")+". "+exactAmounts, out.Data)
	})

	add(Spec{
		Name: "get_domain_order", Title: "Get a domain order",
		Description: "Get a domain order: pending, paid, submitted, then succeeded or failed with a reason; a failed order's amount returns to credit. " + exactAmounts,
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /domain-orders/{order_id}"},
	}, func(ctx context.Context, c *Call, in domainOrderInput) (*Result, error) {
		if err := checkIDs("order_id", in.OrderID); err != nil {
			return nil, err
		}
		o, err := c.getDomainOrder(ctx, in.OrderID)
		if err != nil {
			return nil, err
		}
		return ok(domainOrderText(o), o)
	})

	addConfirmedWith(Spec{
		Name: "retry_domain_order_payment", Title: "Retry a domain order's payment",
		Description: "Try the payment of a domain order that is awaiting_payment again now, and give it another hour. An order the organization's credit does not cover waits an hour for the difference: in US dollars Pantech charges the saved card (a payment_url appears only if the bank asks the user to confirm the payment); in naira the user adds credit. Retrying charges the saved card once more (after a decline, or once a new card is saved) or takes credit just added. The order is never charged twice. An order that is not waiting for a payment, or whose hour has ended, is refused: place it again. " + exactAmounts,
		Risk:        Cost,
		Routes:      []string{"GET /domain-orders/{order_id}", "GET /billing/credit", "POST /domain-orders/{order_id}/retry-payment"},
	}, confirmOptions[retryDomainOrderPaymentInput]{
		bind: func(ctx context.Context, c *Call, in retryDomainOrderPaymentInput) (string, error) {
			o, err := c.awaitingOrder(ctx, in.OrderID)
			if err != nil {
				return "", err
			}
			return orderKey(o), nil
		},
	}, func(ctx context.Context, c *Call, in retryDomainOrderPaymentInput) (*Preview, error) {
		o, err := c.awaitingOrder(ctx, in.OrderID)
		if err != nil {
			return nil, err
		}
		notes := []string{"The order is never charged twice: retrying only makes it due again, and anything paid stays in the organization's credit if the order fails."}
		if o.Currency == "USD" {
			notes = append(notes, "Pantech charges the organization's saved card what the credit does not cover. If the bank asks the user to confirm the payment, the result gives them the link.")
		} else {
			notes = append(notes, "The order is paid from the organization's credit, including credit just added; nothing is charged to a card.")
		}
		if q, err := newQuote(o.ID, 1, o.AmountMinor, 0, o.Currency); err == nil {
			notes = append(notes, c.creditWarning(ctx, q)...)
		}
		return &Preview{
			Action:   fmt.Sprintf("Retry the payment of the %s order %s for %s now, and wait another hour for it.", arg(o.Kind), arg(o.ID), plural(o.Years, "year", "years")),
			Price:    money(o.AmountMinor, o.Currency) + " in total for the order, before VAT; only the difference the credit does not cover is charged",
			Warnings: notes,
			Data:     map[string]any{"order": o},
			bound:    orderKey(o),
		}, nil
	}, func(ctx context.Context, c *Call, in retryDomainOrderPaymentInput) (*Result, error) {
		var o domainOrder
		if err := c.Client.Do(ctx, "POST", "/domain-orders/"+in.OrderID+"/retry-payment", nil, nil, &o, c.writeOpts()...); err != nil {
			return nil, err
		}
		last := &o
		c.poll(ctx, func() (string, bool) {
			next, err := c.getDomainOrder(ctx, in.OrderID)
			if err != nil {
				return "", true
			}
			last = next
			return "order " + in.OrderID + " is " + next.Status, next.Status != "awaiting_payment" || paymentLink(next.PaymentURL) != ""
		})
		return ok("The payment was retried. "+domainOrderText(last), last)
	})
}

// awaitingOrder reads the order a payment retry is for, and refuses one
// that is not waiting for its payment.
func (c *Call) awaitingOrder(ctx context.Context, id string) (*domainOrder, error) {
	if err := checkIDs("order_id", id); err != nil {
		return nil, err
	}
	o, err := c.getDomainOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	if o.Status != "awaiting_payment" {
		return nil, errorf("Order %s is %s, not waiting for a payment: there is nothing to retry. An order whose hour ended has failed; place it again.", arg(o.ID), arg(o.Status))
	}
	return o, nil
}

// orderKey is what a payment retry's token is bound to: the order, still
// awaiting payment, at the amount the user saw.
func orderKey(o *domainOrder) string {
	return fmt.Sprintf("%s|%s|%d|%s", o.ID, o.Status, o.AmountMinor, o.Currency)
}

func isFalse(b *bool) bool { return b != nil && !*b }

// privacyAware swaps the privacy note for the .ng one when the registry
// keeps privacy on.
func privacyAware(notes []string, alwaysOn bool) []string {
	if !alwaysOn {
		return notes
	}
	out := notes[:0:0]
	for _, n := range notes {
		if strings.HasPrefix(n, "Privacy protection") {
			n = ngPrivacy
		}
		out = append(out, n)
	}
	return out
}

// paymentLink returns an order's card payment link when it is a plain https
// link safe to put in our own text, and "" otherwise.
func paymentLink(p *string) string {
	if p == nil || len(*p) > 2048 || strings.ContainsAny(*p, " \t\r\n\"'<>`") {
		return ""
	}
	u, err := url.Parse(*p)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	return *p
}

// turnsProtectionOff reports whether a settings change turns auto-renew,
// the transfer lock or privacy off: those are confirmed.
func turnsProtectionOff(in updateDomainSettingsInput) bool {
	return isFalse(in.AutoRenew) || isFalse(in.TransferLock) || isFalse(in.Privacy)
}

func settingsChange(in updateDomainSettingsInput) (map[string]any, error) {
	if err := checkIDs("domain_id", in.DomainID); err != nil {
		return nil, err
	}
	body := map[string]any{}
	if in.AutoRenew != nil {
		body["auto_renew"] = *in.AutoRenew
	}
	if in.TransferLock != nil {
		body["transfer_lock"] = *in.TransferLock
	}
	if in.Privacy != nil {
		body["privacy"] = *in.Privacy
	}
	if len(body) == 0 {
		return nil, errorf("Nothing to change: give auto_renew, transfer_lock or privacy.")
	}
	return body, nil
}

func nameServerChange(in setNameServersInput) (map[string]any, error) {
	if err := checkIDs("domain_id", in.DomainID); err != nil {
		return nil, err
	}
	if in.UsePantechDNS {
		if len(in.NameServers) > 0 {
			return nil, errorf("Give name_servers only with use_pantech_dns false.")
		}
		body := map[string]any{"use_pantech_dns": true}
		if in.ZoneID != "" {
			if err := checkIDs("zone_id", in.ZoneID); err != nil {
				return nil, err
			}
			body["zone_id"] = in.ZoneID
		}
		return body, nil
	}
	if in.ZoneID != "" {
		return nil, errorf("Give zone_id only with use_pantech_dns true.")
	}
	if len(in.NameServers) < 1 || len(in.NameServers) > 6 {
		return nil, errorf("name_servers must have 1 to 6 host names, or set use_pantech_dns to true.")
	}
	ns := make([]string, len(in.NameServers))
	for i, n := range in.NameServers {
		name, err := dnsName(fmt.Sprintf("name_servers[%d]", i), n)
		if err != nil {
			return nil, err
		}
		ns[i] = name
	}
	return map[string]any{"use_pantech_dns": false, "name_servers": ns}, nil
}

func (c *Call) getDomain(ctx context.Context, id string) (*registeredDomain, error) {
	var d registeredDomain
	if err := c.Client.Do(ctx, "GET", "/domains/"+id, nil, nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (c *Call) getDomainOrder(ctx context.Context, id string) (*domainOrder, error) {
	var o domainOrder
	if err := c.Client.Do(ctx, "GET", "/domain-orders/"+id, nil, nil, &o); err != nil {
		return nil, err
	}
	return &o, nil
}

func (c *Call) listTLDs(ctx context.Context) ([]tldOffer, error) {
	var out struct {
		Data []tldOffer `json:"data"`
	}
	if err := c.Client.Do(ctx, "GET", "/domains/tlds", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (c *Call) transferCheck(ctx context.Context, name string) (*domainTransferCheck, error) {
	var chk domainTransferCheck
	if err := c.Client.Do(ctx, "GET", "/domains/transfer-check", url.Values{"name": {name}}, nil, &chk); err != nil {
		return nil, err
	}
	return &chk, nil
}

// placeOrder sends a paid order, then follows it within the wait budget.
func (c *Call) placeOrder(ctx context.Context, path string, body map[string]any) (*Result, error) {
	var res domainOrderResult
	if err := c.Client.Do(ctx, "POST", path, nil, body, &res, c.writeOpts()...); err != nil {
		return nil, err
	}
	last := &res.Order
	if checkIDs("order_id", res.Order.ID) == nil {
		c.poll(ctx, func() (string, bool) {
			next, err := c.getDomainOrder(ctx, res.Order.ID)
			if err != nil {
				return "", true
			}
			last = next
			return "order " + res.Order.ID + " is " + next.Status, next.Status == "succeeded" || next.Status == "failed" || next.Status == "awaiting_payment"
		})
	}
	text := domainOrderText(last)
	if last.Status == "succeeded" && last.Kind == "register" {
		text += " Next: the registrant should click the registry's verification email within 15 days."
	}
	return ok(text, map[string]any{"domain": res.Domain, "order": last})
}
