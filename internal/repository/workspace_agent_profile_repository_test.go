package repository

import (
	"context"
	"slices"
	"testing"

	"windshift/internal/models"
)

func TestWorkspaceAgentBindingRepository_PersistsAgentStudioProfileContract(t *testing.T) {
	ctx := context.Background()
	db, admin, agentA, agentB := openBindingTestDB(t)
	repo := NewWorkspaceAgentBindingRepository(db)

	legacyID, err := repo.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    agentA,
		ActingUserKind:  "agent",
		CreatedByUserID: admin,
	})
	if err != nil {
		t.Fatalf("insert legacy binding: %v", err)
	}
	legacy, err := repo.Get(ctx, legacyID)
	if err != nil {
		t.Fatalf("get legacy binding: %v", err)
	}
	if legacy.ProfileType != models.AgentProfileLegacy {
		t.Fatalf("local existing binding type = %q, want legacy", legacy.ProfileType)
	}
	if legacy.Lifecycle != models.AgentLifecycleReady || legacy.ProfileVersion != 1 {
		t.Fatalf("legacy lifecycle/version = %q/%d, want ready/1", legacy.Lifecycle, legacy.ProfileVersion)
	}
	if legacy.IdentityClass != models.AgentIdentityUserOwned {
		t.Fatalf("agent acting kind identity class = %q, want user_owned", legacy.IdentityClass)
	}

	standardID, err := repo.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:    1,
		ActingUserID:   agentB,
		ActingUserKind: "centralized_service",
		ProfileType:    models.AgentProfileStandard,
		Lifecycle:      models.AgentLifecycleDraft,
		IdentityClass:  models.AgentIdentityWorkspaceManaged,
		Purpose:        "Triage incoming work",
		CapabilityGroups: []string{
			"issue_management",
			"users_approvals",
		},
		CreatedByUserID: admin,
	})
	if err != nil {
		t.Fatalf("insert Standard profile: %v", err)
	}
	standard, err := repo.Get(ctx, standardID)
	if err != nil {
		t.Fatalf("get Standard profile: %v", err)
	}
	if standard.ProfileType != models.AgentProfileStandard ||
		standard.Lifecycle != models.AgentLifecycleDraft ||
		standard.IdentityClass != models.AgentIdentityWorkspaceManaged {
		t.Fatalf("Standard profile contract not preserved: %+v", standard)
	}
	if standard.Purpose != "Triage incoming work" {
		t.Fatalf("purpose = %q", standard.Purpose)
	}
	if !slices.Equal(standard.CapabilityGroups, []string{"issue_management", "users_approvals"}) {
		t.Fatalf("capability groups = %v", standard.CapabilityGroups)
	}
}

func TestWorkspaceAgentBindingRepository_ArchiveRestorePreservesStableProfile(t *testing.T) {
	ctx := context.Background()
	db, admin, agentA, _ := openBindingTestDB(t)
	repo := NewWorkspaceAgentBindingRepository(db)
	if _, err := db.Exec(`
		UPDATE users
		SET first_name = 'Release', last_name = 'Guide',
		    username = 'release-guide', avatar_url = '/avatars/release.png'
		WHERE id = ?
	`, agentA); err != nil {
		t.Fatalf("seed display identity: %v", err)
	}

	id, err := repo.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    agentA,
		ActingUserKind:  "agent",
		CreatedByUserID: admin,
	})
	if err != nil {
		t.Fatalf("insert profile: %v", err)
	}

	affected, err := repo.Archive(ctx, id, 1, admin)
	if err != nil {
		t.Fatalf("archive profile: %v", err)
	}
	if affected != 1 {
		t.Fatalf("archive affected %d rows, want 1", affected)
	}
	archived, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("get archived profile: %v", err)
	}
	if archived.Lifecycle != models.AgentLifecycleArchived ||
		archived.ArchivedAt == nil ||
		archived.ArchivedByUserID == nil ||
		*archived.ArchivedByUserID != admin {
		t.Fatalf("archive metadata incomplete: %+v", archived)
	}
	if archived.LastKnownName != "Release Guide" ||
		archived.LastKnownHandle != "release-guide" ||
		archived.LastKnownAvatar != "/avatars/release.png" {
		t.Fatalf("identity snapshot mismatch: %+v", archived)
	}
	invokable, err := repo.FindByActingUser(ctx, 1, agentA)
	if err != nil {
		t.Fatalf("find archived profile: %v", err)
	}
	if invokable != nil {
		t.Fatalf("archived profile remained triggerable: %+v", invokable)
	}

	affected, err = repo.Restore(ctx, id, 1)
	if err != nil {
		t.Fatalf("restore profile: %v", err)
	}
	if affected != 1 {
		t.Fatalf("restore affected %d rows, want 1", affected)
	}
	restored, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("get restored profile: %v", err)
	}
	if restored.ID != id || restored.ActingUserID != agentA {
		t.Fatalf("restore changed stable identifiers: %+v", restored)
	}
	if restored.Lifecycle != models.AgentLifecycleDraft ||
		restored.ArchivedAt != nil ||
		restored.ArchivedByUserID != nil {
		t.Fatalf("restore state mismatch: %+v", restored)
	}
}

func TestWorkspaceAgentBindingRepository_RuntimeEditCreatesDraftVersion(t *testing.T) {
	ctx := context.Background()
	db, admin, agentA, _ := openBindingTestDB(t)
	repo := NewWorkspaceAgentBindingRepository(db)

	id, err := repo.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    agentA,
		ActingUserKind:  "agent",
		CreatedByUserID: admin,
	})
	if err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	profile, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	profile.Instructions = "Review every change for release risk."
	if err := repo.UpdateConfig(ctx, profile); err != nil {
		t.Fatalf("update runtime config: %v", err)
	}

	updated, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("reload profile: %v", err)
	}
	if updated.Lifecycle != models.AgentLifecycleDraft {
		t.Fatalf("runtime edit lifecycle = %q, want draft", updated.Lifecycle)
	}
	if updated.ProfileVersion != 2 {
		t.Fatalf("runtime edit profile version = %d, want 2", updated.ProfileVersion)
	}
}
