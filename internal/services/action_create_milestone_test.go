package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

// fakeCommitAttacher records its inputs and returns a canned result so
// tests can assert what the executor passed without standing up a real
// SCM provider or the ItemKeyDetector chain.
type fakeCommitAttacher struct {
	calls  []MilestoneCommitAttachInput
	result MilestoneCommitAttachResult
	err    error
}

func (f *fakeCommitAttacher) AttachCommitIssues(_ context.Context, in MilestoneCommitAttachInput) (MilestoneCommitAttachResult, error) {
	f.calls = append(f.calls, in)
	return f.result, f.err
}

// stubNodeAPI is the minimal NodeAPI used by executor tests. Substitute
// here uses the real ActionService implementation by delegating to a
// zero-value service — substitution doesn't touch service state, so a
// zero-value works without spinning up the engine.
type stubNodeAPI struct {
	emitted []*models.ActionEvent
}

func (s *stubNodeAPI) SubstituteVariables(template string, ctx *models.ExecutionContext) string {
	return (&ActionService{}).substituteVariables(template, ctx)
}
func (s *stubNodeAPI) EmitActionEvent(event *models.ActionEvent) {
	s.emitted = append(s.emitted, event)
}

// newCreateMilestoneTestDB builds an in-memory SQLite with just enough
// schema for the executor: workspaces (FK target), milestones (with
// external_key + partial unique index), milestone_releases. Matches the
// per-test isolation pattern used by milestone_attach_repository_test.go.
func newCreateMilestoneTestDB(t *testing.T) database.Database {
	t.Helper()
	dsn := fmt.Sprintf("file:cme-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE workspaces (id INTEGER PRIMARY KEY, name TEXT NOT NULL, key TEXT UNIQUE NOT NULL)`,
		`CREATE TABLE milestone_categories (id INTEGER PRIMARY KEY, name TEXT UNIQUE, color TEXT)`,
		`CREATE TABLE milestones (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			description TEXT,
			target_date DATE,
			status TEXT NOT NULL DEFAULT 'planning',
			category_id INTEGER,
			is_global INTEGER NOT NULL DEFAULT 1,
			workspace_id INTEGER,
			external_key TEXT,
			position INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE UNIQUE INDEX uq_milestones_workspace_external_key
			ON milestones(workspace_id, external_key) WHERE external_key IS NOT NULL`,
		`CREATE TABLE milestone_releases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			milestone_id INTEGER NOT NULL,
			state TEXT NOT NULL DEFAULT 'created',
			tag_name TEXT NOT NULL,
			name TEXT,
			body TEXT,
			is_draft INTEGER NOT NULL DEFAULT 0,
			is_prerelease INTEGER NOT NULL DEFAULT 0,
			target_commitish TEXT,
			scm_connection_id INTEGER,
			scm_repository TEXT,
			scm_release_id TEXT,
			scm_release_url TEXT,
			created_by INTEGER,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO workspaces(id, name, key) VALUES (1, 'Demo', 'DEMO')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	return db
}

// branchEvent / tagEvent build minimal events with the SCM payload the
// executor expects. Variables map mirrors the "new_" prefix the action
// engine adds to NewValues during context init.
func branchEvent(workspaceID int, short string) *models.ActionEvent {
	return &models.ActionEvent{
		EventType:   models.ActionTriggerSCMReleaseBranchCreated,
		WorkspaceID: workspaceID,
		NewValues: map[string]interface{}{
			"ref.name":                     "release/" + short,
			"ref.short":                    short,
			"ref.sha":                      "branchsha",
			"ref.type":                     "branch",
			"repo.full_name":               "octo/demo",
			"repo.workspace_repository_id": 1,
		},
	}
}

func tagEvent(workspaceID int, tagName, short, prev string) *models.ActionEvent {
	return &models.ActionEvent{
		EventType:   models.ActionTriggerSCMTagCreated,
		WorkspaceID: workspaceID,
		NewValues: map[string]interface{}{
			"ref.name":                     tagName,
			"ref.short":                    short,
			"ref.sha":                      "tagsha",
			"ref.type":                     "tag",
			"ref.prev_name":                prev,
			"repo.full_name":               "octo/demo",
			"repo.workspace_repository_id": 1,
		},
	}
}

// buildCtx assembles the ExecutionContext the executor reads, matching
// how the action engine prefixes NewValues with "new_" when populating
// ctx.Variables.
func buildCtx(event *models.ActionEvent) *models.ExecutionContext {
	vars := map[string]interface{}{
		"workspace_id": event.WorkspaceID,
	}
	for k, v := range event.NewValues {
		vars["new_"+k] = v
	}
	return &models.ExecutionContext{Event: event, Variables: vars}
}

func runExecutor(t *testing.T, exec *CreateMilestoneExecutor, cfg CreateMilestoneNodeConfig, event *models.ActionEvent) *models.StepResult {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	step := &models.StepResult{}
	node := &models.ActionNode{NodeType: models.ActionNodeCreateMilestone, NodeConfig: string(raw)}
	if err := exec.Execute(node, buildCtx(event), step); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return step
}

func TestCreateMilestone_InsertsWhenNew_BranchEvent(t *testing.T) {
	db := newCreateMilestoneTestDB(t)
	exec := NewCreateMilestoneExecutor(NewPlanningService(db), &stubNodeAPI{})

	step := runExecutor(t, exec, CreateMilestoneNodeConfig{
		NameTemplate:      "Release {{ref.short}}",
		UpsertKeyTemplate: "{{ref.short}}",
	}, branchEvent(1, "2.0"))

	if step.Output["created"] != true {
		t.Fatalf("expected created=true, got %v", step.Output)
	}
	if step.Output["external_key"] != "2.0" {
		t.Fatalf("expected external_key=2.0, got %v", step.Output["external_key"])
	}

	var (
		name, status, ek string
	)
	if err := db.QueryRow(`SELECT name, status, external_key FROM milestones WHERE workspace_id = 1 AND external_key = '2.0'`).Scan(&name, &status, &ek); err != nil {
		t.Fatalf("query inserted milestone: %v", err)
	}
	if name != "Release 2.0" {
		t.Fatalf("name = %q, want %q", name, "Release 2.0")
	}
	if status != "planning" {
		t.Fatalf("status = %q, want planning", status)
	}
	if ek != "2.0" {
		t.Fatalf("external_key = %q, want 2.0", ek)
	}
}

func TestCreateMilestone_UpsertPromotesOnTag(t *testing.T) {
	db := newCreateMilestoneTestDB(t)
	exec := NewCreateMilestoneExecutor(NewPlanningService(db), &stubNodeAPI{})

	// Branch event: creates planning milestone.
	cfg := CreateMilestoneNodeConfig{
		NameTemplate:      "Release {{ref.short}}",
		UpsertKeyTemplate: "{{ref.short}}",
	}
	runExecutor(t, exec, cfg, branchEvent(1, "2.0"))

	// Tag event: promotes same milestone to in-progress + attaches release.
	step := runExecutor(t, exec, cfg, tagEvent(1, "v2.0", "2.0", ""))
	if step.Output["created"] == true {
		t.Fatal("expected upsert (created=false) on tag event")
	}
	if step.Output["release_attached"] != true {
		t.Fatalf("expected release_attached=true, got %v", step.Output)
	}

	// Exactly one milestone, promoted, with one release row.
	var n, releases int
	if err := db.QueryRow(`SELECT COUNT(*) FROM milestones WHERE workspace_id = 1 AND external_key = '2.0'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("milestones with key 2.0 = %d, want 1", n)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM milestones WHERE workspace_id = 1 AND external_key = '2.0'`).Scan(&status); err != nil {
		t.Fatalf("status query: %v", err)
	}
	if status != "in-progress" {
		t.Fatalf("status after promotion = %q, want in-progress", status)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM milestone_releases`).Scan(&releases); err != nil {
		t.Fatalf("release count: %v", err)
	}
	if releases != 1 {
		t.Fatalf("milestone_releases rows = %d, want 1", releases)
	}
}

