package auth_test

import (
	"slices"
	"strings"
	"testing"

	"windshift/internal/auth"
)

// The catalog is the single source of truth for every scope-granting surface
// (WI-958). These tests pin the properties the pickers, the OAuth flows and the
// validator all rely on.

func TestScopeCatalogCoversAllValidScopes(t *testing.T) {
	catalog := auth.ScopeCatalog()
	if len(catalog) != len(auth.AllValidScopes) {
		t.Fatalf("catalog has %d entries but AllValidScopes has %d", len(catalog), len(auth.AllValidScopes))
	}
	for _, info := range catalog {
		if !slices.Contains(auth.AllValidScopes, info.Scope) {
			t.Errorf("catalog scope %q missing from AllValidScopes", info.Scope)
		}
		if auth.ValidateScopes([]string{info.Scope}) != nil {
			t.Errorf("catalog scope %q does not validate", info.Scope)
		}
	}
}

func TestScopeCatalogEntriesAreRenderable(t *testing.T) {
	seen := map[string]bool{}
	for _, info := range auth.ScopeCatalog() {
		if seen[info.Scope] {
			t.Errorf("duplicate catalog entry for %q", info.Scope)
		}
		seen[info.Scope] = true

		if info.Label == "" || info.Description == "" || info.ResourceLabel == "" {
			t.Errorf("scope %q is missing picker metadata (label=%q description=%q resourceLabel=%q)",
				info.Scope, info.Label, info.Description, info.ResourceLabel)
		}
		// The scope string must agree with its own resource/action split, since
		// the pickers build checkbox ids from those fields.
		if want := info.Resource + ":" + info.Action; want != info.Scope {
			t.Errorf("scope %q does not match resource:action %q", info.Scope, want)
		}
		if info.Admin != strings.HasPrefix(info.Scope, "admin:") {
			t.Errorf("scope %q Admin=%v disagrees with its prefix", info.Scope, info.Admin)
		}
	}
}

// Every scope in the catalog must be grantable from the token UI. This is the
// regression guard for the drift that made time:read/time:write unreachable:
// the picker now renders the catalog, so a scope missing metadata (asserted
// above) or missing from the catalog entirely is the only way to reintroduce it.
func TestScopeCatalogIncludesTimeAndActionScopes(t *testing.T) {
	for _, want := range []string{
		auth.ScopeTimeRead, auth.ScopeTimeWrite, auth.ScopeTimeDelete,
		auth.ScopeActionsRead, auth.ScopeActionsWrite,
		auth.ScopeTestsRead, auth.ScopeTestsWrite,
		auth.ScopeAssetsRead, auth.ScopeAssetsWrite, auth.ScopeAssetsDelete,
		auth.ScopeAgentSkillsRead,
		auth.ScopeUserPreferencesRead, auth.ScopeUserPreferencesWrite,
	} {
		if !slices.ContainsFunc(auth.ScopeCatalog(), func(i auth.ScopeInfo) bool { return i.Scope == want }) {
			t.Errorf("scope %q is not in the catalog, so no picker can grant it", want)
		}
	}
}

func TestDefaultAgentScopesContainsTimeAndActions(t *testing.T) {
	for _, want := range []string{
		auth.ScopeTimeRead, auth.ScopeTimeWrite,
		auth.ScopeActionsRead, auth.ScopeActionsWrite,
		auth.ScopeMCPAccess,
	} {
		if !slices.Contains(auth.DefaultAgentScopes, want) {
			t.Errorf("DefaultAgentScopes is missing %q", want)
		}
	}
}

// Destructive scopes stay opt-in. pages:delete is the documented exception —
// archiving a page is how the page tools retire content.
func TestDefaultAgentScopesExcludesDestructiveScopes(t *testing.T) {
	for _, unwanted := range []string{
		auth.ScopeItemsDelete, auth.ScopeWorkspacesDelete, auth.ScopeTimeDelete,
		auth.ScopeAssetsDelete, auth.ScopeMilestonesDelete, auth.ScopeIterationsDelete,
		auth.ScopeProjectsDelete,
	} {
		if slices.Contains(auth.DefaultAgentScopes, unwanted) {
			t.Errorf("DefaultAgentScopes must not grant %q by default", unwanted)
		}
	}
	for _, s := range auth.DefaultAgentScopes {
		if auth.IsAdminScope(s) {
			t.Errorf("DefaultAgentScopes must not contain admin scope %q", s)
		}
	}
}

func TestNonAdminScopesExcludesAdminScopes(t *testing.T) {
	nonAdmin := auth.NonAdminScopes()
	for _, s := range nonAdmin {
		if auth.IsAdminScope(s) {
			t.Errorf("NonAdminScopes returned admin scope %q", s)
		}
	}
	if len(nonAdmin)+len(auth.AdminScopes()) != len(auth.AllValidScopes) {
		t.Errorf("NonAdminScopes(%d) + AdminScopes(%d) != AllValidScopes(%d)",
			len(nonAdmin), len(auth.AdminScopes()), len(auth.AllValidScopes))
	}
}

// Legacy strings are no longer a valid mint input (WI-959).
func TestValidateScopesRejectsLegacyStrings(t *testing.T) {
	for _, legacy := range []string{"read", "write", "admin"} {
		if err := auth.ValidateScopes([]string{legacy}); err == nil {
			t.Errorf("legacy scope %q must no longer validate", legacy)
		}
	}
}

func TestScopesSatisfy(t *testing.T) {
	tests := []struct {
		name     string
		held     []string
		required []string
		want     bool
	}{
		{"exact match", []string{"items:read"}, []string{"items:read"}, true},
		{"write implies read", []string{"items:write"}, []string{"items:read"}, true},
		{"read does not imply write", []string{"items:read"}, []string{"items:write"}, false},
		{"admin write implies admin read", []string{"admin:users:write"}, []string{"admin:users:read"}, true},
		{"cross-resource write does not imply read", []string{"items:write"}, []string{"pages:read"}, false},
		{"all required must be held", []string{"items:read"}, []string{"items:read", "pages:read"}, false},
		{"empty required is satisfied", nil, nil, true},
		{"legacy string grants nothing", []string{"write"}, []string{"items:write"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := auth.ScopesSatisfy(tt.held, tt.required); got != tt.want {
				t.Errorf("ScopesSatisfy(%v, %v) = %v, want %v", tt.held, tt.required, got, tt.want)
			}
		})
	}
}
