package tools

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

// Certificate Manager issues free public TLS certificates, validated through
// DNS: Pantech writes the challenge records itself for names in the
// organization's active Pantech DNS zones, and anything else needs one CNAME
// the customer adds once. Certificates can also be imported.
//
// Private keys are secrets. An import's or reimport's key is sent to the API
// and never shown back: previews describe the certificate parsed here,
// without the PEM. An export's key is returned only in the fenced data, after
// a confirmed preview, with a warning in the summary.

type certificateValidation struct {
	Name        string  `json:"name"`
	Method      string  `json:"method"`
	ZoneID      *string `json:"zone_id"`
	CNAMEName   *string `json:"cname_name"`
	CNAMETarget *string `json:"cname_target"`
	Status      string  `json:"status"`
}

type certificateUsage struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type certificate struct {
	ID                   string                  `json:"id"`
	Kind                 string                  `json:"kind"`
	Names                []string                `json:"names"`
	CommonName           *string                 `json:"common_name"`
	Description          string                  `json:"description"`
	KeyType              string                  `json:"key_type"`
	Exportable           bool                    `json:"exportable"`
	Status               string                  `json:"status"`
	PendingReason        *string                 `json:"pending_reason"`
	FailureCode          *string                 `json:"failure_code"`
	Validations          []certificateValidation `json:"validations"`
	ValidationDeadlineAt *string                 `json:"validation_deadline_at"`
	Issuer               *string                 `json:"issuer"`
	SerialNumber         *string                 `json:"serial_number"`
	FingerprintSHA256    *string                 `json:"fingerprint_sha256"`
	NotBefore            *string                 `json:"not_before"`
	NotAfter             *string                 `json:"not_after"`
	IssuedAt             *string                 `json:"issued_at"`
	RenewAfter           *string                 `json:"renew_after"`
	RenewalStatus        *string                 `json:"renewal_status"`
	InUseBy              []certificateUsage      `json:"in_use_by"`
	RevocationPending    bool                    `json:"revocation_pending"`
	RevokedAt            *string                 `json:"revoked_at"`
	CreatedAt            string                  `json:"created_at"`
	UpdatedAt            string                  `json:"updated_at"`
	Version              int64                   `json:"version"`
}

type exportedCertificate struct {
	CertificatePEM string `json:"certificate_pem"`
	ChainPEM       string `json:"chain_pem"`
	PrivateKeyPEM  string `json:"private_key_pem"`
	NotAfter       string `json:"not_after"`
}

var (
	certificateStatuses = []string{"pending_validation", "issuing", "issued", "renewing", "expired", "revoked", "failed"}
	// challengeHost is a CNAME name or target safe to quote in our text.
	challengeHost = regexp.MustCompile(`^[a-z0-9_][a-z0-9_.-]{0,252}$`)
)

// certificateName normalizes one requested name: a DNS name, or one with a
// whole left-most "*" label.
func certificateName(i int, raw string) (string, error) {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	base, wildcard := strings.CutPrefix(name, "*.")
	if len(name) > 253 || !dnsNamePattern.MatchString(base) || (!wildcard && strings.Contains(name, "*")) {
		return "", errorf("names[%d] must be a domain name such as example.com, or a wildcard such as *.example.com (the whole left-most label only).", i)
	}
	return name, nil
}

