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
			if (method == "DELETE" || method == "PUT" || method == "PATCH") && !tool.Risk.NeedsConfirmation() {
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
		"POST /instances/{id}/volume-key-credential", "POST /instances/{id}/ssh-access", "DELETE /me"} {
		if !ExcludedRoute(r) {
			t.Errorf("%s should be excluded", r)
		}
	}
}

// TestNoExcludedCalls scans the tool sources for the SDK methods and paths
// that return secrets, so a tool cannot reach them through Do either.
func TestNoExcludedCalls(t *testing.T) {
	banned := []string{
		"GetKubeconfig", "IssueVolumeKeyCredential", "OpenConsole", "OpenSSHAccess", "GetSSHAccess", "RevokeSSHAccess",
		"ChangePassword", "ResetPassword", "Webhooks.Create", "RotateSecret", "SSHKeys.Create", "Databases.Create(",
		"CreateCreditPurchase", "Me.Revoke",
		`"/kubeconfig`, `/volume-key-credential"`, `/console"`, `/ssh-access`, `/password"`, `/reset-password"`, `/rotate-secret"`,
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
