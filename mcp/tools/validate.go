package tools

import (
	"regexp"
)

// idPattern is what every id a tool puts in a URL path must look like: the
// API's ids are a short prefix, an underscore and letters or digits
// (vm_…, op_…), and zone and slug ids use hyphens. Rejecting everything
// else means no argument can add a path segment ("..", "/") or a query.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// checkIDs validates name/value pairs of path ids.
func checkIDs(pairs ...string) error {
	for i := 0; i+1 < len(pairs); i += 2 {
		if !idPattern.MatchString(pairs[i+1]) {
			return errorf("%s is not a valid id: use the id exactly as a list or get tool returned it (for example vm_…).", pairs[i])
		}
	}
	return nil
}

// checkOptionalIDs is checkIDs for ids that may be left out.
func checkOptionalIDs(pairs ...string) error {
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			if err := checkIDs(pairs[i], pairs[i+1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// listLimit clamps a page size to 1..100, defaulting to 50.
func listLimit(n int) int {
	switch {
	case n <= 0:
		return 50
	case n > 100:
		return 100
	}
	return n
}

// argPattern is what an argument may look like to be quoted in our own
// summary text: a slug, id, version, CIDR or port range.
var argPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

// arg returns a model-supplied argument for a preview's or result's summary
// text, or a placeholder when it is anything else. Summary text is ours: an
// argument with spaces, newlines or quotes (perhaps planted by a web page the
// model read) could otherwise forge a line such as "Price: free" in the
// preview the user approves. The full request is always in the fenced data.
func arg(s string) string {
	if argPattern.MatchString(s) {
		return s
	}
	return "(see the request in the data)"
}
