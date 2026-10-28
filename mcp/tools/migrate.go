package tools

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Pantech Migrate plans a move from another cloud (AWS for now) to Pantech.
// The AI app only guides: Pantech's own engine does all the reading, through
// a read-only role the user creates in their own account, which only Pantech
// can assume, with short-lived tokens. No tool here takes or returns cloud
// access keys, and the role's template is left out of results (the user
// opens the one-click link or gets the template from the connection's page in
// the console; `pantech migrate connect` in the CLI connects with it too). Names,
// tags and everything a scan found are the customer's own text, so they go
// only in the fenced data. Copying servers is not available yet: a plan is
// reviewed, edited and exported as Terraform.

type cloudFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// cloudSetup is a connection's setup without its template: the template is a
// long document the user does not need to see in the conversation.
type cloudSetup struct {
	IssuerURL      string  `json:"issuer_url"`
	Audience       string  `json:"audience"`
	Subject        string  `json:"subject"`
	QuickCreateURL *string `json:"quick_create_url"`
}

type cloudConnection struct {
	ID           string        `json:"id"`
	Provider     string        `json:"provider"`
	Name         string        `json:"name"`
	Status       string        `json:"status"`
	Failure      *cloudFailure `json:"failure"`
	RoleARN      *string       `json:"role_arn"`
	AWSAccountID *string       `json:"aws_account_id"`
	VerifiedAt   *string       `json:"verified_at"`
	CreatedAt    string        `json:"created_at"`
	UpdatedAt    string        `json:"updated_at"`
	Setup        cloudSetup    `json:"setup"`
}

type cloudCostMonth struct {
	Month       string `json:"month"`
	AmountMinor int64  `json:"amount_minor"`
}

type cloudCost struct {
	Currency            string           `json:"currency"`
	AverageMonthlyMinor int64            `json:"average_monthly_minor"`
	Months              []cloudCostMonth `json:"months"`
}

type cloudScan struct {
	ID           string         `json:"id"`
	ConnectionID string         `json:"connection_id"`
	Status       string         `json:"status"`
	Failure      *cloudFailure  `json:"failure"`
	CreatedAt    string         `json:"created_at"`
	StartedAt    *string        `json:"started_at"`
	FinishedAt   *string        `json:"finished_at"`
	AWSAccountID *string        `json:"aws_account_id"`
	Regions      []string       `json:"regions"`
	Counts       map[string]int `json:"counts"`
	Cost         *cloudCost     `json:"cost"`
	Warnings     []string       `json:"warnings"`
	Truncated    []string       `json:"truncated"`
	Inventory    any            `json:"inventory,omitempty"`
}

type migrationFlag struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
}

type migrationItem struct {
	Key    string          `json:"key"`
	Kind   string          `json:"kind"`
	Source any             `json:"source"`
	Status string          `json:"status"`
	Flags  []migrationFlag `json:"flags"`
	Target any             `json:"target"`
}

type migrationEstimate struct {
	Currency            string   `json:"currency"`
	PantechPriced       bool     `json:"pantech_priced"`
	PantechMonthlyMinor *int64   `json:"pantech_monthly_minor"`
	Covers              []string `json:"covers"`
	NotCovered          []string `json:"not_covered"`
	SourceKnown         bool     `json:"source_known"`
	SourceMonthlyMinor  *int64   `json:"source_monthly_minor"`
	SourceCurrency      *string  `json:"source_currency"`
	SourceMonths        int      `json:"source_months"`
	SourceScope         string   `json:"source_scope"`
}

type migrationSummary struct {
	Planned     int            `json:"planned"`
	Excluded    int            `json:"excluded"`
	Unsupported int            `json:"unsupported"`
	ByKind      map[string]int `json:"by_kind"`
}

type migration struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Region       string             `json:"region"`
	ConnectionID string             `json:"connection_id"`
	ScanID       string             `json:"scan_id"`
	Stage        string             `json:"stage"`
	Version      int                `json:"version"`
	Summary      migrationSummary   `json:"summary"`
	Estimate     *migrationEstimate `json:"estimate"`
	Items        []migrationItem    `json:"items,omitempty"`
	CreatedAt    string             `json:"created_at"`
	UpdatedAt    string             `json:"updated_at"`
}

type terraformFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type connectCloudAccountInput struct {
	Name string `json:"name" jsonschema:"A name for the connection, 1 to 100 characters, e.g. 'Production AWS'."`
}

type cloudConnectionInput struct {
	ConnectionID string `json:"connection_id" jsonschema:"The connection's id, e.g. ccn_…, from connect_cloud_account or list_cloud_connections."`
}

type setCloudRoleInput struct {
	ConnectionID string `json:"connection_id" jsonschema:"The connection's id, e.g. ccn_…"`
	RoleARN      string `json:"role_arn" jsonschema:"The RoleArn output of the stack the user created, like arn:aws:iam::123456789012:role/name. Never ask for access keys: only this ARN."`
}

type scanCloudAccountInput struct {
	ConnectionID string `json:"connection_id" jsonschema:"A connected connection's id, e.g. ccn_…"`
	Confirmation
}

type getCloudScanInput struct {
	ScanID           string `json:"scan_id" jsonschema:"The scan's id, e.g. csn_…, from scan_cloud_account or list_cloud_scans."`
	IncludeInventory bool   `json:"include_inventory,omitempty" jsonschema:"Also return every item the scan found (servers, disks, networks, firewall groups, addresses, load balancers, databases, bucket names, users, SSH keys). Large; leave it out for the counts and spend."`
}

type planMigrationInput struct {
	ScanID string `json:"scan_id" jsonschema:"A scan that succeeded, e.g. csn_…"`
	Name   string `json:"name" jsonschema:"A name for the plan, e.g. 'Move production'."`
	Region string `json:"region,omitempty" jsonschema:"The Pantech region to move to, from list_regions. Default: the first region."`
}

type migrationInput struct {
	MigrationID string `json:"migration_id" jsonschema:"The migration plan's id, e.g. mig_…, from plan_migration or list_migrations."`
}

type renameMigrationInput struct {
	MigrationID string `json:"migration_id" jsonschema:"The migration plan's id, e.g. mig_…"`
	Name        string `json:"name" jsonschema:"The new name."`
}

type deleteMigrationInput struct {
	MigrationID string `json:"migration_id" jsonschema:"The migration plan's id, e.g. mig_…"`
	Confirmation
}

type updateMigrationItemInput struct {
	MigrationID string `json:"migration_id" jsonschema:"The migration plan's id, e.g. mig_…"`
	Key         string `json:"key" jsonschema:"The item's key, exactly as get_migration returned it."`
	Excluded    *bool  `json:"excluded,omitempty" jsonschema:"true leaves the item out of the move; false puts it back."`
	PlanSlug    string `json:"plan_slug,omitempty" jsonschema:"Another Pantech plan, for a server or database (list_plans)."`
	StorageGB   *int   `json:"storage_gb,omitempty" jsonschema:"A size in GB, for a disk or database."`
	CIDR        string `json:"cidr,omitempty" jsonschema:"An address range, for a network or subnet, e.g. 10.0.0.0/16."`
	Role        string `json:"role,omitempty" jsonschema:"member or admin, for a person."`
	Email       string `json:"email,omitempty" jsonschema:"The person's email address, for a person."`
}

const migrateGuide = "Pantech reads the other cloud itself, through a read-only role the user creates in their own account; never ask the user for cloud access keys or secrets."

// roleARNPattern is what an AWS IAM role ARN looks like, in the commercial
// partition only.
var roleARNPattern = regexp.MustCompile(`^arn:aws:iam::[0-9]{12}:role/[A-Za-z0-9+=,.@_/-]{1,512}$`)

// quickCreateLink returns the one-click link when it is a plain https link to
// the AWS console, safe to quote in our own text, and "" otherwise.
func quickCreateLink(s cloudSetup) string {
	raw := str(s.QuickCreateURL)
	if raw == "" || strings.ContainsAny(raw, " \t\r\n\"'<>`") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return ""
	}
	if h := u.Hostname(); h != "console.aws.amazon.com" && !strings.HasSuffix(h, ".console.aws.amazon.com") {
		return ""
	}
	return raw
}

