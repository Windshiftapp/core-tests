package wscli

import "testing"

// Auto-onboarding tokens must cover every command the CLI currently ships.
// Missing a scope here means a fresh `ws init` user can't run the matching
// commands without manually editing or recreating their token. Bug-hunt
// finding #1 (2026-05-22) saw this for the page commands.
func TestDefaultCLIScopes_CoversShippedCommandSurface(t *testing.T) {
	required := []string{
		"items:read",
		"items:write",
		"workspaces:read",
		"workspaces:write",
		"users:read",
		"item-types:read",
		"workflows:read",
		"pages:read",
		"pages:write",
		"pages:delete",
	}
	have := make(map[string]struct{}, len(defaultCLIScopes))
	for _, s := range defaultCLIScopes {
		have[s] = struct{}{}
	}
	for _, want := range required {
		if _, ok := have[want]; !ok {
			t.Errorf("defaultCLIScopes missing %q — a fresh `ws init` user will hit scope failures on the matching commands", want)
		}
	}
}
