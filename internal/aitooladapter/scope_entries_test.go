package aitooladapter_test

import (
	"slices"
	"testing"

	"windshift/internal/aitooladapter"
	"windshift/internal/aitools"
	"windshift/internal/auth"
)

func entryNames(entries []aitools.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

// The in-product chat authenticates with a cookie and has no token to read
// scopes from, so it gates its registry on DefaultAgentScopes instead (WI-962).
// This pins the resulting surface: everything the default token set can reach,
// and nothing it cannot.
func TestEntriesForScopesMatchesDefaultAgentScopes(t *testing.T) {
	entries := aitooladapter.EntriesForScopes(aitools.Default, auth.DefaultAgentScopes)
	names := entryNames(entries)

	if len(entries) == 0 {
		t.Fatal("EntriesForScopes returned no entries for DefaultAgentScopes")
	}
	if len(entries) >= len(aitools.Default.All()) {
		t.Fatalf("expected the default scope set to exclude some tools, got %d of %d",
			len(entries), len(aitools.Default.All()))
	}

	// items:delete is deliberately not a default scope.
	for _, excluded := range []string{"delete_item", "delete_comment"} {
		if slices.Contains(names, excluded) {
			t.Errorf("tool %q requires a delete scope and must not be in the default surface", excluded)
		}
	}

	// actions:read/actions:write joined the defaults, so automation authoring
	// stays available to chat.
	for _, included := range []string{
		"create_action", "update_action", "get_action",
		"log_time", "start_timer", "stop_timer", "list_worklogs",
		"create_item", "update_item", "search_items",
	} {
		if !slices.Contains(names, included) {
			t.Errorf("tool %q should be reachable with DefaultAgentScopes", included)
		}
	}

	// Every admitted entry must genuinely be covered by the scope set.
	for _, e := range entries {
		if !auth.ScopesSatisfy(auth.DefaultAgentScopes, e.Scopes) {
			t.Errorf("tool %q admitted but requires unheld scopes %v", e.Name, e.Scopes)
		}
	}
}

func TestEntriesForScopesEmptyScopeSetAdmitsNothing(t *testing.T) {
	if entries := aitooladapter.EntriesForScopes(aitools.Default, nil); len(entries) != 0 {
		t.Errorf("expected no entries for an empty scope set, got %d", len(entries))
	}
}

// A narrower set must shrink the surface rather than fail open.
func TestEntriesForScopesReadOnlySetExcludesWrites(t *testing.T) {
	entries := aitooladapter.EntriesForScopes(aitools.Default, []string{auth.ScopeItemsRead})
	for _, e := range entries {
		for _, s := range e.Scopes {
			if s != auth.ScopeItemsRead {
				t.Errorf("tool %q admitted with scopes %v under an items:read-only set", e.Name, e.Scopes)
			}
		}
	}
	names := entryNames(entries)
	if slices.Contains(names, "create_item") {
		t.Error("create_item requires items:write and must not be admitted by items:read")
	}
	if !slices.Contains(names, "get_item") {
		t.Error("get_item requires only items:read and should be admitted")
	}
}