// sanitize drops a quick-create link that is not a plain https link to the
// AWS console, so no other link is ever handed to the user as the place to
// create the role.
func (conn *cloudConnection) sanitize() *cloudConnection {
	if quickCreateLink(conn.Setup) == "" {
		conn.Setup.QuickCreateURL = nil
	}
	return conn
}

// consoleFallback is the step when there is no one-click link.
const consoleFallback = "download this connection's template from its page in the Pantech console (Migrate) and create a stack from it in the AWS account to move from"

// connectionText describes a connection's status and the next step.
func connectionText(conn *cloudConnection) string {
	switch conn.Status {
	case "awaiting_role":
		step := consoleFallback
		if link := quickCreateLink(conn.Setup); link != "" {
			step = "open this link while signed in to the AWS account to move from, and create the stack: " + link
		}
		return fmt.Sprintf("Cloud connection %s is waiting for its role. The user should %s. Then send the stack's RoleArn output with set_cloud_role.", conn.ID, step)
	case "verifying":
		return fmt.Sprintf("Cloud connection %s is being checked: Pantech is trying the role, usually within a minute. Call get_cloud_connection again shortly.", conn.ID)
	case "connected":
		return fmt.Sprintf("Cloud connection %s is connected. Next: scan_cloud_account.", conn.ID)
	case "failed":
		return fmt.Sprintf("Cloud connection %s failed its check (%s): the failure's message in the data says what to do, then retry_cloud_connection.", conn.ID, arg(failureCode(conn.Failure)))
	}
	return fmt.Sprintf("Cloud connection %s is %s.", conn.ID, arg(conn.Status))
}

func failureCode(f *cloudFailure) string {
	if f == nil || f.Code == "" {
		return "unknown"
	}
	return f.Code
}

// scanText describes a scan: its status, what it found and the spend it read.
func scanText(s *cloudScan) string {
	switch s.Status {
	case "queued", "running":
		return fmt.Sprintf("Scan %s is %s; most accounts take under five minutes. Call get_cloud_scan again in a minute.", s.ID, s.Status)
	case "failed":
		return fmt.Sprintf("Scan %s failed (%s): the failure's message in the data says what to do, then scan_cloud_account again.", s.ID, arg(failureCode(s.Failure)))
	case "succeeded":
	default:
		return fmt.Sprintf("Scan %s is %s.", s.ID, arg(s.Status))
	}
	var b strings.Builder
	total := 0
	for _, n := range s.Counts {
		total += n
	}
	fmt.Fprintf(&b, "Scan %s succeeded: %s found across %s.", s.ID, plural(total, "item", "items"), plural(len(s.Regions), "region", "regions"))
	if s.Cost != nil && currencyCode.MatchString(s.Cost.Currency) {
		fmt.Fprintf(&b, " The source account's whole bill averaged %s a month over %s.",
			money(s.Cost.AverageMonthlyMinor, s.Cost.Currency), plural(len(s.Cost.Months), "month", "months"))
	}
	if len(s.Warnings) > 0 {
		fmt.Fprintf(&b, " %s could not be read (warnings in the data).", plural(len(s.Warnings), "part", "parts"))
	}
	if len(s.Truncated) > 0 {
		b.WriteString(" Some kinds were cut at 2,000 items (truncated in the data).")
	}
	b.WriteString(" Next: plan_migration with this scan_id.")
	return b.String()
}

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

// words joins API enum values that are safe to quote, and drops the rest.
func words(list []string) string {
	var out []string
	for _, w := range list {
		if argPattern.MatchString(w) {
			out = append(out, w)
		}
	}
	return strings.Join(out, ", ")
}

