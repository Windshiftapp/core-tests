package tests

import (
	"strings"
	"testing"
)

// TestEnumerateRegisteredRoutes_Smoke is a sanity check that the AST walker
// is actually finding routes from both public Core surfaces. It's not exhaustive
// — TestRouteClassification and TestAnonymousBaseline are the real
// consumers — but it guards against silent zero-result regressions (a
// renamed selector or a missing routes directory would otherwise pass
// quietly).
func TestEnumerateRegisteredRoutes_Smoke(t *testing.T) {
	routes := EnumerateRegisteredRoutes(t)
	if len(routes) < 100 {
		t.Fatalf("expected at least 100 registered routes (the codebase has ~944), got %d — AST walker likely missed a surface", len(routes))
	}

	// Spot-check at least one representative from each surface so a regression
	// that silently kills one prefix (e.g. by renaming the route group var)
	// gets caught.
	want := []struct{ method, path string }{
		{"GET", "/api/items/{id}"},         // legacy /api surface, items routes file
		{"GET", "/rest/api/v1/items/{id}"}, // v1 bearer surface
	}
	for _, w := range want {
		if !containsRoute(routes, w.method, w.path) {
			// Build a short sample of what we found at the relevant prefix to
			// make the failure actionable.
			sample := samplePrefix(routes, w.path[:strings.Index(w.path[1:], "/")+1])
			t.Errorf("expected route %s %s in enumeration, not found.\nSample of matching prefix: %v", w.method, w.path, sample)
		}
	}
}

func containsRoute(routes []RegisteredRoute, method, path string) bool {
	for _, r := range routes {
		if r.Method == method && r.Path == path {
			return true
		}
	}
	return false
}

func samplePrefix(routes []RegisteredRoute, prefix string) []string {
	var out []string
	for _, r := range routes {
		if strings.HasPrefix(r.Path, prefix) {
			out = append(out, r.Method+" "+r.Path)
			if len(out) == 5 {
				break
			}
		}
	}
	return out
}
