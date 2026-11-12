package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// wantRisk pins every tool's risk, so a tool cannot quietly lose its
// confirmation step. A new tool must be added here.
var wantRisk = map[string]Risk{
	"whoami": Read, "list_regions": Read, "list_plans": Read, "list_images": Read, "estimate_price": Read,
	"get_usage": Read, "get_credit_balance": Read,
	"list_instances": Read, "get_instance": Read, "get_remote_desktop": Read, "create_instance": Cost, "start_instance": Write, "stop_instance": Write,
	"reboot_instance": Write, "resize_instance": Cost, "rename_instance": Write, "delete_instance": Destructive,
	"list_ssh_keys": Read, "create_ssh_key": Write, "change_instance_ssh_key": Destructive,
	"list_networks": Read, "create_network": Cost, "list_subnets": Read, "create_subnet": Write,
	"list_firewall_rules": Read, "add_firewall_rule": Exposure, "delete_firewall_rule": Destructive,
	"list_public_ips": Read, "create_public_ip": Cost, "attach_public_ip": Exposure, "detach_public_ip": Write,
	"create_port_forwarding_rule": Exposure, "list_security_groups": Read, "update_security_group_rules": Destructive,
	"list_load_balancers": Read, "create_load_balancer": Cost,
	"list_volumes": Read, "create_volume": Cost, "attach_volume": Write, "detach_volume": Write, "resize_volume": Cost, "delete_volume": Destructive,
	"list_snapshots": Read, "create_snapshot": Cost,
	"list_databases": Read, "get_database": Read, "create_database": Cost, "update_database_access_rules": Destructive,
	"list_kubernetes_clusters": Read, "get_kubernetes_cluster": Read, "create_kubernetes_cluster": Cost, "scale_node_pool": Cost,
	"list_webhooks": Read, "get_operation": Read, "wait_for_operation": Read,

	// v0.2.0
	"get_usage_by_resource": Read, "list_receipts": Read,
	"rebuild_instance": Destructive, "restore_instance_snapshot": Destructive, "get_instance_metrics": Read, "get_instance_usage": Read,
	"list_instance_volumes": Read, "list_instance_snapshots": Read, "create_instance_snapshot": Cost, "get_instance_order": Read,
	"open_ssh_access": Exposure, "get_ssh_access": Read, "revoke_ssh_access": Write,
	"delete_network": Destructive, "delete_subnet": Destructive, "delete_public_ip": Destructive, "list_port_forwarding_rules": Read,
	"delete_port_forwarding_rule": Destructive, "update_load_balancer": Destructive, "delete_load_balancer": Destructive,
	"create_security_group": Write, "delete_security_group": Destructive,
	"delete_snapshot": Destructive, "restore_snapshot": Cost, "get_snapshot_schedule": Read, "set_snapshot_schedule": Destructive, "get_volume_usage": Read,
	"start_database": Write, "stop_database": Write, "resize_database_storage": Cost, "delete_database": Destructive,
	"list_database_snapshots": Read, "create_database_snapshot": Cost, "get_database_order": Read,
	"delete_kubernetes_cluster": Destructive, "start_kubernetes_cluster": Write, "stop_kubernetes_cluster": Write,
	"list_node_pools": Read, "create_node_pool": Cost, "delete_node_pool": Destructive, "list_kubernetes_addons": Read,
	"toggle_kubernetes_addon": Destructive, "list_cluster_nodes": Read, "list_cluster_workloads": Read,
	"delete_ssh_key": Destructive, "list_webhook_deliveries": Read,

	// Kubernetes node plan resize.
	"list_kubernetes_versions": Read, "resize_kubernetes_node_plan": Cost,

	// Tools sessions: every step runs as the session's unprivileged user and,
	// in an ask session, waits for a person's approval in the console outside
	// a safe list, so the steps are plain writes here.
	"get_instance_agent": Read, "enable_instance_agent": Destructive, "start_tools_session": Write, "list_agent_sessions": Read, "get_agent_session": Read,
	"run_in_instance": Write, "read_instance_file": Write, "list_instance_dir": Write, "write_instance_file": Write,
	"get_agent_tool_call": Read, "list_agent_session_events": Read, "end_agent_session": Write, "get_agent_session_report": Read,

	// v0.3.0, Pantech Migrate: Pantech only reads the other cloud, and a plan creates
	// nothing, so most steps are plain writes. A scan costs USD 0.01 on the
	// user's AWS bill, so it is confirmed.
	"connect_cloud_account": Write, "list_cloud_connections": Read, "get_cloud_connection": Read,
	"set_cloud_role": Write, "retry_cloud_connection": Write, "disconnect_cloud_account": Write,
	"scan_cloud_account": Cost, "list_cloud_scans": Read, "get_cloud_scan": Read,
	"plan_migration": Write, "list_migrations": Read, "get_migration": Read, "update_migration_item": Write,
	"rename_migration": Write, "delete_migration": Destructive, "export_migration_terraform": Read,

	// Pantech DNS and domains. Records are free; a zone beyond the 5 free
	// ones is billed monthly, so creating one is confirmed then; changing or deleting a live record, replacing
	// a zone from a file and repointing a domain overwrite what answers for
	// it. Every paid order is confirmed with its exact total, and so is
	// turning a domain's protection off and getting its auth code.
	"list_dns_zones": Read, "get_dns_zone": Read, "create_dns_zone": Cost, "delete_dns_zone": Destructive, "verify_dns_zone": Write,
	"list_dns_records": Read, "create_dns_record": Write, "update_dns_record": Destructive, "delete_dns_record": Destructive,
	"import_dns_zone": Destructive, "export_dns_zone": Read,
	"list_domain_tlds": Read, "search_domains": Read, "check_domain_transfer": Read, "list_domains": Read, "get_domain": Read,
	"register_domain": Cost, "transfer_domain": Cost, "renew_domain": Cost, "update_domain_settings": Destructive,
	"set_domain_name_servers": Destructive, "get_domain_contact": Read, "update_domain_contact": Destructive,
	"get_domain_auth_code": Destructive, "list_domain_orders": Read, "get_domain_order": Read,
	"retry_domain_order_payment": Cost,

	// Pantech Container Registry. The namespace is global and fixed once a
	// repository exists; a public repository and a new token open access to
	// images; retention, deletes and turning immutable tags off lose images.
	"get_registry_namespace": Read, "set_registry_namespace": Destructive,
	"list_registry_repositories": Read, "create_registry_repository": Exposure, "get_registry_repository": Read,
	"update_registry_repository": Destructive, "delete_registry_repository": Destructive,
	"list_registry_tags": Read, "get_registry_tag": Read, "delete_registry_tag": Destructive,
	"list_registry_tokens": Read, "create_registry_token": Exposure, "revoke_registry_token": Destructive,

	// Teams and access policies. Every write is two-step: a grant (a new
	// team, member, policy or attachment) opens access, and every change or
	// removal can widen access too, by lifting a deny.
	"list_iam_actions": Read, "get_iam_settings": Read, "update_iam_settings": Destructive,
	"list_iam_teams": Read, "get_iam_team": Read, "list_iam_team_members": Read,
	"create_iam_team": Exposure, "update_iam_team": Destructive, "delete_iam_team": Destructive,
	"add_iam_team_member": Exposure, "remove_iam_team_member": Destructive,
	"list_iam_policies": Read, "get_iam_policy": Read, "create_iam_policy": Exposure, "update_iam_policy": Destructive,
	"delete_iam_policy": Destructive, "list_iam_policy_attachments": Read, "attach_iam_policy": Exposure, "detach_iam_policy": Destructive,
	"list_iam_members": Read, "get_iam_effective_access": Read, "simulate_iam_access": Read,

	// Certificate Manager. A request is free but may write validation records
	// into the organization's zones; an import hands Pantech a key; reimport
	// replaces one; export hands a key out; revoke and delete cannot be undone.
	"list_certificates": Read, "get_certificate": Read, "request_certificate": Exposure, "import_certificate": Exposure,
	"update_certificate": Write, "verify_certificate": Write, "reimport_certificate": Destructive,
	"export_certificate": Destructive, "revoke_certificate": Destructive, "delete_certificate": Destructive,
}

