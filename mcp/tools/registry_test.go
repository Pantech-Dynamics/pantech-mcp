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
	"list_instances": Read, "get_instance": Read, "create_instance": Cost, "start_instance": Write, "stop_instance": Write,
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
}

// noConfirmation are the only write routes a one-step tool may call although
// they would otherwise need confirmation, and why. Closing temporary SSH access
// only takes access away, and the API treats it as a plain write.
var noConfirmation = map[string]string{
	"DELETE /instances/{instance_id}/ssh-access/{grant_id}": "revoke_ssh_access only closes a port",
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
			if tool.Risk == Read && method != "GET" && r != "POST /pricing/estimate" {
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
