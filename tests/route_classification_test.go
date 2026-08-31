package tests

import (
	"sort"
	"strings"
	"testing"
)

// TestRouteClassification is the drift guard for the central permission
// matrix. For every registered route whose path starts with an enforced
// prefix, the test requires the route to be either:
//
//  1. Classified in MatrixRoutes (i.e. carry a PermissionClass), or
//  2. Explicitly listed in RouteClassificationExemptions with a reason.
//
// A new route that lands without either fails the test. Reviewers see the
// classification decision (or the conscious exemption) at PR time, instead
// of silently shipping with whatever 403/404/200 mix the handler happens
// to return today.
//
// Enforcement is prefix-scoped: only routes whose path starts with one of
// EnforcedPrefixes is checked. The matrix grows incrementally — each new
// prefix becomes an explicit policy decision rather than a 944-route
// landfall. Slice 4 enforces /api/items only; subsequent slices expand
// the prefix list.
//
// Placeholder names are normalized (`/items/{id}` and `/items/{itemId}`
// compare equal) so the matrix can use whichever placeholder names map
// cleanly to fixture fields without having to mirror the registration
// names byte-for-byte.
func TestRouteClassification(t *testing.T) {
	registered := EnumerateRegisteredRoutes(t)

	// Build a lookup of (method, normalized-path) → matrix entry.
	classified := make(map[string]struct{}, len(MatrixRoutes))
	for _, r := range MatrixRoutes {
		classified[routeKey(r.Method, r.Path)] = struct{}{}
	}

	// Build a lookup of (method, normalized-path) → exemption reason.
	exempted := make(map[string]string, len(RouteClassificationExemptions))
	for _, e := range RouteClassificationExemptions {
		exempted[routeKey(e.Method, e.Path)] = e.Reason
	}

	var unclassified []RegisteredRoute
	for _, r := range registered {
		if !isEnforcedPath(r.Path) {
			continue
		}
		key := routeKey(r.Method, r.Path)
		if _, ok := classified[key]; ok {
			continue
		}
		if _, ok := exempted[key]; ok {
			continue
		}
		unclassified = append(unclassified, r)
	}

	if len(unclassified) == 0 {
		return
	}

	sort.Slice(unclassified, func(i, j int) bool {
		if unclassified[i].Path != unclassified[j].Path {
			return unclassified[i].Path < unclassified[j].Path
		}
		return unclassified[i].Method < unclassified[j].Method
	})

	var b strings.Builder
	b.WriteString("unclassified routes under enforced prefixes (add to MatrixRoutes or RouteClassificationExemptions):\n")
	for _, r := range unclassified {
		b.WriteString("\t")
		b.WriteString(r.Method)
		b.WriteString(" ")
		b.WriteString(r.Path)
		b.WriteString("\n")
	}
	t.Fatal(b.String())
}

// isEnforcedPath reports whether the registered path falls under any
// enforced prefix. Matches by HasPrefix so `/api/items` covers `/api/items`,
// `/api/items/{id}`, and any deeper subroute.
func isEnforcedPath(path string) bool {
	for _, p := range EnforcedPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// routeKey normalizes a (method, path) pair for comparison. Placeholder
// names are reduced to a single sentinel so `/items/{id}` and
// `/items/{itemId}` compare equal.
func routeKey(method, path string) string {
	return method + " " + normalizePlaceholders(path)
}

// normalizePlaceholders rewrites every `{name}` segment to `{*}`. Used to
// compare route templates whose placeholders differ in name but not in
// position.
func normalizePlaceholders(path string) string {
	var b strings.Builder
	b.Grow(len(path))
	for i := 0; i < len(path); {
		if path[i] != '{' {
			b.WriteByte(path[i])
			i++
			continue
		}
		j := i + 1
		for j < len(path) && path[j] != '}' {
			j++
		}
		if j == len(path) {
			b.WriteString(path[i:])
			break
		}
		b.WriteString("{*}")
		i = j + 1
	}
	return b.String()
}
