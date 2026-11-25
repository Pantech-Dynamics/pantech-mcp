package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The clouds Pantech Migrate reads, the Pantech Migrate agent, and inviting a
// plan's people. The agent's enrollment code is a one-time credential, shown
// once in enroll_migrate_agent's result for the user to run on their own
// machine; the provider token the agent reads stays there and never passes
// through a tool.

type cloudProvider struct {
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	Access         string `json:"access"`
	CostReadable   bool   `json:"cost_readable"`
	PeopleReadable bool   `json:"people_readable"`
	Available      bool   `json:"available"`
}

type gcpFederationInput struct {
	ProjectNumber       string   `json:"project_number" jsonschema:"The project number the setup printed (digits)."`
	PoolID              string   `json:"pool_id" jsonschema:"The workload identity pool id the setup printed."`
	ProviderID          string   `json:"provider_id" jsonschema:"The pool provider id the setup printed."`
	ServiceAccountEmail string   `json:"service_account_email" jsonschema:"The service account email the setup printed."`
	ProjectIDs          []string `json:"project_ids" jsonschema:"The project ids to scan, 1 to 20."`
}

type azureFederationInput struct {
	TenantID        string   `json:"tenant_id" jsonschema:"The tenant id the setup printed."`
	ClientID        string   `json:"client_id" jsonschema:"The app registration's application (client) id the setup printed."`
	SubscriptionIDs []string `json:"subscription_ids" jsonschema:"The subscription ids to scan, 1 to 20."`
}

type huaweiProjectInput struct {
	Region string `json:"region" jsonschema:"The region, e.g. af-south-1."`
	ID     string `json:"id" jsonschema:"The region project's id."`
}

type huaweiFederationInput struct {
	IdentityProviderID string               `json:"identity_provider_id" jsonschema:"The id of the identity provider the user created from the setup's steps."`
	Projects           []huaweiProjectInput `json:"projects" jsonschema:"The region projects to scan, 1 to 20."`
}

type setCloudFederationInput struct {
	ConnectionID string                 `json:"connection_id" jsonschema:"The connection's id, e.g. ccn_…"`
	GCP          *gcpFederationInput    `json:"gcp,omitempty" jsonschema:"For a Google Cloud connection: what its gcloud script printed."`
	Azure        *azureFederationInput  `json:"azure,omitempty" jsonschema:"For an Azure connection: what its Azure CLI script printed."`
	Huawei       *huaweiFederationInput `json:"huawei,omitempty" jsonschema:"For a Huawei Cloud connection: its identity provider and region projects."`
}

type enrollMigrateAgentInput struct {
	ConnectionID string `json:"connection_id" jsonschema:"An agent connection's id (DigitalOcean, Hetzner, Linode, Vultr, Contabo or OpenStack), e.g. ccn_…"`
	Confirmation
}

type inviteMigrationPeopleInput struct {
	MigrationID string   `json:"migration_id" jsonschema:"The migration plan's id, e.g. mig_…"`
	Keys        []string `json:"keys" jsonschema:"The keys of the plan's user items to invite, 1 to 50, exactly as get_migration returned them."`
	Confirmation
}

type agentEnrollment struct {
	Code          string `json:"code"`
	ExpiresAt     string `json:"expires_at"`
	EnrollCommand string `json:"enroll_command"`
}

type inviteResult struct {
	Key      string  `json:"key"`
	Email    *string `json:"email"`
	Role     *string `json:"role"`
	Outcome  string  `json:"outcome"`
	Reason   *string `json:"reason"`
	InviteID *string `json:"invite_id"`
}

