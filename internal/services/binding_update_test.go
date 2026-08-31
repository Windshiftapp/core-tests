//go:build test

package services

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/models"
)

// UpdateBinding edits an existing binding's mutable config in place (WI-450):
// LLM, repos, TTL, daily budget, instructions, skills — while the acting
// identity and target pool stay fixed.
func TestBindingService_UpdateBinding(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	conn := seedServiceSCMConnection(t, st, 1)
	llm := validTestLLMConnectionID
	poolID := 5

	bindingID, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID: 1, ActingUserID: st.AgentID, ActingUserKind: ActingIdentityKindAgent,
		TargetPoolID: &poolID, LLMConnectionID: &llm, TokenTTLMinutes: 30, MaxRunsPerDay: 2,
		Instructions: "old", TokenScopes: []string{"items:read"}, CreatedByUserID: st.AdminID,
		Repos: []models.BindingRepo{{SCMConnectionID: &conn, RepoSlug: "acme/old", IsPrimary: true}},
	})
	if err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	updated, err := st.BS.UpdateBinding(ctx, UpdateBindingRequest{
		WorkspaceID:     1,
		BindingID:       bindingID,
		LLMConnectionID: &llm,
		TokenTTLMinutes: 45,
		MaxRunsPerDay:   0,
		Instructions:    "new persona",
		Repos:           []RepoInput{{RepoSlug: "acme/new", SCMConnectionID: &conn, IsPrimary: true}},
		// RunnerImage nil, TokenScopes nil → both preserved.
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if updated.TokenTTLMinutes != 45 || updated.MaxRunsPerDay != 0 || updated.Instructions != "new persona" {
		t.Errorf("editable fields not applied: ttl=%d max=%d instr=%q", updated.TokenTTLMinutes, updated.MaxRunsPerDay, updated.Instructions)
	}
	if len(updated.Repos) != 1 || updated.Repos[0].RepoSlug != "acme/new" || !updated.Repos[0].IsPrimary {
		t.Errorf("repos not replaced: %+v", updated.Repos)
	}
	// Identity + pool are immutable.
	if updated.ActingUserID != st.AgentID {
		t.Errorf("acting user changed: %d", updated.ActingUserID)
	}
	if updated.TargetPoolID == nil || *updated.TargetPoolID != poolID {
		t.Errorf("target pool changed: %v", updated.TargetPoolID)
	}
	// Scopes preserved when the request omits them (nil).
	if len(updated.TokenScopes) != 1 || updated.TokenScopes[0] != "items:read" {
		t.Errorf("token scopes not preserved on omit: %v", updated.TokenScopes)
	}
}

func TestBindingService_UpdateBinding_Validation(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	conn := seedServiceSCMConnection(t, st, 1)
	llm := validTestLLMConnectionID

	bindingID, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID: 1, ActingUserID: st.AgentID, ActingUserKind: ActingIdentityKindAgent,
		LLMConnectionID: &llm, TokenTTLMinutes: 30, CreatedByUserID: st.AdminID,
	})
	if err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	base := func() UpdateBindingRequest {
		return UpdateBindingRequest{WorkspaceID: 1, BindingID: bindingID, LLMConnectionID: &llm}
	}

	// LLM is mandatory.
	r := base()
	r.LLMConnectionID = nil
	if _, err := st.BS.UpdateBinding(ctx, r); !errors.Is(err, ErrLLMConnectionRequired) {
		t.Errorf("nil llm: want ErrLLMConnectionRequired, got %v", err)
	}

	// TTL over the agent-token cap.
	r = base()
	r.TokenTTLMinutes = 100000
	if _, err := st.BS.UpdateBinding(ctx, r); !errors.Is(err, ErrBindingTokenTTLOverCap) {
		t.Errorf("over-cap ttl: want ErrBindingTokenTTLOverCap, got %v", err)
	}

	// A repo without an SCM connection is rejected.
	r = base()
	r.Repos = []RepoInput{{RepoSlug: "acme/x", IsPrimary: true}}
	if _, err := st.BS.UpdateBinding(ctx, r); !errors.Is(err, ErrBindingRepoNeedsSCMConnection) {
		t.Errorf("repo w/o conn: want ErrBindingRepoNeedsSCMConnection, got %v", err)
	}
	_ = conn

	// Wrong workspace → not found.
	r = base()
	r.WorkspaceID = 999
	if _, err := st.BS.UpdateBinding(ctx, r); !errors.Is(err, ErrBindingNotFound) {
		t.Errorf("wrong workspace: want ErrBindingNotFound, got %v", err)
	}
}

