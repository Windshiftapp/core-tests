//go:build test

package services

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/models"
)

func TestBindingService_UpdateStudioProfileEditsOnlyWorkspaceManagedIdentity(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	llm := validTestLLMConnectionID

	bindingID, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    st.SvcUserID,
		ActingUserKind:  ActingIdentityKindCentralized,
		ProfileType:     models.AgentProfileStandard,
		Lifecycle:       models.AgentLifecycleReady,
		ProfileVersion:  1,
		IdentityClass:   models.AgentIdentityWorkspaceManaged,
		Purpose:         "Old purpose",
		LLMConnectionID: &llm,
		TokenTTLMinutes: 60,
		CreatedByUserID: st.AdminID,
	})
	if err != nil {
		t.Fatalf("seed workspace-managed profile: %v", err)
	}

	updated, err := st.BS.UpdateStudioProfile(ctx, UpdateStudioProfileRequest{
		WorkspaceID:     1,
		BindingID:       bindingID,
		ExpectedVersion: 1,
		Name:            "Workspace Navigator",
		Handle:          "workspace-navigator",
		AvatarURL:       "https://example.test/avatar.png",
		Purpose:         "Guide teammates through delivery.",
	})
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if updated.DisplayName != "Workspace Navigator" ||
		updated.Handle != "workspace-navigator" ||
		updated.AvatarURL != "https://example.test/avatar.png" {
		t.Fatalf("updated identity = %+v", updated)
	}
	if updated.Purpose != "Guide teammates through delivery." ||
		updated.ProfileVersion != 2 ||
		updated.Lifecycle != models.AgentLifecycleDraft {
		t.Fatalf("updated profile = %+v", updated)
	}

	if _, err := st.BS.UpdateStudioProfile(ctx, UpdateStudioProfileRequest{
		WorkspaceID:     1,
		BindingID:       bindingID,
		ExpectedVersion: 1,
		Name:            "Stale overwrite",
		Handle:          "stale-overwrite",
		Purpose:         "Must not win.",
	}); !errors.Is(err, ErrAgentProfileVersionConflict) {
		t.Fatalf("stale update = %v, want ErrAgentProfileVersionConflict", err)
	}
}

func TestBindingService_UpdateStudioProfileKeepsCentralIdentityReadOnly(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	llm := validTestLLMConnectionID

	bindingID, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    st.SvcUserID,
		ActingUserKind:  ActingIdentityKindCentralized,
		ProfileType:     models.AgentProfileStandard,
		Lifecycle:       models.AgentLifecycleDraft,
		ProfileVersion:  1,
		IdentityClass:   models.AgentIdentityCentralized,
		Purpose:         "Old purpose",
		LLMConnectionID: &llm,
		TokenTTLMinutes: 60,
		CreatedByUserID: st.AdminID,
	})
	if err != nil {
		t.Fatalf("seed centralized profile: %v", err)
	}
	current, err := st.Bindings.Get(ctx, bindingID)
	if err != nil {
		t.Fatalf("load centralized profile: %v", err)
	}

	updated, err := st.BS.UpdateStudioProfile(ctx, UpdateStudioProfileRequest{
		WorkspaceID:     1,
		BindingID:       bindingID,
		ExpectedVersion: 1,
		Name:            current.DisplayName,
		Handle:          current.Handle,
		AvatarURL:       current.AvatarURL,
		Purpose:         "New purpose",
	})
	if err != nil {
		t.Fatalf("update centralized purpose: %v", err)
	}
	if updated.Purpose != "New purpose" || updated.DisplayName != current.DisplayName {
		t.Fatalf("centralized purpose update = %+v", updated)
	}

	if _, err := st.BS.UpdateStudioProfile(ctx, UpdateStudioProfileRequest{
		WorkspaceID:     1,
		BindingID:       bindingID,
		ExpectedVersion: 2,
		Name:            "Renamed centrally",
		Handle:          current.Handle,
		AvatarURL:       current.AvatarURL,
		Purpose:         "New purpose",
	}); !errors.Is(err, ErrAgentProfileIdentityImmutable) {
		t.Fatalf("centralized identity mutation = %v, want ErrAgentProfileIdentityImmutable", err)
	}
}