// readPOSTs are the POST routes a read-only tool may call: they compute an
// answer and change nothing.
var readPOSTs = map[string]bool{"POST /pricing/estimate": true, "POST /iam/simulate": true}

// noConfirmation are the only write routes a one-step tool may call although
// they would otherwise need confirmation, and why. Closing temporary SSH access
// and disconnecting a cloud account only take access away, and the API treats
// them as plain writes; the Migrate PATCH routes edit a connection or a plan,
// which creates, bills, opens and deletes nothing.
var noConfirmation = map[string]string{
	"DELETE /instances/{instance_id}/ssh-access/{grant_id}": "revoke_ssh_access only closes a port",
	"DELETE /cloud-connections/{connection_id}":             "disconnect_cloud_account only takes Pantech's access away",
	"PATCH /cloud-connections/{connection_id}":              "set_cloud_role and retry_cloud_connection only queue a check of the role",
	"PATCH /migrations/{migration_id}":                      "rename_migration only renames a plan",
	"PATCH /migrations/{migration_id}/items":                "update_migration_item only edits a plan; nothing is created",
	"PATCH /certificates/{certificate_id}":                  "update_certificate only changes a description",
}

var toolName = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

func TestRegistry(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range All() {
		if seen[tool.Name] {
			t.Errorf("%s registered twice", tool.Name)
		}
		seen[tool.Name] = true
		if !toolName.MatchString(tool.Name) {
			t.Errorf("%s: names are snake_case", tool.Name)
		}
		want, ok := wantRisk[tool.Name]
		if !ok {
			t.Errorf("%s: not in wantRisk; decide its risk there", tool.Name)
		} else if tool.Risk != want {
			t.Errorf("%s: risk %s, want %s", tool.Name, tool.Risk, want)
		}
		if tool.Title == "" || len(tool.Description) < 20 || len(tool.Routes) == 0 {
			t.Errorf("%s: needs a title, a description and its routes", tool.Name)
		}
		a := annotations(tool.Spec)
		if a.ReadOnlyHint != (tool.Risk == Read) {
			t.Errorf("%s: readOnlyHint %v for risk %s", tool.Name, a.ReadOnlyHint, tool.Risk)
		}
		if a.OpenWorldHint == nil {
			t.Errorf("%s: openWorldHint unset", tool.Name)
		}
		if tool.Risk != Read && a.DestructiveHint == nil {
			t.Errorf("%s: destructiveHint unset", tool.Name)
		}
		if tool.Risk == Destructive && !tool.Destructive {
			t.Errorf("%s: destructive risk without destructiveHint", tool.Name)
		}
		if tool.Destructive && !tool.Risk.NeedsConfirmation() {
			t.Errorf("%s: destructiveHint but no confirmation", tool.Name)
		}
		for _, r := range tool.Routes {
			method, _, _ := strings.Cut(r, " ")
			if tool.Risk == Read && method != "GET" && !readPOSTs[r] {
				t.Errorf("%s: read-only tool calls %s", tool.Name, r)
			}
			// Every DELETE, and every route the spec marks x-destructive or that
			// replaces a whole rule list, is only called by a confirmed tool.
			if (method == "DELETE" || method == "PUT" || method == "PATCH") && !tool.Risk.NeedsConfirmation() && noConfirmation[r] == "" {
				t.Errorf("%s: calls %s without confirmation", tool.Name, r)
			}
			if !strings.HasPrefix(strings.SplitN(r, " ", 2)[1], "/") {
				t.Errorf("%s: route %q", tool.Name, r)
			}
		}
		desc := describe(tool.Spec)
		if tool.Risk.NeedsConfirmation() != strings.Contains(desc, "confirmation_token") {
			t.Errorf("%s: the description must explain the two steps exactly when it needs confirmation", tool.Name)
		}
	}
	for name := range wantRisk {
		if !seen[name] {
			t.Errorf("%s is in wantRisk but not registered", name)
		}
	}
}