// migrationText describes a plan: its items, what is flagged and the price
// comparison.
func migrationText(m *migration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Migration plan %s (to region %s): %d planned, %d excluded, %d unsupported.",
		m.ID, arg(m.Region), m.Summary.Planned, m.Summary.Excluded, m.Summary.Unsupported)
	if m.Items != nil {
		flagged := 0
		for _, it := range m.Items {
			if len(it.Flags) > 0 {
				flagged++
			}
		}
		if flagged > 0 {
			fmt.Fprintf(&b, " %s flagged: read each flag's message in the data to the user (a blocking flag means the item cannot move yet).", plural(flagged, "item is", "items are"))
		}
	}
	if e := m.Estimate; e != nil && currencyCode.MatchString(e.Currency) {
		if e.PantechPriced && e.PantechMonthlyMinor != nil {
			fmt.Fprintf(&b, " Pantech's monthly price for the plan: %s (covers %s", money(*e.PantechMonthlyMinor, e.Currency), words(e.Covers))
			if len(e.NotCovered) > 0 {
				fmt.Fprintf(&b, "; not included: %s", words(e.NotCovered))
			}
			b.WriteString(").")
		} else {
			b.WriteString(" Part of the plan could not be priced, so there is no Pantech total.")
		}
		if e.SourceKnown && e.SourceMonthlyMinor != nil && e.SourceCurrency != nil && currencyCode.MatchString(*e.SourceCurrency) {
			fmt.Fprintf(&b, " The source account's whole bill averaged %s a month over %s (every service, not only what is planned).",
				money(*e.SourceMonthlyMinor, *e.SourceCurrency), plural(e.SourceMonths, "month", "months"))
		}
		b.WriteString(" " + exactAmounts)
	}
	b.WriteString(" Nothing is created: review and edit the plan (update_migration_item), then export_migration_terraform. Copying servers is not available yet.")
	return b.String()
}