func TestCreateMilestone_BranchEvent_LeavesExistingMilestoneAlone(t *testing.T) {
	db := newCreateMilestoneTestDB(t)
	exec := NewCreateMilestoneExecutor(NewPlanningService(db), &stubNodeAPI{})
	cfg := CreateMilestoneNodeConfig{
		NameTemplate:      "Release {{ref.short}}",
		UpsertKeyTemplate: "{{ref.short}}",
	}

	// Seed: a milestone already in-progress (e.g. promoted by a tag earlier).
	if _, err := db.Exec(
		`INSERT INTO milestones(name, status, is_global, workspace_id, external_key) VALUES ('Release 2.0', 'in-progress', FALSE, 1, '2.0')`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Replay a branch event for the same release line — must not regress
	// the milestone back to planning.
	runExecutor(t, exec, cfg, branchEvent(1, "2.0"))

	var status string
	if err := db.QueryRow(`SELECT status FROM milestones WHERE workspace_id = 1 AND external_key = '2.0'`).Scan(&status); err != nil {
		t.Fatalf("status query: %v", err)
	}
	if status != "in-progress" {
		t.Fatalf("branch event regressed milestone to %q, expected to remain in-progress", status)
	}
}

func TestCreateMilestone_TagEvent_DirectlyInsertsWithTagStatus(t *testing.T) {
	// Project pushed a v1.0 tag without a release/* branch having
	// preceded it: executor still creates the milestone, but starts
	// it at the tag status rather than "planning".
	db := newCreateMilestoneTestDB(t)
	exec := NewCreateMilestoneExecutor(NewPlanningService(db), &stubNodeAPI{})

	step := runExecutor(t, exec, CreateMilestoneNodeConfig{
		NameTemplate:      "Release {{ref.short}}",
		UpsertKeyTemplate: "{{ref.short}}",
	}, tagEvent(1, "v1.0", "1.0", ""))

	if step.Output["created"] != true {
		t.Fatalf("expected created=true, got %v", step.Output)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM milestones WHERE workspace_id = 1 AND external_key = '1.0'`).Scan(&status); err != nil {
		t.Fatalf("status query: %v", err)
	}
	if status != "in-progress" {
		t.Fatalf("status = %q, want in-progress", status)
	}
}

func TestCreateMilestone_EmptyUpsertKey_Fails(t *testing.T) {
	db := newCreateMilestoneTestDB(t)
	exec := NewCreateMilestoneExecutor(NewPlanningService(db), &stubNodeAPI{})

	raw, _ := json.Marshal(CreateMilestoneNodeConfig{
		NameTemplate:      "irrelevant",
		UpsertKeyTemplate: "  ", // whitespace renders to empty
	})
	node := &models.ActionNode{NodeType: models.ActionNodeCreateMilestone, NodeConfig: string(raw)}
	step := &models.StepResult{}
	err := exec.Execute(node, buildCtx(branchEvent(1, "")), step)
	if err == nil {
		t.Fatal("expected error for empty upsert_key, got nil")
	}
}

func TestCreateMilestone_AttachCommitIssues_CallsAttacher(t *testing.T) {
	db := newCreateMilestoneTestDB(t)
	att := &fakeCommitAttacher{
		result: MilestoneCommitAttachResult{
			CommitsScanned:  4,
			AttachedItemIDs: []int{11, 22},
		},
	}
	exec := NewCreateMilestoneExecutor(NewPlanningService(db), &stubNodeAPI{}).WithCommitAttacher(att)

	step := runExecutor(t, exec, CreateMilestoneNodeConfig{
		NameTemplate:      "Release {{ref.short}}",
		UpsertKeyTemplate: "{{ref.short}}",
	}, tagEvent(1, "v1.2", "1.2", "v1.1"))

	if len(att.calls) != 1 {
		t.Fatalf("attacher called %d times, want 1", len(att.calls))
	}
	got := att.calls[0]
	if got.WorkspaceID != 1 || got.WorkspaceRepoID != 1 {
		t.Fatalf("attacher input ws=%d repo=%d, want 1/1", got.WorkspaceID, got.WorkspaceRepoID)
	}
	if got.BaseRef != "v1.1" || got.HeadRef != "v1.2" {
		t.Fatalf("attacher refs base=%q head=%q, want v1.1/v1.2", got.BaseRef, got.HeadRef)
	}
	if step.Output["commits_scanned"] != 4 {
		t.Fatalf("commits_scanned = %v, want 4", step.Output["commits_scanned"])
	}
	ids, _ := step.Output["attached_item_ids"].([]int)
	if len(ids) != 2 || ids[0] != 11 || ids[1] != 22 {
		t.Fatalf("attached_item_ids = %v, want [11 22]", step.Output["attached_item_ids"])
	}
}

func TestCreateMilestone_AttachCommitIssues_SkipsOnFirstTag(t *testing.T) {
	db := newCreateMilestoneTestDB(t)
	att := &fakeCommitAttacher{}
	exec := NewCreateMilestoneExecutor(NewPlanningService(db), &stubNodeAPI{}).WithCommitAttacher(att)

	// prev empty == first matching tag in repo; attacher must not be called.
	step := runExecutor(t, exec, CreateMilestoneNodeConfig{
		NameTemplate:      "Release {{ref.short}}",
		UpsertKeyTemplate: "{{ref.short}}",
	}, tagEvent(1, "v1.0", "1.0", ""))

	if len(att.calls) != 0 {
		t.Fatalf("attacher called %d times on first-tag, want 0", len(att.calls))
	}
	if step.Output["attach_commit_issues_skipped"] == nil {
		t.Fatalf("expected attach_commit_issues_skipped in output, got %v", step.Output)
	}
}

func TestCreateMilestone_AttachCommitIssues_DisabledByConfig(t *testing.T) {
	db := newCreateMilestoneTestDB(t)
	att := &fakeCommitAttacher{result: MilestoneCommitAttachResult{CommitsScanned: 7}}
	exec := NewCreateMilestoneExecutor(NewPlanningService(db), &stubNodeAPI{}).WithCommitAttacher(att)

	off := false
	runExecutor(t, exec, CreateMilestoneNodeConfig{
		NameTemplate:       "Release {{ref.short}}",
		UpsertKeyTemplate:  "{{ref.short}}",
		AttachCommitIssues: &off,
	}, tagEvent(1, "v1.2", "1.2", "v1.1"))

	if len(att.calls) != 0 {
		t.Fatalf("attacher called %d times when disabled, want 0", len(att.calls))
	}
}

func TestCreateMilestone_AttachCommitIssues_AttacherErrorIsNonFatal(t *testing.T) {
	db := newCreateMilestoneTestDB(t)
	att := &fakeCommitAttacher{err: errors.New("rate limited")}
	exec := NewCreateMilestoneExecutor(NewPlanningService(db), &stubNodeAPI{}).WithCommitAttacher(att)

	step := runExecutor(t, exec, CreateMilestoneNodeConfig{
		NameTemplate:      "Release {{ref.short}}",
		UpsertKeyTemplate: "{{ref.short}}",
	}, tagEvent(1, "v1.2", "1.2", "v1.1"))

	if step.Output["attach_commit_issues_error"] == nil {
		t.Fatalf("expected error recorded in output, got %v", step.Output)
	}
	// Milestone must still have been upserted.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM milestones WHERE workspace_id = 1 AND external_key = '1.2'`).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 1 {
		t.Fatalf("milestone not upserted despite attacher error: rows=%d", n)
	}
}

func TestCreateMilestone_AttachCommitIssues_NoAttacherRecordsSkip(t *testing.T) {
	db := newCreateMilestoneTestDB(t)
	// No .WithCommitAttacher — attacher field stays nil.
	exec := NewCreateMilestoneExecutor(NewPlanningService(db), &stubNodeAPI{})

	step := runExecutor(t, exec, CreateMilestoneNodeConfig{
		NameTemplate:      "Release {{ref.short}}",
		UpsertKeyTemplate: "{{ref.short}}",
	}, tagEvent(1, "v1.2", "1.2", "v1.1"))

	if step.Output["attach_commit_issues_skipped"] == nil {
		t.Fatalf("expected skip note when no attacher wired, got %v", step.Output)
	}
}

func TestCreateMilestone_RejectsNonSCMTriggers(t *testing.T) {
	db := newCreateMilestoneTestDB(t)
	exec := NewCreateMilestoneExecutor(NewPlanningService(db), &stubNodeAPI{})

	event := &models.ActionEvent{
		EventType:   models.ActionTriggerItemCreated,
		WorkspaceID: 1,
		NewValues:   map[string]interface{}{"ref.short": "x"},
	}
	raw, _ := json.Marshal(CreateMilestoneNodeConfig{
		NameTemplate:      "x",
		UpsertKeyTemplate: "x",
	})
	node := &models.ActionNode{NodeType: models.ActionNodeCreateMilestone, NodeConfig: string(raw)}
	step := &models.StepResult{}
	err := exec.Execute(node, buildCtx(event), step)
	if err == nil {
		t.Fatal("expected error when fired from non-SCM trigger, got nil")
	}
}