// TestDestructiveRoutesNeedConfirmation reads the routes the public API marks
// x-destructive and checks that every tool calling one previews first and
// carries destructiveHint, so a destructive change is never one step.
func TestDestructiveRoutesNeedConfirmation(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "x-destructive-routes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	destructive := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			destructive[line] = true
		}
	}
	if len(destructive) < 15 {
		t.Fatalf("only %d x-destructive routes read", len(destructive))
	}
	covered := 0
	for _, tool := range All() {
		for _, r := range tool.Routes {
			if !destructive[r] {
				continue
			}
			covered++
			if _, exempt := noConfirmation[r]; exempt {
				continue
			}
			if !tool.Risk.NeedsConfirmation() || !tool.Destructive || tool.Risk != Destructive {
				t.Errorf("%s calls x-destructive %s: it must be Destructive, with destructiveHint and confirmation", tool.Name, r)
			}
			if !strings.Contains(tool.Description, "Manage and delete") {
				t.Errorf("%s calls x-destructive %s: its description must say it needs Manage and delete access", tool.Name, r)
			}
		}
	}
	if covered < 15 {
		t.Errorf("only %d tool routes are x-destructive; is the list current?", covered)
	}
	// Every tool that costs money or overwrites is two-step, whatever its routes.
	for _, tool := range All() {
		if (tool.Risk >= Cost || tool.Destructive) && !strings.Contains(describe(tool.Spec), "confirmation_token") {
			t.Errorf("%s: costly or destructive without the two-step note", tool.Name)
		}
	}
}

