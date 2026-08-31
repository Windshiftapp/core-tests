package auth

import (
	"strings"
	"testing"
)

// ValidateAgentScopes is the chokepoint that keeps a coding-agent run
// token from grabbing scopes the orchestrator never intended. These
// tests guard the cross-cutting invariants: admin:* must be impossible
// regardless of the per-binding configuration, legacy strings must be
// impossible, and the per-run scope set must round-trip cleanly.

func TestValidateAgentScopes_AcceptsRunScopeSet(t *testing.T) {
	// ValidateAgentScopes gates short-lived per-run coding-agent tokens
	// against DefaultCodingAgentRunScopes — intentionally narrower than the
	// broader DefaultAgentScopes minted for interactive agent/CLI tokens
	// (WI-238). The run set, including page writes, must round-trip cleanly
	// through its own gate.
	if err := ValidateAgentScopes(DefaultCodingAgentRunScopes); err != nil {
		t.Fatalf("DefaultCodingAgentRunScopes must validate; got %v", err)
	}
	if err := ValidateAgentScopes([]string{ScopePagesWrite}); err != nil {
		t.Fatalf("coding-agent page writes must validate; got %v", err)
	}
}

func TestValidateAgentScopes_RejectsBroaderInteractiveSet(t *testing.T) {
	// DefaultAgentScopes (interactive agent/CLI mint) is deliberately broader
	// than the per-run gate — it includes workspaces:write, pages:delete,
	// assets:write, tests:write, and actions:write. A per-run token must not
	// inherit the full interactive set wholesale, so validating it must fail.
	if err := ValidateAgentScopes(DefaultAgentScopes); err == nil {
		t.Fatalf("DefaultAgentScopes is broader than the run gate and must not validate wholesale")
	}
}

func TestValidateAgentScopes_RejectsAdminScope(t *testing.T) {
	for _, s := range AdminScopes() {
		err := ValidateAgentScopes([]string{s})
		if err == nil {
			t.Errorf("admin scope %q must be rejected", s)
			continue
		}
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error must mention %q, got %v", s, err)
		}
	}
}

func TestValidateAgentScopes_RejectsLegacyScopes(t *testing.T) {
	for _, s := range []string{"read", "write", "admin"} {
		if err := ValidateAgentScopes([]string{s}); err == nil {
			t.Errorf("legacy scope %q must be rejected for agent tokens", s)
		}
	}
}

func TestValidateAgentScopes_RejectsItemsDelete(t *testing.T) {
	// items:delete is in AllValidScopes but deliberately excluded from
	// DefaultAgentScopes — a per-run token must not be able to remove
	// items in the workspace.
	if err := ValidateAgentScopes([]string{ScopeItemsDelete}); err == nil {
		t.Errorf("%s must be rejected for agent tokens", ScopeItemsDelete)
	}
}

func TestValidateAgentScopes_RejectsPlanningWrites(t *testing.T) {
	for _, s := range []string{ScopeMilestonesWrite, ScopeIterationsWrite, ScopeProjectsWrite} {
		if err := ValidateAgentScopes([]string{s}); err == nil {
			t.Errorf("planning :write scope %q must be rejected for agent tokens", s)
		}
	}
}

func TestValidateAgentScopes_RejectsUnknownScope(t *testing.T) {
	err := ValidateAgentScopes([]string{"made:up"})
	if err == nil || !strings.Contains(err.Error(), "made:up") {
		t.Errorf("expected agent-scope error mentioning made:up, got %v", err)
	}
}

func TestValidateAgentScopes_AcceptsEmpty(t *testing.T) {
	// Empty input means the caller intends to default to DefaultAgentScopes
	// later. Validation itself must not reject an empty list — that would
	// turn a missing-list bug into a misleading "scopes not permitted"
	// error path at the wrong layer.
	if err := ValidateAgentScopes(nil); err != nil {
		t.Errorf("empty scope list must validate; got %v", err)
	}
}
