package services

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func milestoneReleaseFixture(t *testing.T) (*PlanningService, int) {
	t.Helper()
	db := newPlanningScopeTestDB(t)
	milestoneID := planningScopeInsertID(t, db, `
		INSERT INTO milestones (name, description, status, is_global, workspace_id)
		VALUES ('Release candidate', '', 'in-progress', true, NULL)
	`)
	return NewPlanningService(db), milestoneID
}

func milestoneReleaseParams(milestoneID int, key string) ReleaseMilestoneParams {
	return ReleaseMilestoneParams{
		ID:             milestoneID,
		IdempotencyKey: key,
		TagName:        "v0.8.4",
		Name:           "0.8.4",
	}
}

func TestBeginMilestoneReleasePersistsBeforeRemoteWork(t *testing.T) {
	service, milestoneID := milestoneReleaseFixture(t)
	params := milestoneReleaseParams(milestoneID, "request-1")

	attempt, err := service.BeginMilestoneRelease(context.Background(), params)
	if err != nil {
		t.Fatalf("BeginMilestoneRelease: %v", err)
	}

	var state, leaseToken string
	if err := service.db.QueryRow(`
		SELECT state, lease_token FROM milestone_releases WHERE id = ?
	`, attempt.ID).Scan(&state, &leaseToken); err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if state != "pending" || leaseToken != attempt.LeaseToken {
		t.Fatalf("attempt state/token = %q/%q, want pending/%q", state, leaseToken, attempt.LeaseToken)
	}
}

func TestBeginMilestoneReleaseInsertFailureHasNoPartialState(t *testing.T) {
	service, milestoneID := milestoneReleaseFixture(t)
	if _, err := service.db.ExecWrite(`
		CREATE TRIGGER reject_release_attempt
		BEFORE INSERT ON milestone_releases
		BEGIN
			SELECT RAISE(ABORT, 'insert rejected');
		END
	`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	if _, err := service.BeginMilestoneRelease(
		context.Background(),
		milestoneReleaseParams(milestoneID, "request-insert-failure"),
	); err == nil {
		t.Fatal("BeginMilestoneRelease succeeded, want insert failure")
	}

	var releases int
	if err := service.db.QueryRow(`SELECT COUNT(*) FROM milestone_releases`).Scan(&releases); err != nil {
		t.Fatalf("count releases: %v", err)
	}
	if releases != 0 {
		t.Fatalf("release attempts = %d, want 0", releases)
	}
}

func TestMilestoneReleaseRetryReconcilesAfterUncertainProviderResult(t *testing.T) {
	service, milestoneID := milestoneReleaseFixture(t)
	params := milestoneReleaseParams(milestoneID, "request-timeout")
	first, err := service.BeginMilestoneRelease(context.Background(), params)
	if err != nil {
		t.Fatalf("begin first attempt: %v", err)
	}
	if err := service.MarkMilestoneReleaseUncertain(
		context.Background(), first.ID, first.LeaseToken, "provider timeout",
	); err != nil {
		t.Fatalf("MarkMilestoneReleaseUncertain: %v", err)
	}

	retry, err := service.BeginMilestoneRelease(context.Background(), params)
	if err != nil {
		t.Fatalf("begin retry: %v", err)
	}
	if !retry.NeedsReconcile {
		t.Fatal("retry NeedsReconcile = false, want true")
	}

	releaseID := "remote-42"
	releaseURL := "https://scm.example/releases/42"
	params.SCMReleaseID = &releaseID
	params.SCMReleaseURL = &releaseURL
	milestone, err := service.CompleteMilestoneRelease(
		context.Background(), retry.ID, retry.LeaseToken, params,
	)
	if err != nil {
		t.Fatalf("CompleteMilestoneRelease: %v", err)
	}
	if milestone.Status != "completed" {
		t.Fatalf("milestone status = %q, want completed", milestone.Status)
	}

	done, err := service.BeginMilestoneRelease(context.Background(), params)
	if err != nil {
		t.Fatalf("begin completed retry: %v", err)
	}
	if !done.AlreadyCreated {
		t.Fatal("completed retry AlreadyCreated = false, want true")
	}
}

func TestMilestoneReleaseRetryRejectsChangedRequestForSameIdempotencyKey(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ReleaseMilestoneParams)
	}{
		{name: "tag", mutate: func(p *ReleaseMilestoneParams) { p.TagName = "v0.8.4-different" }},
		{name: "name", mutate: func(p *ReleaseMilestoneParams) { p.Name = "different name" }},
		{name: "body", mutate: func(p *ReleaseMilestoneParams) { p.Body = "different body" }},
		{name: "draft flag", mutate: func(p *ReleaseMilestoneParams) { p.IsDraft = true }},
		{name: "prerelease flag", mutate: func(p *ReleaseMilestoneParams) { p.IsPrerelease = true }},
		{name: "target commit", mutate: func(p *ReleaseMilestoneParams) { p.TargetCommitish = "different-sha" }},
		{name: "SCM connection", mutate: func(p *ReleaseMilestoneParams) {
			value := 42
			p.SCMConnectionID = &value
		}},
		{name: "SCM repository", mutate: func(p *ReleaseMilestoneParams) {
			value := "owner/different"
			p.SCMRepository = &value
		}},
		{name: "creator", mutate: func(p *ReleaseMilestoneParams) {
			value := 42
			p.CreatedBy = &value
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, milestoneID := milestoneReleaseFixture(t)
			original := milestoneReleaseParams(milestoneID, "request-payload-drift")
			original.Body = "release notes"
			original.TargetCommitish = "main"

			first, err := service.BeginMilestoneRelease(context.Background(), original)
			if err != nil {
				t.Fatalf("begin original attempt: %v", err)
			}
			if err := service.MarkMilestoneReleaseUncertain(
				context.Background(), first.ID, first.LeaseToken, "provider timeout",
			); err != nil {
				t.Fatalf("mark original attempt uncertain: %v", err)
			}

			changed := original
			tt.mutate(&changed)
			if _, err := service.BeginMilestoneRelease(context.Background(), changed); !errors.Is(err, ErrMilestoneReleaseIdempotencyConflict) {
				t.Fatalf("changed request error = %v, want ErrMilestoneReleaseIdempotencyConflict", err)
			}

			var tagName, state string
			if err := service.db.QueryRow(`
				SELECT tag_name, state FROM milestone_releases WHERE id = ?
			`, first.ID).Scan(&tagName, &state); err != nil {
				t.Fatalf("load durable release attempt: %v", err)
			}
			if tagName != original.TagName || state != "reconciliation-required" {
				t.Fatalf(
					"durable attempt changed to tag/state %q/%q, want %q/reconciliation-required",
					tagName, state, original.TagName,
				)
			}
		})
	}
}