func TestNoExcludedRoutes(t *testing.T) {
	for _, tool := range All() {
		for _, r := range tool.Routes {
			if ExcludedRoute(r) {
				t.Errorf("%s calls excluded route %s", tool.Name, r)
			}
		}
	}
	for _, r := range []string{"POST /kubernetes-clusters/{cluster_id}/kubeconfig", "POST /instances/{id}/console", "POST /webhooks",
		"POST /webhooks/{id}/rotate-secret", "PUT /databases/{id}/password", "POST /databases/{id}/reset-password",
		"POST /instances/{id}/volume-key-credential", "DELETE /me", "GET /kubernetes-clusters/{cluster_id}/live-manifests",
		"POST /kubernetes-clusters/{cluster_id}/apply-manifest", "POST /billing/credit/purchases"} {
		if !ExcludedRoute(r) {
			t.Errorf("%s should be excluded", r)
		}
	}
	// A grant holds no secret, so temporary SSH access is allowed.
	for _, r := range []string{"POST /instances/{instance_id}/ssh-access", "GET /instances/{instance_id}/ssh-access/{grant_id}", "GET /me",
		"GET /webhooks/{webhook_id}/deliveries", "GET /billing/receipts"} {
		if ExcludedRoute(r) {
			t.Errorf("%s should not be excluded", r)
		}
	}
}

// TestNoExcludedCalls scans the tool sources for the SDK methods and paths
// that return secrets, so a tool cannot reach them through Do either.
func TestNoExcludedCalls(t *testing.T) {
	banned := []string{
		"GetKubeconfig", "IssueVolumeKeyCredential", "OpenConsole", "GetLiveManifests", "ApplyManifest",
		"ChangePassword", "ResetPassword", "Webhooks.Create", "RotateSecret", "SSHKeys.Create", "Databases.Create(",
		"CreateCreditPurchase", "Me.Revoke", "GetReceiptPDF", "GetDelivery(", "Webhooks.Test",
		`"/kubeconfig`, `/volume-key-credential"`, `/console"`, `/live-manifests"`, `/apply-manifest"`, `/password"`, `/reset-password"`, `/rotate-secret"`,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "excluded.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range banned {
			if strings.Contains(string(src), b) {
				t.Errorf("%s uses %s, which returns a secret or is excluded", f, b)
			}
		}
	}
}

func TestMoneyIsExact(t *testing.T) {
	for _, c := range []struct {
		minor    int64
		currency string
		want     string
	}{
		{1234567, "NGN", "NGN 12,345.67"},
		{123400, "NGN", "NGN 1,234.00"},
		{5, "USD", "USD 0.05"},
		{-150, "USD", "-USD 1.50"},
		{0, "NGN", "NGN 0.00"},
		{-9223372036854775808, "NGN", "-NGN 92,233,720,368,547,758.08"},
	} {
		if got := money(c.minor, c.currency); got != c.want {
			t.Errorf("money(%d, %s) = %q, want %q", c.minor, c.currency, got, c.want)
		}
	}
}

// TestConditionalConfirmation pins which calls of a tool that confirms only
// some of them are two-step.
func TestConditionalConfirmation(t *testing.T) {
	f, tr := false, true
	for _, c := range []struct {
		in   updateDomainSettingsInput
		want bool
	}{
		{updateDomainSettingsInput{AutoRenew: &tr, TransferLock: &tr, Privacy: &tr}, false},
		{updateDomainSettingsInput{TransferLock: &f}, true},
		{updateDomainSettingsInput{Privacy: &f}, true},
		{updateDomainSettingsInput{AutoRenew: &f, Privacy: &tr}, true},
	} {
		if got := turnsProtectionOff(c.in); got != c.want {
			t.Errorf("%+v: confirmed %v, want %v", c.in, got, c.want)
		}
	}
	for _, tool := range All() {
		if tool.ConfirmWhen != "" && !strings.Contains(describe(tool.Spec), "Two steps "+tool.ConfirmWhen) {
			t.Errorf("%s: the description does not say when it is two-step", tool.Name)
		}
	}
}