func init() {
	add(Spec{
		Name: "connect_cloud_account", Title: "Connect an AWS account to plan a move",
		Description: "Start a connection to the user's AWS account for Pantech Migrate. Nothing is shared yet: it returns a one-click link that opens AWS's create-stack page, which creates in the user's account a read-only role only Pantech can assume, with short-lived tokens. Give the user the link and the steps (the template is also downloadable from the connection's page in the Pantech console; a user who prefers the CLI can instead connect with `pantech migrate connect`, which saves it); then send the stack's RoleArn output with set_cloud_role. " + migrateGuide + " Owners and admins only; at most 10 connections.",
		Risk:        Write,
		Routes:      []string{"POST /cloud-connections"},
	}, func(ctx context.Context, c *Call, in connectCloudAccountInput) (*Result, error) {
		body := map[string]any{"provider": "aws", "name": in.Name}
		var conn cloudConnection
		if err := c.Client.Do(ctx, "POST", "/cloud-connections", nil, body, &conn, c.writeOpts()...); err != nil {
			return nil, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Started cloud connection %s. Give the user these steps:\n", arg(conn.ID))
		conn.sanitize()
		if link := quickCreateLink(conn.Setup); link != "" {
			fmt.Fprintf(&b, "1. Open this link while signed in to the AWS account to move from: %s\n", link)
		} else {
			b.WriteString("1. In a browser, " + consoleFallback + ".\n")
		}
		b.WriteString("2. Tick the acknowledgement that the stack creates an IAM role, and create the stack. It makes a read-only role only Pantech can assume.\n")
		b.WriteString("3. When the stack is complete, copy its RoleArn output and share it here; then call set_cloud_role with it.\n")
		b.WriteString("The template itself is not shown here; the user can download it from this connection's page in the Pantech console (Migrate). " + migrateGuide)
		return ok(b.String(), conn)
	})

	add(Spec{
		Name: "list_cloud_connections", Title: "List cloud connections",
		Description: "List the organization's connections to other cloud accounts for Pantech Migrate, newest first, with each one's status (awaiting_role, verifying, connected or failed).",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /cloud-connections"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		var page struct {
			Data []cloudConnection `json:"data"`
		}
		if err := c.Client.Do(ctx, "GET", "/cloud-connections", nil, nil, &page); err != nil {
			return nil, err
		}
		for i := range page.Data {
			page.Data[i].sanitize()
		}
		return ok(plural(len(page.Data), "cloud connection", "cloud connections")+".", page.Data)
	})

	add(Spec{
		Name: "get_cloud_connection", Title: "Get a cloud connection",
		Description: "Get a cloud connection: its status (awaiting_role until set_cloud_role, verifying while Pantech tries the role, then connected or failed with a message saying what to do), the AWS account it reaches, and its one-click setup link.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /cloud-connections/{connection_id}"},
	}, func(ctx context.Context, c *Call, in cloudConnectionInput) (*Result, error) {
		if err := checkIDs("connection_id", in.ConnectionID); err != nil {
			return nil, err
		}
		conn, err := c.getConnection(ctx, in.ConnectionID)
		if err != nil {
			return nil, err
		}
		return ok(connectionText(conn), conn)
	})

	add(Spec{
		Name: "set_cloud_role", Title: "Send the role a cloud connection uses",
		Description: "Send the RoleArn output of the stack the user created, so Pantech can check the role (usually within a minute; this waits for it). Only the ARN is sent: " + migrateGuide,
		Risk:        Write, Idempotent: true,
		Routes: []string{"PATCH /cloud-connections/{connection_id}", "GET /cloud-connections/{connection_id}"},
	}, func(ctx context.Context, c *Call, in setCloudRoleInput) (*Result, error) {
		if err := checkIDs("connection_id", in.ConnectionID); err != nil {
			return nil, err
		}
		if !roleARNPattern.MatchString(in.RoleARN) {
			return nil, errorf("role_arn must be the stack's RoleArn output, like arn:aws:iam::123456789012:role/name.")
		}
		return c.updateConnection(ctx, in.ConnectionID, map[string]any{"role_arn": in.RoleARN})
	})

	add(Spec{
		Name: "retry_cloud_connection", Title: "Check a cloud connection again",
		Description: "Check a failed cloud connection's role again, after the user fixed what its failure message said (this waits for the check, usually under a minute).",
		Risk:        Write, Idempotent: true,
		Routes: []string{"PATCH /cloud-connections/{connection_id}", "GET /cloud-connections/{connection_id}"},
	}, func(ctx context.Context, c *Call, in cloudConnectionInput) (*Result, error) {
		if err := checkIDs("connection_id", in.ConnectionID); err != nil {
			return nil, err
		}
		return c.updateConnection(ctx, in.ConnectionID, map[string]any{"retry": true})
	})

	add(Spec{
		Name: "disconnect_cloud_account", Title: "Disconnect a cloud account",
		Description: "Remove a cloud connection and its scans from Pantech; migration plans made from it stay. It only takes access away, so Manage access is enough. To revoke access on the AWS side too, the user deletes the stack in their AWS account.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"DELETE /cloud-connections/{connection_id}"},
	}, func(ctx context.Context, c *Call, in cloudConnectionInput) (*Result, error) {
		if err := checkIDs("connection_id", in.ConnectionID); err != nil {
			return nil, err
		}
		if err := c.Client.Do(ctx, "DELETE", "/cloud-connections/"+in.ConnectionID, nil, nil, nil, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Cloud connection %s is disconnected: Pantech no longer uses its role. To remove the role from the AWS account too, the user deletes the stack there.", in.ConnectionID),
			map[string]any{"connection_id": in.ConnectionID, "status": "disconnected"})
	})

	addConfirmed(Spec{
		Name: "scan_cloud_account", Title: "Scan a connected cloud account",
		Description: "Have Pantech read everything a connected account's role can see (servers, disks, networks, firewall groups, public addresses, load balancers, databases, bucket names, users, SSH keys) and the last three months of spend, in every enabled region. Reading spend costs USD 0.01 on the user's AWS bill (charged by AWS, not Pantech). Waits up to a minute; most accounts take under five, so follow it with get_cloud_scan.",
		Risk:        Cost,
		Routes:      []string{"GET /cloud-connections/{connection_id}", "POST /cloud-connections/{connection_id}/scans", "GET /cloud-scans/{scan_id}"},
	}, func(ctx context.Context, c *Call, in scanCloudAccountInput) (*Preview, error) {
		if err := checkIDs("connection_id", in.ConnectionID); err != nil {
			return nil, err
		}
		conn, err := c.getConnection(ctx, in.ConnectionID)
		if err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Scan the cloud account behind connection %s: Pantech reads its resources and the last three months of spend, read only, in every enabled region.", conn.ID),
			Price:  "USD 0.01 on the user's AWS bill, charged by AWS for reading spend (Pantech charges nothing for the scan)",
			Warnings: []string{
				"Nothing is changed in the AWS account; the role can only read.",
			},
			Data: map[string]any{"connection": conn},
		}
		if conn.Status != "connected" {
			p.Warnings = append(p.Warnings, "The connection is not connected yet: finish set_cloud_role first, or Pantech refuses.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in scanCloudAccountInput) (*Result, error) {
		var s cloudScan
		if err := c.Client.Do(ctx, "POST", "/cloud-connections/"+in.ConnectionID+"/scans", nil, nil, &s, c.writeOpts()...); err != nil {
			return nil, err
		}
		if checkIDs("scan_id", s.ID) != nil {
			return ok("The scan is queued. Call list_cloud_scans for its id, then get_cloud_scan.", s)
		}
		last := &s
		c.poll(ctx, func() (string, bool) {
			next, err := c.getScan(ctx, s.ID, false)
			if err != nil {
				return "", true
			}
			last = next
			return "scan " + s.ID + " is " + next.Status, next.Status != "queued" && next.Status != "running"
		})
		return ok(scanText(last), last)
	})

	add(Spec{
		Name: "list_cloud_scans", Title: "List a connection's scans",
		Description: "List a cloud connection's scans, newest first, without their inventories (the 10 newest finished scans are kept).",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /cloud-connections/{connection_id}/scans"},
	}, func(ctx context.Context, c *Call, in cloudConnectionInput) (*Result, error) {
		if err := checkIDs("connection_id", in.ConnectionID); err != nil {
			return nil, err
		}
		var page struct {
			Data []cloudScan `json:"data"`
		}
		if err := c.Client.Do(ctx, "GET", "/cloud-connections/"+in.ConnectionID+"/scans", nil, nil, &page); err != nil {
			return nil, err
		}
		return ok(plural(len(page.Data), "scan", "scans")+".", page.Data)
	})

	add(Spec{
		Name: "get_cloud_scan", Title: "Get a cloud scan",
		Description: "Get a scan: its status (queued, running, succeeded or failed), what it found per kind, the regions read, the source account's monthly spend, and the parts it could not read. include_inventory adds every item found; names and tags in it are the user's own text, shown as data.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /cloud-scans/{scan_id}"},
	}, func(ctx context.Context, c *Call, in getCloudScanInput) (*Result, error) {
		if err := checkIDs("scan_id", in.ScanID); err != nil {
			return nil, err
		}
		s, err := c.getScan(ctx, in.ScanID, in.IncludeInventory)
		if err != nil {
			return nil, err
		}
		return ok(scanText(s), s)
	})

	add(Spec{
		Name: "plan_migration", Title: "Plan a migration from a scan",
		Description: "Build a migration plan from a scan that succeeded: each server, disk, network, firewall group, database, key and person mapped to its Pantech equivalent and sized against today's plans, with anything that cannot move yet flagged and explained, and Pantech's monthly price next to the source bill. Nothing is created or billed: the plan is to review, edit (update_migration_item) and export (export_migration_terraform). At most 25 plans.",
		Risk:        Write,
		Routes:      []string{"POST /migrations"},
	}, func(ctx context.Context, c *Call, in planMigrationInput) (*Result, error) {
		if err := checkIDs("scan_id", in.ScanID); err != nil {
			return nil, err
		}
		body := map[string]any{"scan_id": in.ScanID, "name": in.Name}
		if in.Region != "" {
			body["region"] = in.Region
		}
		var m migration
		if err := c.Client.Do(ctx, "POST", "/migrations", nil, body, &m, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(migrationText(&m), m)
	})

	add(Spec{
		Name: "list_migrations", Title: "List migration plans",
		Description: "List the organization's migration plans, newest first, with their counts (no items or price: use get_migration).",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /migrations"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		var page struct {
			Data []migration `json:"data"`
		}
		if err := c.Client.Do(ctx, "GET", "/migrations", nil, nil, &page); err != nil {
			return nil, err
		}
		return ok(plural(len(page.Data), "migration plan", "migration plans")+".", page.Data)
	})

	add(Spec{
		Name: "get_migration", Title: "Get a migration plan",
		Description: "Get a migration plan with every item (its source, its Pantech target, and flags whose messages explain what cannot move or needs a decision) and a price comparison computed now: Pantech's monthly price for the plan next to the source account's average monthly bill. " + exactAmounts,
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /migrations/{migration_id}"},
	}, func(ctx context.Context, c *Call, in migrationInput) (*Result, error) {
		if err := checkIDs("migration_id", in.MigrationID); err != nil {
			return nil, err
		}
		var m migration
		if err := c.Client.Do(ctx, "GET", "/migrations/"+in.MigrationID, nil, nil, &m); err != nil {
			return nil, err
		}
		return ok(migrationText(&m), m)
	})

	add(Spec{
		Name: "update_migration_item", Title: "Edit an item of a migration plan",
		Description: "Change one item of a plan, named by its key from get_migration: leave it out (excluded), choose another plan (plan_slug, for servers and databases), a size (storage_gb, for disks and databases), an address range (cidr, for networks and subnets), or a person's role and email. Items flagged as unsupported stay unsupported. Only the plan changes; nothing is created.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"PATCH /migrations/{migration_id}/items"},
	}, func(ctx context.Context, c *Call, in updateMigrationItemInput) (*Result, error) {
		if err := checkIDs("migration_id", in.MigrationID); err != nil {
			return nil, err
		}
		if in.Key == "" {
			return nil, errorf("key is required: use the item's key exactly as get_migration returned it.")
		}
		if in.Role != "" && in.Role != "member" && in.Role != "admin" {
			return nil, errorf("role must be member or admin.")
		}
		if in.StorageGB != nil && *in.StorageGB < 1 {
			return nil, errorf("storage_gb must be at least 1.")
		}
		body := map[string]any{"key": in.Key}
		if in.Excluded != nil {
			body["excluded"] = *in.Excluded
		}
		if in.PlanSlug != "" {
			body["plan_slug"] = in.PlanSlug
		}
		if in.StorageGB != nil {
			body["storage_gb"] = *in.StorageGB
		}
		if in.CIDR != "" {
			body["cidr"] = in.CIDR
		}
		if in.Role != "" {
			body["role"] = in.Role
		}
		if in.Email != "" {
			body["email"] = in.Email
		}
		if len(body) == 1 {
			return nil, errorf("Nothing to change: give excluded, plan_slug, storage_gb, cidr, role or email.")
		}
		var m migration
		if err := c.Client.Do(ctx, "PATCH", "/migrations/"+in.MigrationID+"/items", nil, body, &m, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok("Item updated. "+migrationText(&m), m)
	})

	add(Spec{
		Name: "rename_migration", Title: "Rename a migration plan",
		Description: "Change a migration plan's name.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"PATCH /migrations/{migration_id}"},
	}, func(ctx context.Context, c *Call, in renameMigrationInput) (*Result, error) {
		if err := checkIDs("migration_id", in.MigrationID); err != nil {
			return nil, err
		}
		var m migration
		if err := c.Client.Do(ctx, "PATCH", "/migrations/"+in.MigrationID, nil, map[string]any{"name": in.Name}, &m, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Migration plan %s renamed.", in.MigrationID), m)
	})

	addConfirmed(Spec{
		Name: "delete_migration", Title: "Delete a migration plan",
		Description: "Delete a migration plan and the edits made to it. Nothing in Pantech or in the other cloud is touched. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /migrations/{migration_id}", "DELETE /migrations/{migration_id}"},
	}, func(ctx context.Context, c *Call, in deleteMigrationInput) (*Preview, error) {
		if err := checkIDs("migration_id", in.MigrationID); err != nil {
			return nil, err
		}
		var m migration
		if err := c.Client.Do(ctx, "GET", "/migrations/"+in.MigrationID, nil, nil, &m); err != nil {
			return nil, err
		}
		m.Items = nil
		return &Preview{
			Action: fmt.Sprintf("Permanently delete migration plan %s and every edit made to it. Nothing in Pantech or in the other cloud is touched; the cloud connection and its scans stay.", in.MigrationID),
			Warnings: []string{
				deleteAccessNote,
				"Export it first with export_migration_terraform if the user may still want it.",
			},
			Data: map[string]any{"migration": m},
		}, nil
	}, func(ctx context.Context, c *Call, in deleteMigrationInput) (*Result, error) {
		if err := c.Client.Do(ctx, "DELETE", "/migrations/"+in.MigrationID, nil, nil, nil, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Migration plan %s deleted.", in.MigrationID), map[string]any{"migration_id": in.MigrationID, "status": "deleted"})
	})

	add(Spec{
		Name: "export_migration_terraform", Title: "Export a migration plan as Terraform",
		Description: "Export a migration plan's planned items as Terraform for the Pantech provider: main.tf and variables.tf. Values Pantech cannot know (the image, an SSH key, disk offerings, database passwords) are variables without defaults. Give the user the files to review, then run terraform plan. This is how a plan is carried out for now: copying servers is not available yet.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /migrations/{migration_id}/terraform"},
	}, func(ctx context.Context, c *Call, in migrationInput) (*Result, error) {
		if err := checkIDs("migration_id", in.MigrationID); err != nil {
			return nil, err
		}
		var out struct {
			Files []terraformFile `json:"files"`
		}
		if err := c.Client.Do(ctx, "GET", "/migrations/"+in.MigrationID+"/terraform", nil, nil, &out); err != nil {
			return nil, err
		}
		var names []string
		for _, f := range out.Files {
			names = append(names, f.Name)
		}
		return ok(fmt.Sprintf("Terraform for migration plan %s: %s (%s), in the data. The user saves them in an empty folder, fills in the variables, and runs terraform init and terraform plan to review before terraform apply, which creates billed resources.",
			in.MigrationID, plural(len(out.Files), "file", "files"), words(names)), out)
	})
}