func certificateNames(raw []string) ([]string, error) {
	if len(raw) < 1 || len(raw) > 100 {
		return nil, errorf("names must have 1 to 100 entries.")
	}
	var out []string
	for i, r := range raw {
		n, err := certificateName(i, r)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// parsedPEM is what a preview says about a certificate to import, read here
// from the PEM: never the PEM or the key itself.
type parsedPEM struct {
	Names     []string `json:"names"`
	Subject   string   `json:"subject"`
	Issuer    string   `json:"issuer"`
	NotBefore string   `json:"not_before"`
	NotAfter  string   `json:"not_after"`
	KeyType   string   `json:"key_type"`
}

// parseImport checks a certificate and key the way the API will (the key
// matches the leaf, the leaf is not a CA) and describes the leaf. The API
// still makes every other check.
func parseImport(certPEM, chainPEM, keyPEM string) (*parsedPEM, error) {
	if len(certPEM)+len(chainPEM)+len(keyPEM) > 64<<10 {
		return nil, errorf("The certificate, chain and key are more than 64 KiB in all.")
	}
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errorf("certificate_pem must be one PEM certificate (-----BEGIN CERTIFICATE-----).")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil || leaf.IsCA {
		return nil, errorf("certificate_pem is not a server certificate that can be read.")
	}
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		return nil, errorf("private_key_pem must be the unencrypted private key (PKCS #1, PKCS #8 or SEC 1) of this certificate.")
	}
	p := &parsedPEM{
		Names:     leaf.DNSNames,
		Subject:   leaf.Subject.CommonName,
		Issuer:    leaf.Issuer.CommonName,
		NotBefore: leaf.NotBefore.UTC().Format("2006-01-02T15:04:05Z"),
		NotAfter:  leaf.NotAfter.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if len(p.Names) == 0 && p.Subject != "" {
		p.Names = []string{p.Subject}
	}
	switch k := leaf.PublicKey.(type) {
	case *ecdsa.PublicKey:
		p.KeyType = "ecdsa-" + strings.ToLower(strings.ReplaceAll(k.Curve.Params().Name, "-", ""))
	case *rsa.PublicKey:
		p.KeyType = fmt.Sprintf("rsa-%d", k.N.BitLen())
	default:
		return nil, errorf("certificate_pem has a key type Pantech does not support: RSA 2048 to 4096 bits, or ECDSA P-256 or P-384.")
	}
	return p, nil
}

// certificateText describes a certificate's state and the next step.
func certificateText(ct *certificate) string {
	id := "Certificate " + arg(ct.ID) + " (" + plural(len(ct.Names), "name", "names") + ")"
	switch ct.Status {
	case "pending_validation", "issuing":
		var cnames []string
		for _, v := range ct.Validations {
			if v.Method == "cname" && v.Status == "pending" && v.CNAMEName != nil && v.CNAMETarget != nil &&
				challengeHost.MatchString(*v.CNAMEName) && challengeHost.MatchString(*v.CNAMETarget) {
				entry := *v.CNAMEName + " CNAME " + *v.CNAMETarget
				if !slices.Contains(cnames, entry) {
					cnames = append(cnames, entry)
				}
			}
		}
		if len(cnames) > 0 {
			text := fmt.Sprintf("%s is waiting for validation. At the DNS host of these names, add once: %s. Renewals then stay automatic as long as the records stay. Then call verify_certificate.", id, strings.Join(cnames, "; "))
			if ct.ValidationDeadlineAt != nil {
				text += " The request fails if they are not in place by " + arg(*ct.ValidationDeadlineAt) + "."
			}
			return text
		}
		return fmt.Sprintf("%s is being validated and issued (%s); with Pantech DNS this usually takes under two minutes. Call get_certificate shortly.", id, arg(failureCodeOf(ct.PendingReason)))
	case "issued", "renewing":
		text := id + " is issued"
		if ct.NotAfter != nil {
			text += ", valid until " + arg(*ct.NotAfter)
		}
		text += "."
		if ct.Kind == "public" {
			text += " Pantech renews it automatically from 30 days before it expires."
		} else {
			text += " It is imported, so Pantech does not renew it: reimport_certificate replaces it in place."
		}
		if ct.Status == "renewing" {
			text += " A renewal is in progress; the current certificate keeps working."
		}
		if ct.RenewalStatus != nil && *ct.RenewalStatus == "failing" {
			text += " Renewal is failing (" + arg(failureCodeOf(ct.PendingReason)) + "): check the validations in the data."
		}
		return text
	case "failed":
		return fmt.Sprintf("%s failed (%s). It can be requested again.", id, arg(failureCodeOf(ct.FailureCode)))
	}
	return fmt.Sprintf("%s is %s.", id, arg(ct.Status))
}

type listCertificatesInput struct {
	Q      string `json:"q,omitempty" jsonschema:"Only certificates with a name containing this text."`
	Status string `json:"status,omitempty" jsonschema:"pending_validation, issuing, issued, renewing, expired, revoked or failed."`
	Kind   string `json:"kind,omitempty" jsonschema:"public (issued by Pantech) or imported."`
	pageInput
}

type certificateInput struct {
	CertificateID string `json:"certificate_id" jsonschema:"The certificate's id, e.g. cert_…, from list_certificates."`
}

type requestCertificateInput struct {
	Names       []string `json:"names" jsonschema:"1 to 100 names, e.g. example.com and *.example.com (a wildcard is the whole left-most label). The first is the common name. Names are published in public Certificate Transparency logs: do not request private names."`
	KeyType     string   `json:"key_type,omitempty" jsonschema:"ecdsa-p256 (default) or rsa-2048."`
	Exportable  bool     `json:"exportable,omitempty" jsonschema:"true lets owners and admins export the private key later, to use on their own servers. Fixed at creation. Default false: only Pantech services can use it."`
	Description string   `json:"description,omitempty" jsonschema:"A note for the team."`
	Confirmation
}

type importCertificateInput struct {
	CertificatePEM string `json:"certificate_pem" jsonschema:"The certificate, PEM."`
	ChainPEM       string `json:"chain_pem,omitempty" jsonschema:"The intermediate certificates, PEM, at most 10."`
	PrivateKeyPEM  string `json:"private_key_pem" jsonschema:"The certificate's unencrypted private key, PEM (PKCS #1, PKCS #8 or SEC 1). It is sent to Pantech and never shown back."`
	Exportable     bool   `json:"exportable,omitempty" jsonschema:"true lets owners and admins export it again later. Fixed at import."`
	Description    string `json:"description,omitempty" jsonschema:"A note for the team."`
	Confirmation
}

type updateCertificateInput struct {
	CertificateID string `json:"certificate_id" jsonschema:"The certificate's id, e.g. cert_…"`
	Description   string `json:"description" jsonschema:"The new note."`
}

type reimportCertificateInput struct {
	CertificateID  string `json:"certificate_id" jsonschema:"The imported certificate's id, e.g. cert_…"`
	CertificatePEM string `json:"certificate_pem" jsonschema:"The new certificate, PEM, for exactly the same names."`
	ChainPEM       string `json:"chain_pem,omitempty" jsonschema:"The intermediate certificates, PEM."`
	PrivateKeyPEM  string `json:"private_key_pem" jsonschema:"The new certificate's unencrypted private key, PEM. It is sent to Pantech and never shown back."`
	Confirmation
}

type certificateConfirmInput struct {
	CertificateID string `json:"certificate_id" jsonschema:"The certificate's id, e.g. cert_…"`
	Confirmation
}

type revokeCertificateInput struct {
	CertificateID string `json:"certificate_id" jsonschema:"The certificate's id, e.g. cert_…"`
	Reason        string `json:"reason,omitempty" jsonschema:"unspecified (default) or key_compromise, when the private key may be known to someone else."`
	Confirmation
}

func certificatePath(id string) string { return "/certificates/" + id }

func init() {
	add(Spec{
		Name: "list_certificates", Title: "List certificates",
		Description: "List the organization's TLS certificates, newest first: names, kind (public, issued free by Pantech, or imported), status, expiry and whether the key can be exported. Paginated.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /certificates"},
	}, func(ctx context.Context, c *Call, in listCertificatesInput) (*Result, error) {
		if in.Status != "" && !slices.Contains(certificateStatuses, in.Status) {
			return nil, errorf("status must be one of %s.", strings.Join(certificateStatuses, ", "))
		}
		if in.Kind != "" && in.Kind != "public" && in.Kind != "imported" {
			return nil, errorf("kind must be public or imported.")
		}
		q := pageQuery(in.pageInput)
		setIf(q, "q", in.Q)
		setIf(q, "status", in.Status)
		setIf(q, "kind", in.Kind)
		var page pantech.Page[certificate]
		if err := c.Client.Do(ctx, "GET", "/certificates", q, nil, &page); err != nil {
			return nil, err
		}
		return pageResult(&page, "certificate", "certificates")
	})

	add(Spec{
		Name: "get_certificate", Title: "Get a certificate",
		Description: "Get a certificate: its names, status and what to do next (the CNAME records to add when a name is not in a Pantech DNS zone), validity, renewal and what uses it. Never the private key.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /certificates/{certificate_id}"},
	}, func(ctx context.Context, c *Call, in certificateInput) (*Result, error) {
		ct, err := c.getCertificate(ctx, in.CertificateID)
		if err != nil {
			return nil, err
		}
		return ok(certificateText(ct), ct)
	})

	addConfirmedWith(Spec{
		Name: "request_certificate", Title: "Request a certificate",
		Description: "Request a free public TLS certificate for 1 to 100 names, wildcards included, from a publicly trusted certificate authority. Names in the organization's active Pantech DNS zones are validated automatically: Pantech adds and then removes _acme-challenge TXT records there. Any other name needs one CNAME at its DNS host, shown in the result; renewals then stay automatic. Usually issued within two minutes with Pantech DNS. Owners and admins; 20 requests a day, 100 certificates.",
		Risk:        Exposure,
		ConfirmWhen: "when Pantech will write validation records in the organization's Pantech DNS zones",
		Routes:      []string{"GET /dns/zones", "POST /certificates", "GET /certificates/{certificate_id}"},
	}, confirmOptions[requestCertificateInput]{
		whenLive: func(ctx context.Context, c *Call, in requestCertificateInput) (bool, error) {
			names, err := certificateNames(in.Names)
			if err != nil {
				return false, err
			}
			zones, err := c.validationZones(ctx, names)
			return len(zones) > 0, err
		},
	}, func(ctx context.Context, c *Call, in requestCertificateInput) (*Preview, error) {
		names, err := checkRequest(in)
		if err != nil {
			return nil, err
		}
		zones, err := c.validationZones(ctx, names)
		if err != nil {
			return nil, err
		}
		var ids []string
		for _, z := range zones {
			if !slices.Contains(ids, z.ID) {
				ids = append(ids, z.ID)
			}
		}
		return &Preview{
			Action: fmt.Sprintf("Request a free public certificate for %s. Pantech will add _acme-challenge TXT records to %s (%s) to prove control, now and at every renewal, and remove them after each validation.",
				plural(len(names), "name", "names"), plural(len(ids), "Pantech DNS zone", "Pantech DNS zones"), strings.Join(ids, ", ")),
			Warnings: requestNotes(in),
			Data:     map[string]any{"names": names, "zones": zones, "key_type": in.KeyType, "exportable": in.Exportable},
		}, nil
	}, func(ctx context.Context, c *Call, in requestCertificateInput) (*Result, error) {
		names, err := checkRequest(in)
		if err != nil {
			return nil, err
		}
		body := map[string]any{"names": names}
		if in.KeyType != "" {
			body["key_type"] = in.KeyType
		}
		if in.Exportable {
			body["exportable"] = true
		}
		if in.Description != "" {
			body["description"] = in.Description
		}
		var ct certificate
		if err := c.Client.Do(ctx, "POST", "/certificates", nil, body, &ct, c.writeOpts()...); err != nil {
			return nil, err
		}
		last := c.waitCertificate(ctx, &ct)
		return ok(certificateText(last), last)
	})

	addConfirmed(Spec{
		Name: "import_certificate", Title: "Import a certificate",
		Description: "Import a certificate bought elsewhere, with its chain and unencrypted private key, so Pantech services can use it. The key is sent to Pantech and never shown back. Pantech does not renew imported certificates; replace one with reimport_certificate. Owners and admins.",
		Risk:        Exposure,
		Routes:      []string{"POST /certificates/import"},
	}, func(ctx context.Context, c *Call, in importCertificateInput) (*Preview, error) {
		p, err := parseImport(in.CertificatePEM, in.ChainPEM, in.PrivateKeyPEM)
		if err != nil {
			return nil, err
		}
		notes := []string{"The private key is sealed by Pantech and never shown back by these tools. Pantech does not renew an imported certificate: it emails owners and admins 30, 7 and 1 days before it expires."}
		if in.Exportable {
			notes = append(notes, "Exportable: owners and admins (and AI apps with Manage and delete access) can export the key later.")
		}
		return &Preview{
			Action:   fmt.Sprintf("Import a certificate for %s, valid until %s (details in the data; the private key is not shown).", plural(len(p.Names), "name", "names"), p.NotAfter),
			Warnings: notes,
			Data:     map[string]any{"certificate": p, "exportable": in.Exportable},
		}, nil
	}, func(ctx context.Context, c *Call, in importCertificateInput) (*Result, error) {
		body := map[string]any{"certificate_pem": in.CertificatePEM, "private_key_pem": in.PrivateKeyPEM}
		if in.ChainPEM != "" {
			body["chain_pem"] = in.ChainPEM
		}
		if in.Exportable {
			body["exportable"] = true
		}
		if in.Description != "" {
			body["description"] = in.Description
		}
		var ct certificate
		if err := c.Client.Do(ctx, "POST", "/certificates/import", nil, body, &ct, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok("Imported. "+certificateText(&ct), ct)
	})

	add(Spec{
		Name: "update_certificate", Title: "Change a certificate's description",
		Description: "Change a certificate's description. Nothing else about a certificate can change.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"PATCH /certificates/{certificate_id}"},
	}, func(ctx context.Context, c *Call, in updateCertificateInput) (*Result, error) {
		if err := checkIDs("certificate_id", in.CertificateID); err != nil {
			return nil, err
		}
		var ct certificate
		if err := c.Client.Do(ctx, "PATCH", certificatePath(in.CertificateID), nil, map[string]any{"description": in.Description}, &ct, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Certificate %s saved.", in.CertificateID), ct)
	})

	add(Spec{
		Name: "verify_certificate", Title: "Check a certificate's CNAMEs now",
		Description: "Check now whether the CNAME records a certificate waits for are in place, instead of at the next check (every 5 minutes), and wait for the answer. DNS changes at another host can take a while to show.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"POST /certificates/{certificate_id}/verify", "GET /certificates/{certificate_id}"},
	}, func(ctx context.Context, c *Call, in certificateInput) (*Result, error) {
		if err := checkIDs("certificate_id", in.CertificateID); err != nil {
			return nil, err
		}
		var ct certificate
		if err := c.Client.Do(ctx, "POST", certificatePath(in.CertificateID)+"/verify", nil, nil, &ct, c.writeOpts()...); err != nil {
			return nil, err
		}
		last := c.waitCertificate(ctx, &ct)
		return ok(certificateText(last), last)
	})

	addConfirmed(Spec{
		Name: "reimport_certificate", Title: "Replace an imported certificate",
		Description: "Replace an imported certificate in place with a new certificate and key for exactly the same names, so whatever uses it keeps working. The key is sent to Pantech and never shown back. Owners and admins.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /certificates/{certificate_id}", "POST /certificates/{certificate_id}/reimport"},
	}, func(ctx context.Context, c *Call, in reimportCertificateInput) (*Preview, error) {
		if err := checkIDs("certificate_id", in.CertificateID); err != nil {
			return nil, err
		}
		p, err := parseImport(in.CertificatePEM, in.ChainPEM, in.PrivateKeyPEM)
		if err != nil {
			return nil, err
		}
		ct, err := c.getCertificate(ctx, in.CertificateID)
		if err != nil {
			return nil, err
		}
		if ct.Kind != "imported" {
			return nil, errorf("Certificate %s is issued by Pantech, which renews it: only imported certificates can be replaced.", ct.ID)
		}
		if !sameNames(ct.Names, p.Names) {
			return nil, errorf("The new certificate names other names than certificate %s; a replacement needs exactly the same names.", ct.ID)
		}
		was := "now"
		if ct.NotAfter != nil {
			was = "now valid until " + arg(*ct.NotAfter)
		}
		return &Preview{
			Action:   fmt.Sprintf("Replace imported certificate %s (%s) with the new one, valid until %s. The old certificate and key are discarded.", ct.ID, was, p.NotAfter),
			Warnings: []string{"Pantech services using it switch to the new certificate. This cannot be undone: keep the old files if they may be needed."},
			Data:     map[string]any{"current": ct, "new": p},
		}, nil
	}, func(ctx context.Context, c *Call, in reimportCertificateInput) (*Result, error) {
		body := map[string]any{"certificate_pem": in.CertificatePEM, "private_key_pem": in.PrivateKeyPEM}
		if in.ChainPEM != "" {
			body["chain_pem"] = in.ChainPEM
		}
		var ct certificate
		if err := c.Client.Do(ctx, "POST", certificatePath(in.CertificateID)+"/reimport", nil, body, &ct, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok("Replaced. "+certificateText(&ct), ct)
	})

	addConfirmed(Spec{
		Name: "export_certificate", Title: "Export a certificate and its key",
		Description: "Export a certificate created as exportable: the certificate, chain and private key as PEM, to install on the user's own servers. Whoever holds the key can impersonate these names. Every export is recorded and emails the organization's owners and admins a security notice. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true,
		Routes: []string{"GET /certificates/{certificate_id}", "POST /certificates/{certificate_id}/export"},
	}, func(ctx context.Context, c *Call, in certificateConfirmInput) (*Preview, error) {
		ct, err := c.getCertificate(ctx, in.CertificateID)
		if err != nil {
			return nil, err
		}
		if !ct.Exportable {
			return nil, errorf("Certificate %s was created without exportable, so its key cannot be exported: only Pantech services can use it. Request a new exportable certificate to use on your own servers.", ct.ID)
		}
		notes := []string{
			deleteAccessNote,
			"The private key will be in the result. Whoever holds it can impersonate these names: give it only to the user, and store it with owner-only file permissions.",
			"The export is recorded and every owner and admin is emailed a security notice.",
		}
		if ct.Kind == "public" {
			notes = append(notes, "Pantech renews the certificate with a new key about every 60 days: export again after each renewal (the certificate.renewed webhook says when).")
		}
		return &Preview{
			Action:   fmt.Sprintf("Export certificate %s (%s) with its private key.", ct.ID, plural(len(ct.Names), "name", "names")),
			Warnings: notes,
			Data:     map[string]any{"certificate": ct},
		}, nil
	}, func(ctx context.Context, c *Call, in certificateConfirmInput) (*Result, error) {
		var ex exportedCertificate
		if err := c.Client.Do(ctx, "POST", certificatePath(in.CertificateID)+"/export", nil, nil, &ex, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Certificate %s exported. The certificate, chain and PRIVATE KEY are in the data. Give them only to the user, to save on their server (the key readable only by its owner, e.g. chmod 600). Do not repeat the key anywhere else. Owners and admins have been emailed a security notice.", in.CertificateID), ex)
	})

	addConfirmed(Spec{
		Name: "revoke_certificate", Title: "Revoke a certificate",
		Description: "Ask the certificate authority to revoke a certificate Pantech issued, so browsers stop trusting it, and stop its renewals. Use key_compromise when the key may be known to someone else. Imported certificates are revoked with the authority that issued them. Refused while a Pantech service uses it. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /certificates/{certificate_id}", "POST /certificates/{certificate_id}/revoke"},
	}, func(ctx context.Context, c *Call, in revokeCertificateInput) (*Preview, error) {
		if in.Reason != "" && in.Reason != "unspecified" && in.Reason != "key_compromise" {
			return nil, errorf("reason must be unspecified or key_compromise.")
		}
		ct, err := c.getCertificate(ctx, in.CertificateID)
		if err != nil {
			return nil, err
		}
		if ct.Kind != "public" {
			return nil, errorf("Certificate %s is imported: revoke it with the authority that issued it, then delete it here.", ct.ID)
		}
		p := &Preview{
			Action: fmt.Sprintf("Revoke certificate %s for its %s. Browsers stop trusting it and Pantech stops renewing it. This cannot be undone.", ct.ID, plural(len(ct.Names), "name", "names")),
			Warnings: []string{
				deleteAccessNote,
				"Any server still using this certificate (including an exported copy) shows browsers an error. Request a new certificate first if the names are still in use.",
			},
			Data: map[string]any{"certificate": ct},
		}
		if len(ct.InUseBy) > 0 {
			p.Warnings = append(p.Warnings, "A Pantech service uses it, so Pantech refuses (CERTIFICATE_IN_USE) until it is detached.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in revokeCertificateInput) (*Result, error) {
		body := map[string]any{}
		if in.Reason != "" {
			body["reason"] = in.Reason
		}
		var ct certificate
		if err := c.Client.Do(ctx, "POST", certificatePath(in.CertificateID)+"/revoke", nil, body, &ct, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Revocation of certificate %s is requested; it is revoked once the certificate authority confirms, usually within minutes.", in.CertificateID), ct)
	})

	addConfirmed(Spec{
		Name: "delete_certificate", Title: "Delete a certificate",
		Description: "Delete a certificate and its key from Pantech. Deleting does not revoke it: a copy exported earlier stays valid until it expires (revoke_certificate first if that matters). Refused while a Pantech service uses it. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /certificates/{certificate_id}", "DELETE /certificates/{certificate_id}"},
	}, func(ctx context.Context, c *Call, in certificateConfirmInput) (*Preview, error) {
		ct, err := c.getCertificate(ctx, in.CertificateID)
		if err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Delete certificate %s (%s) and its key. This cannot be undone.", ct.ID, plural(len(ct.Names), "name", "names")),
			Warnings: []string{
				deleteAccessNote,
				"Deleting does not revoke it: any exported copy keeps working until it expires.",
			},
			Data: map[string]any{"certificate": ct},
		}
		if len(ct.InUseBy) > 0 {
			p.Warnings = append(p.Warnings, "A Pantech service uses it, so Pantech refuses (CERTIFICATE_IN_USE) until it is detached.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in certificateConfirmInput) (*Result, error) {
		if err := c.Client.Do(ctx, "DELETE", certificatePath(in.CertificateID), nil, nil, nil, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Certificate %s deleted.", in.CertificateID), map[string]any{"certificate_id": in.CertificateID, "status": "deleted"})
	})
}

func checkRequest(in requestCertificateInput) ([]string, error) {
	if in.KeyType != "" && in.KeyType != "ecdsa-p256" && in.KeyType != "rsa-2048" {
		return nil, errorf("key_type must be ecdsa-p256 or rsa-2048.")
	}
	return certificateNames(in.Names)
}

func requestNotes(in requestCertificateInput) []string {
	notes := []string{
		"Free. Every name is published in public Certificate Transparency logs.",
		"A CAA record at a domain must allow a publicly trusted certificate authority, or the request fails (caa_forbidden).",
	}
	if in.Exportable {
		notes = append(notes, "Exportable: owners and admins (and AI apps with Manage and delete access) can export its private key. This cannot be changed later.")
	} else {
		notes = append(notes, "Not exportable: only Pantech services can use it. This cannot be changed later.")
	}
	return notes
}

func sameNames(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	for i := range y {
		y[i] = strings.ToLower(y[i])
	}
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(slices.Compact(x), slices.Compact(y))
}

// validationZone is a requested name and the active Pantech DNS zone that
// will hold its challenge record.
type validationZone struct {
	Name   string `json:"name"`
	ID     string `json:"zone_id"`
	ZoneOf string `json:"zone"`
}

// validationZones finds, for each name, the longest active zone of the
// organization that holds it (for *.x, the zone of x), as Pantech will.
func (c *Call) validationZones(ctx context.Context, names []string) ([]validationZone, error) {
	var zones []dnsZone
	cursor := ""
	for range 10 {
		q := url.Values{"status": {"active"}, "limit": {"100"}}
		setIf(q, "cursor", cursor)
		var page pantech.Page[dnsZone]
		if err := c.Client.Do(ctx, "GET", "/dns/zones", q, nil, &page); err != nil {
			if e, isAPI := pantech.AsError(err); isAPI && e.Code == "DNS_UNAVAILABLE" {
				return nil, nil
			}
			return nil, err
		}
		zones = append(zones, page.Data...)
		if !page.HasMore() || *page.NextCursor == cursor {
			break
		}
		cursor = *page.NextCursor
	}
	var out []validationZone
	for _, n := range names {
		base := strings.TrimPrefix(n, "*.")
		best := -1
		for i, z := range zones {
			if z.Status != "active" || (base != z.Name && !strings.HasSuffix(base, "."+z.Name)) {
				continue
			}
			if best < 0 || len(z.Name) > len(zones[best].Name) {
				best = i
			}
		}
		if best >= 0 && checkIDs("zone_id", zones[best].ID) == nil {
			out = append(out, validationZone{Name: n, ID: zones[best].ID, ZoneOf: zones[best].Name})
		}
	}
	return out, nil
}

func (c *Call) getCertificate(ctx context.Context, id string) (*certificate, error) {
	if err := checkIDs("certificate_id", id); err != nil {
		return nil, err
	}
	var ct certificate
	if err := c.Client.Do(ctx, "GET", certificatePath(id), nil, nil, &ct); err != nil {
		return nil, err
	}
	return &ct, nil
}

// waitCertificate polls a new or re-checked certificate within the wait
// budget until it is issued, failed or waiting for the customer's CNAMEs.
func (c *Call) waitCertificate(ctx context.Context, ct *certificate) *certificate {
	last := ct
	if checkIDs("certificate_id", ct.ID) != nil {
		return last
	}
	c.poll(ctx, func() (string, bool) {
		next, err := c.getCertificate(ctx, ct.ID)
		if err != nil {
			return "", true
		}
		last = next
		waiting := next.PendingReason != nil && *next.PendingReason == "awaiting_cname"
		return "certificate " + ct.ID + " is " + next.Status, waiting || (next.Status != "pending_validation" && next.Status != "issuing")
	})
	return last
}