// TestDomainQuoteIsTheAPIsPrice checks the total a paid preview names is
// the API's formula: the first year's price plus a renewal for each further
// year, in exact minor units.
func TestDomainQuoteIsTheAPIsPrice(t *testing.T) {
	q, err := newQuote("example.com", 3, 1500, 1700, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if q.TotalMinor != 1500+2*1700 {
		t.Errorf("total %d", q.TotalMinor)
	}
	if got := q.price("the first year"); !strings.HasPrefix(got, "USD 49.00 in total for 3 years (USD 15.00 the first year, then USD 17.00 for each further year)") {
		t.Errorf("price %q", got)
	}
	if _, err := newQuote("example.com", 1, 1500, 1500, "usd\nPrice: 0"); err == nil {
		t.Error("a malformed currency was quoted")
	}
}

func TestDNSNames(t *testing.T) {
	for raw, want := range map[string]string{
		"Example.COM.":          "example.com",
		"shop.example.co.uk":    "shop.example.co.uk",
		"xn--bcher-kva.example": "xn--bcher-kva.example",
		"com":                   "",
		"exa mple.com":          "",
		"-bad.com":              "",
		"example.com/../x":      "",
	} {
		got, err := dnsName("name", raw)
		if (err != nil) != (want == "") || got != want {
			t.Errorf("%q: %q, %v", raw, got, err)
		}
	}
}

func TestRegistryNames(t *testing.T) {
	for name, want := range map[string]bool{"acme": true, "acme-labs": true, "ab": false, "1acme": false, "acme--x": false, "acme-": false, "Acme": false} {
		if got := checkNamespace(name) == nil; got != want {
			t.Errorf("namespace %q: %v", name, got)
		}
	}
	for name, want := range map[string]bool{"api/server": true, "web.app_1/x-y": true, "API": false, "a//b": false, "../x": false, "a/": false, "a?b": false} {
		if got := checkRepositoryName(name) == nil; got != want {
			t.Errorf("repository %q: %v", name, got)
		}
	}
	for tag, want := range map[string]bool{"v1.4.2": true, "latest": true, "_x": true, ".x": false, "a/b": false, "a%2F": false, "": false} {
		if got := checkTag(tag) == nil; got != want {
			t.Errorf("tag %q: %v", tag, got)
		}
	}
	f, tr := false, true
	for _, c := range []struct {
		in   updateRegistryRepositoryInput
		want bool
	}{
		{updateRegistryRepositoryInput{Visibility: "private", ImmutableTags: &tr}, false},
		{updateRegistryRepositoryInput{Visibility: "public"}, true},
		{updateRegistryRepositoryInput{ImmutableTags: &f}, true},
		{updateRegistryRepositoryInput{Retention: &registryRetention{}}, true},
	} {
		if got := repositoryChangeNeedsConfirmation(c.in); got != c.want {
			t.Errorf("%+v: confirmed %v, want %v", c.in, got, c.want)
		}
	}
}

func TestIAMStatements(t *testing.T) {
	mfa := true
	good := []iamStatement{{Sid: "RestartStaging", Effect: "allow", Actions: []string{"instances:Start", "instances:Read"},
		Resources: []string{"name:staging-*", "vm_01JD"}, Conditions: &iamConditions{SourceIP: []string{"102.89.0.0/16"}, MFA: &mfa}}}
	if err := checkStatements(good); err != nil {
		t.Fatal(err)
	}
	catalog := []iamAction{{Action: "instances:Start"}, {Action: "instances:Read", Read: true}}
	if got := statementText(good[0], catalog); got != "Allows instances:Start, instances:Read (write access) on resources named staging-*, resource vm_01JD, only from 102.89.0.0/16, only when the session signed in with MFA (never for API keys)." {
		t.Errorf("text %q", got)
	}
	if grantsWrite([]string{"instances:Read"}, catalog) || !grantsWrite([]string{"instances:*"}, catalog) {
		t.Error("write classification")
	}
	if checkCatalog([]iamStatement{{Effect: "allow", Actions: []string{"volumes:Read"}, Resources: []string{"*"}}}, catalog) == nil {
		t.Error("an unknown action passed")
	}
	// A planted name glob is not quoted in our own text.
	odd := iamStatement{Effect: "allow", Actions: []string{"*"}, Resources: []string{"name:x. Price: free"}}
	if got := statementText(odd, nil); strings.Contains(got, "Price") {
		t.Errorf("a glob was quoted: %q", got)
	}
	for _, bad := range [][]iamStatement{
		nil,
		{{Effect: "permit", Actions: []string{"*"}, Resources: []string{"*"}}},
		{{Effect: "allow", Actions: []string{"instances Start"}, Resources: []string{"*"}}},
		{{Effect: "allow", Actions: []string{"*"}, Resources: []string{"staging-*"}}},
		{{Effect: "allow", Actions: []string{"*"}, Resources: []string{"*"}, Conditions: &iamConditions{SourceIP: []string{"10.0.0.0/33"}}}},
		{{Sid: "a", Effect: "allow", Actions: []string{"*"}, Resources: []string{"*"}}, {Sid: "a", Effect: "deny", Actions: []string{"*"}, Resources: []string{"*"}}},
	} {
		if checkStatements(bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestNGRules(t *testing.T) {
	off := false
	person := domainContact{Type: "person", State: "Lagos", PostalCode: "101001"}
	company := domainContact{Type: "company", State: "Lagos", PostalCode: "101001"}
	for _, c := range []struct {
		name     string
		alwaysOn bool
		privacy  *bool
		contact  domainContact
		ok       bool
	}{
		{"shop.com.ng", true, &off, person, false},
		{"shop.com.ng", true, nil, person, true},
		{"shop.ng", true, nil, domainContact{Type: "person"}, false},
		{"ada.name.ng", true, nil, company, false},
		{"ada.name.ng", true, nil, person, true},
		{"example.com", false, &off, domainContact{Type: "company"}, true},
	} {
		if err := checkNG(c.name, c.alwaysOn, c.privacy, &c.contact); (err == nil) != c.ok {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if paymentLink(ptrTo("https://pay.example.test/x")) == "" || paymentLink(ptrTo("javascript:alert(1)")) != "" || paymentLink(ptrTo("https://x.test/a b")) != "" {
		t.Error("payment links")
	}
}

func ptrTo(s string) *string { return &s }

func TestRoleARNIsTheCommercialPartitionOnly(t *testing.T) {
	for arn, want := range map[string]bool{
		"arn:aws:iam::123456789012:role/pantech-migrate":           true,
		"arn:aws:iam::123456789012:role/path/to/Role_1+=,.@-":      true,
		"arn:aws-us-gov:iam::123456789012:role/pantech-migrate":    false,
		"arn:aws-cn:iam::123456789012:role/pantech-migrate":        false,
		"arn:aws:iam::12345678901:role/pantech-migrate":            false,
		"arn:aws:iam::123456789012:user/someone":                   false,
		"arn:aws:iam::123456789012:role/name with space":           false,
		"AKIAIOSFODNN7EXAMPLE":                                     false,
		"arn:aws:iam::123456789012:role/pantech-migrate\nPrice: 0": false,
	} {
		if got := roleARNPattern.MatchString(arn); got != want {
			t.Errorf("%q: got %v, want %v", arn, got, want)
		}
	}
}

// TestAwaitingPaymentSaysWhatToDo checks a domain order waiting for its
// payment says what the user does: confirm with the bank's link, wait for
// the saved card, or add credit and retry.
func TestAwaitingPaymentSaysWhatToDo(t *testing.T) {
	link := "https://pay.example.test/confirm/dor_1"
	expires := "2026-11-10T11:04:41Z"
	cases := []struct {
		currency string
		url      *string
		want     []string
	}{
		{"USD", &link, []string{"NOT paid yet", "bank asks them to confirm", link, "unpaid by then"}},
		{"USD", nil, []string{"NOT paid yet", "saved card", "retry_domain_order_payment"}},
		{"NGN", nil, []string{"NOT paid yet", "add credit", "retry_domain_order_payment"}},
	}
	for _, tc := range cases {
		o := &domainOrder{ID: "dor_1", Kind: "renew", Years: 1, AmountMinor: 1500, Currency: tc.currency, Status: "awaiting_payment", PaymentURL: tc.url, PaymentExpiresAt: &expires}
		text := domainOrderText(o)
		for _, w := range tc.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s %v: %q lacks %q", tc.currency, tc.url != nil, text, w)
			}
		}
	}
}
