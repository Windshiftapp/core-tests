package services

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/models"
)

// fakeRunnerPoolLister stands in for repository.ActionRepository: it reports the
// runner_pool capability ids a workspace may target.
type fakeRunnerPoolLister struct{ ids []int }

func (f *fakeRunnerPoolLister) ListCapabilitiesForWorkspace(_ int, capType string) ([]*models.ActionCapability, error) {
	if capType != string(models.CapabilityRunnerPool) {
		return nil, nil
	}
	out := make([]*models.ActionCapability, 0, len(f.ids))
	for _, id := range f.ids {
		out = append(out, &models.ActionCapability{ID: id, CapabilityType: models.CapabilityRunnerPool})
	}
	return out, nil
}

// TestBindingService_CreateValidatesTargetPool pins the security gate: a binding
// may only target a runner pool the workspace is allowed to dispatch to. A nil
// target (local in-process) always passes; a pool not in the workspace's list
// (or no lister wired) is rejected.
func TestBindingService_CreateValidatesTargetPool(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	st.BS.pools = &fakeRunnerPoolLister{ids: []int{5}}

	llmConn := validTestLLMConnectionID
	base := func(actingUser int, pool *int) CreateBindingRequest {
		return CreateBindingRequest{
			WorkspaceID:     1,
			ActingUserID:    actingUser,
			CreatedByUserID: st.AdminID,
			LLMConnectionID: &llmConn,
			TargetPoolID:    pool,
		}
	}

	// Happy path through Create: a valid pool is accepted and persisted on the
	// binding (st.AgentID is an owned agent, so it clears the identity gate).
	pool5 := 5
	b, err := st.BS.Create(ctx, base(st.AgentID, &pool5))
	if err != nil {
		t.Fatalf("valid pool: %v", err)
	}
	if b.TargetPoolID == nil || *b.TargetPoolID != 5 {
		t.Errorf("target pool not persisted: got %v", b.TargetPoolID)
	}

	// Rejection cases hit the gate directly (the identity chokepoint runs first
	// in Create and would mask the pool check for non-owned identities).
	if err := st.BS.validateTargetPool(1, 99); !errors.Is(err, ErrBindingInvalidPool) {
		t.Errorf("foreign pool: want ErrBindingInvalidPool, got %v", err)
	}
	st.BS.pools = nil
	if err := st.BS.validateTargetPool(1, 5); !errors.Is(err, ErrBindingInvalidPool) {
		t.Errorf("no lister: want ErrBindingInvalidPool, got %v", err)
	}
}
