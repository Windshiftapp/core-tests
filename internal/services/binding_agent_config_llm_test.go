//go:build test

package services

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/models"
)

// UpdateAgentConfig can retarget a profile at another enabled LLM connection,
// which is how the Agent Studio editor changes a workspace agent's model.
func TestBindingService_UpdateAgentConfig_ChangesLLMConnection(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	// The default stack only resolves connection 1; allow a second so the test
	// can prove the value actually changes.
	st.BS.llmRuntime = &fakeLLMRuntime{valid: map[int]bool{1: true, 2: true}}

	oldLLM := 1
	bindingID, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID: 1, ActingUserID: st.AgentID, ActingUserKind: ActingIdentityKindAgent,
		LLMConnectionID: &oldLLM, TokenTTLMinutes: 30, CreatedByUserID: st.AdminID,
	})
	if err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	newLLM := 2
	if err := st.BS.UpdateAgentConfig(ctx, 1, bindingID, "keep me", nil, nil, &newLLM); err != nil {
		t.Fatalf("change connection: %v", err)
	}
	b, err := st.Bindings.Get(ctx, bindingID)
	if err != nil {
		t.Fatalf("reload binding: %v", err)
	}
	if b.LLMConnectionID == nil || *b.LLMConnectionID != newLLM {
		t.Fatalf("llm connection = %v, want %d", b.LLMConnectionID, newLLM)
	}
	if b.Instructions != "keep me" {
		t.Fatalf("instructions = %q, want preserved", b.Instructions)
	}
	// A model change invalidates readiness, so the profile returns to Draft.
	if b.Lifecycle != models.AgentLifecycleDraft {
		t.Fatalf("lifecycle = %q, want draft", b.Lifecycle)
	}
	// One configuration change is one new profile version, not two.
	if b.ProfileVersion != 2 {
		t.Fatalf("profile version = %d, want 2", b.ProfileVersion)
	}
}

// Omitting the connection preserves it, and an unknown connection is rejected
// before any write happens.
func TestBindingService_UpdateAgentConfig_LLMConnectionValidation(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)

	oldLLM := validTestLLMConnectionID
	bindingID, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID: 1, ActingUserID: st.AgentID, ActingUserKind: ActingIdentityKindAgent,
		LLMConnectionID: &oldLLM, TokenTTLMinutes: 30, CreatedByUserID: st.AdminID,
	})
	if err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	if err := st.BS.UpdateAgentConfig(ctx, 1, bindingID, "persona", nil, nil, nil); err != nil {
		t.Fatalf("omitted connection: %v", err)
	}
	b, err := st.Bindings.Get(ctx, bindingID)
	if err != nil {
		t.Fatalf("reload binding: %v", err)
	}
	if b.LLMConnectionID == nil || *b.LLMConnectionID != oldLLM {
		t.Fatalf("omitted connection changed: %v", b.LLMConnectionID)
	}

	unknown := 4242
	err = st.BS.UpdateAgentConfig(ctx, 1, bindingID, "new persona", nil, nil, &unknown)
	if !errors.Is(err, ErrLLMConnectionInvalid) {
		t.Fatalf("unknown connection: got %v, want ErrLLMConnectionInvalid", err)
	}
	b, err = st.Bindings.Get(ctx, bindingID)
	if err != nil {
		t.Fatalf("reload after rejected update: %v", err)
	}
	if b.Instructions != "persona" {
		t.Fatalf("rejected update wrote instructions: %q", b.Instructions)
	}
}