var (
	digitsPattern  = regexp.MustCompile(`^[0-9]{1,30}$`)
	cloudIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@:-]{0,254}$`)
)

// checkCloudIDs checks values the user copied from their cloud's setup: they
// go in a request body, never a URL, but must still look like ids.
func checkCloudIDs(field string, values ...string) error {
	if len(values) == 0 || len(values) > 20 {
		return errorf("%s needs 1 to 20 values.", field)
	}
	for _, v := range values {
		if !cloudIDPattern.MatchString(v) {
			return errorf("%s holds a value that is not an id: copy it exactly as the setup printed it.", field)
		}
	}
	return nil
}

// federationBody checks the one federation given and returns the PATCH body.
func federationBody(in setCloudFederationInput) (map[string]any, error) {
	n := 0
	for _, set := range []bool{in.GCP != nil, in.Azure != nil, in.Huawei != nil} {
		if set {
			n++
		}
	}
	if n != 1 {
		return nil, errorf("Give exactly one of gcp, azure or huawei: what the connection's setup printed.")
	}
	switch {
	case in.GCP != nil:
		g := in.GCP
		if !digitsPattern.MatchString(g.ProjectNumber) {
			return nil, errorf("gcp.project_number is the project number the setup printed: digits only.")
		}
		for f, v := range map[string]string{"gcp.pool_id": g.PoolID, "gcp.provider_id": g.ProviderID, "gcp.service_account_email": g.ServiceAccountEmail} {
			if err := checkCloudIDs(f, v); err != nil {
				return nil, err
			}
		}
		if err := checkCloudIDs("gcp.project_ids", g.ProjectIDs...); err != nil {
			return nil, err
		}
		return map[string]any{"gcp": g}, nil
	case in.Azure != nil:
		az := in.Azure
		for f, v := range map[string]string{"azure.tenant_id": az.TenantID, "azure.client_id": az.ClientID} {
			if err := checkCloudIDs(f, v); err != nil {
				return nil, err
			}
		}
		if err := checkCloudIDs("azure.subscription_ids", az.SubscriptionIDs...); err != nil {
			return nil, err
		}
		return map[string]any{"azure": az}, nil
	}
	h := in.Huawei
	if err := checkCloudIDs("huawei.identity_provider_id", h.IdentityProviderID); err != nil {
		return nil, err
	}
	if len(h.Projects) == 0 || len(h.Projects) > 20 {
		return nil, errorf("huawei.projects needs 1 to 20 region projects.")
	}
	for _, p := range h.Projects {
		if err := checkCloudIDs("huawei.projects", p.Region, p.ID); err != nil {
			return nil, err
		}
	}
	return map[string]any{"huawei": h}, nil
}

// userItems returns the plan's user items among keys, and the keys that
// name no user item.
func userItems(m *migration, keys []string) (found []migrationItem, unknown int) {
	for _, k := range keys {
		i := slices.IndexFunc(m.Items, func(it migrationItem) bool { return it.Key == k })
		if i < 0 || m.Items[i].Kind != "user" {
			unknown++
			continue
		}
		found = append(found, m.Items[i])
	}
	return found, unknown
}

func init() {
	add(Spec{
		Name: "list_cloud_providers", Title: "List the clouds Pantech Migrate can read",
		Description: "List every cloud Pantech Migrate can connect, how Pantech reads it (federated: short-lived tokens Pantech signs and the user's cloud trusts; agent: the Pantech Migrate agent the user runs with a read-only token that stays on their machine), whether a scan reads its spend and its people, and whether this deployment can connect it now.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /cloud-providers"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		var page struct {
			Data []cloudProvider `json:"data"`
		}
		if err := c.Client.Do(ctx, "GET", "/cloud-providers", nil, nil, &page); err != nil {
			return nil, err
		}
		var available []string
		for _, p := range page.Data {
			if p.Available {
				available = append(available, p.Slug)
			}
		}
		return ok(fmt.Sprintf("%s; available now: %s. Connect one with connect_cloud_account.", plural(len(page.Data), "cloud", "clouds"), words(available)), page.Data)
	})

	add(Spec{
		Name: "set_cloud_federation", Title: "Send what a cloud's setup printed",
		Description: "For a Google Cloud, Azure or Huawei Cloud connection: send what the connection's setup printed (exactly one of gcp, azure or huawei), so Pantech can check its read-only access (usually within a minute; this waits for it). These are ids, not secrets: " + migrateGuide,
		Risk:        Write, Idempotent: true,
		Routes: []string{"PATCH /cloud-connections/{connection_id}", "GET /cloud-connections/{connection_id}"},
	}, func(ctx context.Context, c *Call, in setCloudFederationInput) (*Result, error) {
		if err := checkIDs("connection_id", in.ConnectionID); err != nil {
			return nil, err
		}
		body, err := federationBody(in)
		if err != nil {
			return nil, err
		}
		return c.updateConnection(ctx, in.ConnectionID, body)
	})

	addConfirmed(Spec{
		Name: "enroll_migrate_agent", Title: "Enroll a Pantech Migrate agent",
		Description: "For a connection read by the Pantech Migrate agent (DigitalOcean, Hetzner, Linode, Vultr, Contabo, OpenStack): issue a one-time enrollment code and the command that uses it. The code is shown once, in this tool's result, works once and for an hour; give it only to the user, who runs the command on the machine where the agent will run, with their read-only provider token in its environment (the token never leaves that machine; never ask for it). Issuing a code replaces any unused one, and redeeming it replaces the connection's earlier agent. Owners and admins only.",
		Risk:        Exposure,
		Routes:      []string{"GET /cloud-connections/{connection_id}", "POST /cloud-connections/{connection_id}/agent-enrollments"},
	}, func(ctx context.Context, c *Call, in enrollMigrateAgentInput) (*Preview, error) {
		if err := checkIDs("connection_id", in.ConnectionID); err != nil {
			return nil, err
		}
		conn, err := c.getConnection(ctx, in.ConnectionID)
		if err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Issue a one-time enrollment code for the Pantech Migrate agent of connection %s. Whoever runs it within the hour becomes this connection's agent and can upload what it reads.", conn.ID),
			Warnings: []string{
				"The code is shown once, in the result: give it only to the user, and run it only on a machine they control.",
				"An unused earlier code stops working, and an enrolled agent is replaced once the new code is used.",
			},
			Data: map[string]any{"connection": conn},
		}
		if !conn.isAgent() {
			p.Warnings = append(p.Warnings, "This connection is not read by an agent: Pantech refuses (CLOUD_CONNECTION_NOT_AGENT).")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in enrollMigrateAgentInput) (*Result, error) {
		var e agentEnrollment
		if err := c.Client.Do(ctx, "POST", "/cloud-connections/"+in.ConnectionID+"/agent-enrollments", nil, nil, &e, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Enrollment code issued for connection %s; it is in the data with the command that uses it, valid once until expires_at. Give the user the command to run where the agent will run, with their read-only provider token in its environment (setup.agent.token_variables on the connection names it), then the connection's setup.agent.run_command (or container_command) to keep the agent running. Do not repeat the code anywhere else. Then scan_cloud_account.", in.ConnectionID), e)
	})

	add(Spec{
		Name: "revoke_migrate_agent", Title: "Revoke a connection's Pantech Migrate agent",
		Description: "End an agent connection's Pantech Migrate agent at once: its credential stops working on its next request, and the connection waits for a new agent (enroll_migrate_agent). It only takes access away, so Manage access is enough. Revoking again changes nothing.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"DELETE /cloud-connections/{connection_id}/agent"},
	}, func(ctx context.Context, c *Call, in cloudConnectionInput) (*Result, error) {
		if err := checkIDs("connection_id", in.ConnectionID); err != nil {
			return nil, err
		}
		var conn cloudConnection
		if err := c.Client.Do(ctx, "DELETE", "/cloud-connections/"+in.ConnectionID+"/agent", nil, nil, &conn, c.writeOpts()...); err != nil {
			return nil, err
		}
		conn.sanitize()
		return ok(fmt.Sprintf("The agent of connection %s is revoked. The user can also stop it and delete its read-only token in the other cloud.", in.ConnectionID), conn)
	})

	addConfirmed(Spec{
		Name: "invite_migration_people", Title: "Invite a plan's people to Pantech",
		Description: "Send Pantech invites to the people a migration plan lists (its user items), by key, each with the role the plan gives it, through the organization's ordinary invite flow (the same email and rules as inviting from the console). An admin can invite members only: a person planned as admin comes back role_not_allowed. Safe to repeat: someone already invited from the plan, holding an invite, or already a member is reported and never invited twice. Owners and admins only.",
		Risk:        Exposure,
		Routes:      []string{"GET /migrations/{migration_id}", "POST /migrations/{migration_id}/invites"},
	}, func(ctx context.Context, c *Call, in inviteMigrationPeopleInput) (*Preview, error) {
		if err := checkIDs("migration_id", in.MigrationID); err != nil {
			return nil, err
		}
		if len(in.Keys) == 0 || len(in.Keys) > 50 {
			return nil, errorf("keys needs 1 to 50 user item keys from get_migration.")
		}
		var m migration
		if err := c.Client.Do(ctx, "GET", "/migrations/"+in.MigrationID, nil, nil, &m); err != nil {
			return nil, err
		}
		people, unknown := userItems(&m, in.Keys)
		p := &Preview{
			Action: fmt.Sprintf("Invite %s from migration plan %s to the organization, each with the role the plan gives them; each receives an email to join.", plural(len(people), "person", "people"), in.MigrationID),
			Warnings: []string{
				"Each person who accepts gets access to this organization with their role. Check the emails and roles in the data with the user.",
			},
			Data: map[string]any{"people": people},
		}
		if unknown > 0 {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s name no user item of the plan: Pantech reports them as skipped.", plural(unknown, "key", "keys")))
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in inviteMigrationPeopleInput) (*Result, error) {
		var out struct {
			Results   []inviteResult `json:"results"`
			Migration migration      `json:"migration"`
		}
		if err := c.Client.Do(ctx, "POST", "/migrations/"+in.MigrationID+"/invites", nil, map[string]any{"keys": in.Keys}, &out, c.writeOpts()...); err != nil {
			return nil, err
		}
		counts := map[string]int{}
		for _, r := range out.Results {
			counts[r.Outcome]++
		}
		var parts []string
		for _, o := range []string{"invited", "already_invited", "already_member", "role_not_allowed", "skipped", "failed"} {
			if counts[o] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", counts[o], o))
			}
		}
		text := "Invites for migration plan " + in.MigrationID + ": " + strings.Join(parts, ", ") + "."
		if counts["role_not_allowed"] > 0 {
			text += " role_not_allowed: an admin can invite members only; change those people's role to member (update_migration_item) or ask an owner."
		}
		if counts["failed"] > 0 {
			text += " Those that failed can be tried again."
		}
		return ok(text, out.Results)
	})
}

// rawResult decodes an answer whole for the data, and its status fields for
// the summary.
func (c *Call) rawResult(ctx context.Context, method, path string, body any, write bool) (json.RawMessage, *moveState, error) {
	var raw json.RawMessage
	var opts = c.writeOpts()
	if !write {
		opts = nil
	}
	if err := c.Client.Do(ctx, method, path, nil, body, &raw, opts...); err != nil {
		return nil, nil, err
	}
	var st moveState
	_ = json.Unmarshal(raw, &st)
	return raw, &st, nil
}