func (c *Call) getConnection(ctx context.Context, id string) (*cloudConnection, error) {
	var conn cloudConnection
	if err := c.Client.Do(ctx, "GET", "/cloud-connections/"+id, nil, nil, &conn); err != nil {
		return nil, err
	}
	return conn.sanitize(), nil
}

func (c *Call) getScan(ctx context.Context, id string, inventory bool) (*cloudScan, error) {
	var q url.Values
	if inventory {
		q = url.Values{"include": {"inventory"}}
	}
	var s cloudScan
	if err := c.Client.Do(ctx, "GET", "/cloud-scans/"+id, q, nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// updateConnection sends a role ARN or a retry, then waits, within the wait
// budget, for Pantech's check of the role to finish.
func (c *Call) updateConnection(ctx context.Context, id string, body map[string]any) (*Result, error) {
	var conn cloudConnection
	if err := c.Client.Do(ctx, "PATCH", "/cloud-connections/"+id, nil, body, &conn, c.writeOpts()...); err != nil {
		return nil, err
	}
	conn.sanitize()
	last := &conn
	if conn.Status == "verifying" {
		c.poll(ctx, func() (string, bool) {
			next, err := c.getConnection(ctx, id)
			if err != nil {
				return "", true
			}
			last = next
			return "connection " + id + " is " + next.Status, next.Status != "verifying"
		})
	}
	return ok(connectionText(last), last)
}

// poll calls check until it reports done or the wait budget runs out,
// sending progress with the message check returns.
func (c *Call) poll(ctx context.Context, check func() (msg string, done bool)) {
	deadline := time.Now().Add(c.env.waitBudget())
	p := newProgress(c.Req)
	for {
		msg, done := check()
		if done {
			return
		}
		p.send(ctx, msg)
		if time.Now().Add(c.env.pollInterval()).After(deadline) {
			return
		}
		if sleep(ctx, c.env.pollInterval()) != nil {
			return
		}
	}
}
