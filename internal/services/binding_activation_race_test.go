//go:build test

package services

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/models"
)

func createActivatableProfile(t *testing.T, st *bindingTestStack) *models.WorkspaceAgentBinding {
	t.Helper()
	llmConnectionID := validTestLLMConnectionID
	profile, err := st.BS.CreateStudioProfile(context.Background(), CreateStudioProfileRequest{
		WorkspaceID:     1,
		CreatedByUserID: st.AdminID,
		TemplateKey:     "workspace_guide",
		Name:            "Workspace Guide",
		Handle:          "workspace-guide",
		Purpose:         "Help members find the right workspace context.",
		LLMConnectionID: &llmConnectionID,
	})
	if err != nil {
		t.Fatalf("create Studio profile: %v", err)
	}
	return profile
}

// WI-1803: activation must not mark a configuration Ready using validation of
// an older definition. A model change after validation bumps the profile
// version, so activation with the stale validation is refused and the profile
// stays Draft.
func TestActivateStudioProfileRejectsValidationOfStaleDefinition(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	st.BS.llmRuntime = &fakeLLMRuntime{valid: map[int]bool{validTestLLMConnectionID: true, 2: true}}
	profile := createActivatableProfile(t, st)

	validation, err := st.BS.ValidateStudioProfile(ctx, 1, profile.ID)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !validation.Ready {
		t.Fatalf("expected a ready validation, got %+v", validation)
	}
	if validation.ProfileVersion != profile.ProfileVersion {
		t.Fatalf("validation version = %d, want the validated definition version %d", validation.ProfileVersion, profile.ProfileVersion)
	}

	// A concurrent model change lands between validation and activation.
	newLLM := 2
	if err := st.BS.UpdateAgentConfig(ctx, 1, profile.ID, profile.Instructions, nil, nil, &newLLM); err != nil {
		t.Fatalf("change connection: %v", err)
	}

	if _, _, err := st.BS.activateStudioProfile(ctx, 1, profile.ID, validation); !errors.Is(err, ErrAgentProfileVersionConflict) {
		t.Fatalf("stale activation = %v, want ErrAgentProfileVersionConflict", err)
	}
	reloaded, err := st.Bindings.Get(ctx, profile.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Lifecycle != models.AgentLifecycleDraft {
		t.Fatalf("lifecycle = %q, want draft; the changed definition must not be marked Ready", reloaded.Lifecycle)
	}
}

// A validation captured against the current definition still activates.
func TestActivateStudioProfileActivatesCurrentDefinition(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	profile := createActivatableProfile(t, st)

	validation, err := st.BS.ValidateStudioProfile(ctx, 1, profile.ID)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	activated, _, err := st.BS.activateStudioProfile(ctx, 1, profile.ID, validation)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if activated.Lifecycle != models.AgentLifecycleReady {
		t.Fatalf("lifecycle = %q, want ready", activated.Lifecycle)
	}
}
