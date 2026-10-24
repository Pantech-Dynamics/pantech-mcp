package mcp

import (
	"os"
	"regexp"
	"testing"
)

// TestVersionMatchesChangelog keeps the default Version in step with the
// newest release heading in CHANGELOG.md, which is what gets tagged.
func TestVersionMatchesChangelog(t *testing.T) {
	raw, err := os.ReadFile("../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^## v(\d+\.\d+\.\d+) — \d{4}-\d{2}-\d{2}$`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("CHANGELOG.md has no `## vX.Y.Z — YYYY-MM-DD` heading")
	}
	if got := string(m[1]); got != Version {
		t.Fatalf("CHANGELOG.md's newest version is %s, but Version is %s: bump one of them", got, Version)
	}
}
