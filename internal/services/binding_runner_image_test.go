//go:build test

package services

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/models"
)

func TestValidateRunnerImage(t *testing.T) {
	t.Parallel()
	valid := []string{
		"",       // empty = use default
		"alpine", // bare name
		"ghcr.io/windshiftapp/windshift-agent:1.2.3",
		"ghcr.io/acme/playwright:v0.8.2",
		"registry.example.com:5000/team/img:tag",
		"node@sha256:" + "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	}
	for _, img := range valid {
		got, err := validateRunnerImage("  " + img + "  ") // also asserts trimming
		if err != nil {
			t.Errorf("validateRunnerImage(%q) unexpected error: %v", img, err)
		}
		if got != img {
			t.Errorf("validateRunnerImage(%q) = %q, want trimmed %q", img, got, img)
		}
	}

	invalid := []string{
		"has space",
		"Bad!!image",
		"UPPER/Repo", // registry path must be lowercase
		"img:tag with space",
		"img;rm -rf", // shell metacharacter
		"img\ttab",
	}
	for _, img := range invalid {
		if _, err := validateRunnerImage(img); !errors.Is(err, ErrBindingInvalidRunnerImage) {
			t.Errorf("validateRunnerImage(%q) err = %v, want ErrBindingInvalidRunnerImage", img, err)
		}
	}
}

func TestBindingService_Create_RunnerImageRequiresPool(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	llm := validTestLLMConnectionID

	_, err := st.BS.Create(ctx, CreateBindingRequest{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		CreatedByUserID: st.AdminID,
		LLMConnectionID: &llm,
		TokenScopes:     []string{"items:read"},
		RunnerImage:     "ghcr.io/acme/playwright:1", // no TargetPoolID
	})
	if !errors.Is(err, ErrBindingRunnerImageRequiresPool) {
		t.Fatalf("err = %v, want ErrBindingRunnerImageRequiresPool", err)
	}
}

func TestBindingService_Create_RunnerImageRejectsBadRef(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	llm := validTestLLMConnectionID

	_, err := st.BS.Create(ctx, CreateBindingRequest{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		CreatedByUserID: st.AdminID,
		LLMConnectionID: &llm,
		TokenScopes:     []string{"items:read"},
		RunnerImage:     "not a valid image!!",
	})
	if !errors.Is(err, ErrBindingInvalidRunnerImage) {
		t.Fatalf("err = %v, want ErrBindingInvalidRunnerImage", err)
	}
}

// UpdateAgentConfig can set and clear the runner image on a pool binding, and
// rejects an image on a non-pool binding (WI-450).
func TestBindingService_UpdateAgentConfig_RunnerImage(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)

	poolID := 4
	poolBinding, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID: 1, ActingUserID: st.AgentID, ActingUserKind: ActingIdentityKindAgent,
		TargetPoolID: &poolID, TokenScopes: []string{"items:read"}, CreatedByUserID: st.AdminID,
	})
	if err != nil {
		t.Fatalf("seed pool binding: %v", err)
	}

	sp := func(s string) *string { return &s }

	// Set an image.
	if err := st.BS.UpdateAgentConfig(ctx, 1, poolBinding, "", sp("ghcr.io/acme/playwright:1"), nil); err != nil {
		t.Fatalf("set image: %v", err)
	}
	if b, _ := st.Bindings.Get(ctx, poolBinding); b.RunnerImage != "ghcr.io/acme/playwright:1" {
		t.Fatalf("after set: RunnerImage = %q", b.RunnerImage)
	}
	// Presence-aware (WI-450 P2): a nil runner image leaves the current value
	// untouched, so a client omitting the key never clears it.
	if err := st.BS.UpdateAgentConfig(ctx, 1, poolBinding, "new instructions", nil, nil); err != nil {
		t.Fatalf("update without image: %v", err)
	}
	if b, _ := st.Bindings.Get(ctx, poolBinding); b.RunnerImage != "ghcr.io/acme/playwright:1" {
		t.Fatalf("omitted image must preserve existing; got %q", b.RunnerImage)
	}
	// An explicit empty string clears it.
	if err := st.BS.UpdateAgentConfig(ctx, 1, poolBinding, "", sp(""), nil); err != nil {
		t.Fatalf("clear image: %v", err)
	}
	if b, _ := st.Bindings.Get(ctx, poolBinding); b.RunnerImage != "" {
		t.Fatalf("after clear: RunnerImage = %q, want empty", b.RunnerImage)
	}
	// Bad ref → typed error.
	if err := st.BS.UpdateAgentConfig(ctx, 1, poolBinding, "", sp("bad image!!"), nil); !errors.Is(err, ErrBindingInvalidRunnerImage) {
		t.Errorf("bad ref: want ErrBindingInvalidRunnerImage, got %v", err)
	}

	// A non-pool binding rejects any image.
	localBinding, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID: 1, ActingUserID: st.SvcUserID, ActingUserKind: ActingIdentityKindAgent,
		TokenScopes: []string{"items:read"}, CreatedByUserID: st.AdminID,
	})
	if err != nil {
		t.Fatalf("seed local binding: %v", err)
	}
	if err := st.BS.UpdateAgentConfig(ctx, 1, localBinding, "", sp("ghcr.io/acme/playwright:1"), nil); !errors.Is(err, ErrBindingRunnerImageRequiresPool) {
		t.Errorf("local binding image: want ErrBindingRunnerImageRequiresPool, got %v", err)
	}
}

// A pool binding's runner_image rides through to the queued run's job_image, so
// the remote runner launches the custom coding-agent image (WI-450). The local
// in-process path leaves job_image empty (custom images are remote-only).
func TestBindingService_PoolRun_CarriesRunnerImage(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)

	poolID := 7
	const image = "ghcr.io/acme/playwright:1"
	bindingID, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		ActingUserKind:  ActingIdentityKindAgent,
		TargetPoolID:    &poolID,
		RunnerImage:     image,
		TokenScopes:     []string{"items:read"},
		TokenTTLMinutes: 15,
		CreatedByUserID: st.AdminID,
	})
	if err != nil {
		t.Fatalf("seed pool binding: %v", err)
	}
	if got, err := st.Bindings.Get(ctx, bindingID); err != nil || got.RunnerImage != image {
		t.Fatalf("binding round-trip RunnerImage = %q (err %v), want %q", got.RunnerImage, err, image)
	}

	itemID := seedItem(t, st.DB, 1)
	if err := st.BS.MaybeStartRunForAssignee(ctx, 1, itemID, nil, &st.AgentID, st.AdminID); err != nil {
		t.Fatalf("assignee trigger: %v", err)
	}

	var jobImage string
	if err := st.DB.QueryRow(
		`SELECT COALESCE(job_image, '') FROM agent_runs WHERE item_id = ? ORDER BY id DESC LIMIT 1`, itemID,
	).Scan(&jobImage); err != nil {
		t.Fatalf("read run job_image: %v", err)
	}
	if jobImage != image {
		t.Fatalf("queued pool run job_image = %q, want %q", jobImage, image)
	}
}