func TestBindingService_UpdateBinding_ReplacesStandardCapabilityGroups(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	llm := validTestLLMConnectionID

	bindingID, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:      1,
		ActingUserID:     st.AgentID,
		ActingUserKind:   ActingIdentityKindAgent,
		ProfileType:      models.AgentProfileStandard,
		LLMConnectionID:  &llm,
		TokenTTLMinutes:  60,
		Instructions:     "Keep this prompt.",
		CreatedByUserID:  st.AdminID,
		CapabilityGroups: []string{"comment_editing"},
	})
	if err != nil {
		t.Fatalf("seed Standard binding: %v", err)
	}

	groups := []string{"issue_management", "read_comment"}
	updated, err := st.BS.UpdateBinding(ctx, UpdateBindingRequest{
		WorkspaceID:      1,
		BindingID:        bindingID,
		LLMConnectionID:  &llm,
		TokenTTLMinutes:  60,
		Instructions:     "Keep this prompt.",
		CapabilityGroups: &groups,
	})
	if err != nil {
		t.Fatalf("replace capability groups: %v", err)
	}
	if len(updated.CapabilityGroups) != 1 || updated.CapabilityGroups[0] != "issue_management" {
		t.Fatalf("capability groups = %v, want [issue_management]", updated.CapabilityGroups)
	}
	if updated.ProfileVersion != 2 || updated.Lifecycle != models.AgentLifecycleDraft {
		t.Fatalf("updated definition = version %d lifecycle %q", updated.ProfileVersion, updated.Lifecycle)
	}

	invalid := []string{"delete_everything"}
	if _, err := st.BS.UpdateBinding(ctx, UpdateBindingRequest{
		WorkspaceID:      1,
		BindingID:        bindingID,
		LLMConnectionID:  &llm,
		TokenTTLMinutes:  60,
		Instructions:     "Keep this prompt.",
		CapabilityGroups: &invalid,
	}); !errors.Is(err, ErrAgentProfileInvalidCapabilities) {
		t.Fatalf("invalid capabilities: got %v, want ErrAgentProfileInvalidCapabilities", err)
	}
}

func TestBindingService_MigrateLegacyToRunner(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	llm := validTestLLMConnectionID

	bindingID, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		ActingUserKind:  ActingIdentityKindAgent,
		ProfileType:     models.AgentProfileLegacy,
		Lifecycle:       models.AgentLifecycleReady,
		ProfileVersion:  4,
		LLMConnectionID: &llm,
		TokenTTLMinutes: 60,
		Instructions:    "Keep this profile and its history.",
		CreatedByUserID: st.AdminID,
	})
	if err != nil {
		t.Fatalf("seed Legacy binding: %v", err)
	}

	updated, err := st.BS.MigrateLegacyToRunner(ctx, 1, bindingID, 7)
	if err != nil {
		t.Fatalf("migrate Legacy binding: %v", err)
	}
	if updated.ProfileType != models.AgentProfileCoding {
		t.Fatalf("profile type = %q, want coding", updated.ProfileType)
	}
	if updated.TargetPoolID == nil || *updated.TargetPoolID != 7 {
		t.Fatalf("target pool = %v, want 7", updated.TargetPoolID)
	}
	if updated.Lifecycle != models.AgentLifecycleDraft || updated.ProfileVersion != 5 {
		t.Fatalf("definition = lifecycle %q version %d, want draft version 5", updated.Lifecycle, updated.ProfileVersion)
	}

	if _, err := st.BS.MigrateLegacyToRunner(ctx, 1, bindingID, 5); !errors.Is(err, ErrAgentProfileLegacyMigrationOnly) {
		t.Fatalf("second migration: got %v, want ErrAgentProfileLegacyMigrationOnly", err)
	}
	if _, err := st.BS.MigrateLegacyToRunner(ctx, 999, bindingID, 5); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("cross-workspace migration: got %v, want ErrBindingNotFound", err)
	}
}

func TestBindingService_ConnectCodingRunnerIsFirstAssignmentOnly(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	llm := validTestLLMConnectionID

	bindingID, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		ActingUserKind:  ActingIdentityKindAgent,
		ProfileType:     models.AgentProfileCoding,
		Lifecycle:       models.AgentLifecycleDraft,
		ProfileVersion:  2,
		LLMConnectionID: &llm,
		CreatedByUserID: st.AdminID,
	})
	if err != nil {
		t.Fatalf("seed Coding Draft: %v", err)
	}

	updated, err := st.BS.ConnectCodingRunner(ctx, 1, bindingID, 7)
	if err != nil {
		t.Fatalf("connect first runner: %v", err)
	}
	if updated.TargetPoolID == nil || *updated.TargetPoolID != 7 ||
		updated.ProfileVersion != 3 || updated.Lifecycle != models.AgentLifecycleDraft {
		t.Fatalf("connected profile = %+v", updated)
	}

	if _, err := st.BS.ConnectCodingRunner(ctx, 1, bindingID, 5); !errors.Is(err, ErrAgentProfileRunnerAlreadySet) {
		t.Fatalf("runner reassignment = %v, want ErrAgentProfileRunnerAlreadySet", err)
	}
	if _, err := st.BS.ConnectCodingRunner(ctx, 999, bindingID, 5); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("cross-workspace connection = %v, want ErrBindingNotFound", err)
	}
}