func TestCompleteMilestoneReleaseRollsBackWhenStatusUpdateFails(t *testing.T) {
	service, milestoneID := milestoneReleaseFixture(t)
	params := milestoneReleaseParams(milestoneID, "request-status-failure")
	attempt, err := service.BeginMilestoneRelease(context.Background(), params)
	if err != nil {
		t.Fatalf("BeginMilestoneRelease: %v", err)
	}
	if _, err := service.db.ExecWrite(`
		CREATE TRIGGER reject_release_status
		BEFORE UPDATE OF status ON milestones
		BEGIN
			SELECT RAISE(ABORT, 'status rejected');
		END
	`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	if _, err := service.CompleteMilestoneRelease(
		context.Background(), attempt.ID, attempt.LeaseToken, params,
	); err == nil {
		t.Fatal("CompleteMilestoneRelease succeeded, want status failure")
	}

	var releaseState, milestoneStatus string
	if err := service.db.QueryRow(`
		SELECT state FROM milestone_releases WHERE id = ?
	`, attempt.ID).Scan(&releaseState); err != nil {
		t.Fatalf("load release state: %v", err)
	}
	if err := service.db.QueryRow(`
		SELECT status FROM milestones WHERE id = ?
	`, milestoneID).Scan(&milestoneStatus); err != nil {
		t.Fatalf("load milestone status: %v", err)
	}
	if releaseState != "pending" || milestoneStatus != "in-progress" {
		t.Fatalf("release/milestone state = %q/%q, want pending/in-progress", releaseState, milestoneStatus)
	}
}

func TestConcurrentMilestoneReleaseRetriesHaveOneOwner(t *testing.T) {
	service, milestoneID := milestoneReleaseFixture(t)
	params := milestoneReleaseParams(milestoneID, "request-concurrent")

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := service.BeginMilestoneRelease(context.Background(), params)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var owners, rejected int
	for err := range results {
		switch {
		case err == nil:
			owners++
		case errors.Is(err, ErrMilestoneReleaseInProgress):
			rejected++
		default:
			t.Fatalf("unexpected retry error: %v", err)
		}
	}
	if owners != 1 || rejected != 1 {
		t.Fatalf("owners/rejected = %d/%d, want 1/1", owners, rejected)
	}
}
