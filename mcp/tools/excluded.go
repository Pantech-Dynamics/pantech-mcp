package tools

import (
	"regexp"
	"strings"
)

// excludedRoutes return or take secrets (kubeconfig, volume key credential,
// console, Kubernetes manifests read or applied, which may hold Kubernetes
// secrets, database passwords, webhook signing secrets) or act on the caller's
// own key or money. Temporary SSH
// access is allowed: a grant holds only a host, a port and an expiry, and the
// user logs in with their own key. No tool may call them: TestNoExcludedRoutes
// checks every tool's routes and the end-to-end test every request made.
var excludedRoutes = []*regexp.Regexp{
	regexp.MustCompile(`/kubeconfig$`),
	regexp.MustCompile(`/volume-key-credential$`),
	regexp.MustCompile(`/instances/[^/]+/console$`),
	regexp.MustCompile(`/live-manifests$`),
	regexp.MustCompile(`/apply-manifest$`),
	regexp.MustCompile(`/password$`),
	regexp.MustCompile(`/reset-password$`),
	regexp.MustCompile(`^POST /webhooks$`),
	regexp.MustCompile(`/rotate-secret$`),
	regexp.MustCompile(`/billing/credit/purchases`),
	regexp.MustCompile(`/me$`), // DELETE /me revokes the caller's key; GET /me is allowed below
}

// ExcludedRoute reports whether "METHOD /path" is a route no tool may call.
func ExcludedRoute(route string) bool {
	if route == "GET /me" {
		return false
	}
	_, path, _ := strings.Cut(route, " ")
	for _, re := range excludedRoutes {
		if re.MatchString(route) || re.MatchString(path) {
			return true
		}
	}
	return false
}
