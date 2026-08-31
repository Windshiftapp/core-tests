package scm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
)

// captureEmitter is the test double for ActionEventEmitter. Records
// every event in arrival order so assertions can inspect both the count
// and the payload.
type captureEmitter struct {
	mu     sync.Mutex
	events []*models.ActionEvent
}

type failingDurableActionRecorder struct{}

func (failingDurableActionRecorder) EmitActionEventInTx(context.Context, database.Tx, *models.ActionEvent) error {
	return errors.New("durable admission failed")
}

func (c *captureEmitter) EmitActionEvent(e *models.ActionEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *captureEmitter) snapshot() []*models.ActionEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*models.ActionEvent, len(c.events))
	copy(out, c.events)
	return out
}

// newSyncRefsTestService spins up an in-memory SQLite with just enough
// schema for the ref-sync paths: workspace_repositories carries the
// milestone glob columns, scm_processed_refs is the idempotency ledger.
// Also pre-inserts repo id 1 mapped to repo "owner/repo" with both
// patterns at their schema defaults — tests override patterns via UPDATE
// when they need to.
func newSyncRefsTestService(t *testing.T) (*SyncService, *captureEmitter) {
	t.Helper()
	dsn := fmt.Sprintf("file:syncrefs-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE workspace_repositories (
			id INTEGER PRIMARY KEY,
			workspace_scm_connection_id INTEGER NOT NULL DEFAULT 0,
			repository_external_id TEXT DEFAULT '',
			repository_name TEXT DEFAULT '',
			repository_url TEXT DEFAULT '',
			default_branch TEXT DEFAULT 'main',
			is_active INTEGER DEFAULT 1,
			last_synced_at DATETIME,
			milestone_tag_pattern TEXT NOT NULL DEFAULT 'v*',
			milestone_branch_pattern TEXT NOT NULL DEFAULT 'release/*',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE scm_processed_refs (
			workspace_repository_id INTEGER NOT NULL,
			ref_type TEXT NOT NULL,
			ref_name TEXT NOT NULL,
			sha TEXT,
			processed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (workspace_repository_id, ref_type, ref_name)
		)`,
		`INSERT INTO workspace_repositories(id, repository_name) VALUES (1, 'owner/repo')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}

	emitter := &captureEmitter{}
	svc := &SyncService{db: db}
	svc.SetActionEvents(emitter)
	return svc, emitter
}

// errorRefProvider injects an error from ListTags so we can verify
// "fail-and-don't-mark-processed" behaviour.
type errorRefProvider struct {
	fakeProvider
	listTagsErr error
}

func (e *errorRefProvider) ListTags(_ context.Context, _, _ string, _ time.Time) ([]Tag, error) {
	return nil, e.listTagsErr
}

func TestRefShort(t *testing.T) {
	cases := []struct {
		refType, refName, want string
	}{
		{"tag", "v2.0", "2.0"},
		{"tag", "V2.0", "2.0"},
		{"tag", "v2.0-rc1", "2.0-rc1"},
		{"tag", "release-v2.0", "release-v2.0"}, // no leading "v<digit>"
		{"tag", "vendor", "vendor"},             // not "v<digit>"
		{"branch", "release/2.0", "2.0"},
		{"branch", "release/v2.0-rc1", "v2.0-rc1"}, // leading 'v' on branch is preserved
		{"branch", "main", "main"},
		{"unknown", "anything", "anything"},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/%s", c.refType, c.refName), func(t *testing.T) {
			if got := refShort(c.refType, c.refName); got != c.want {
				t.Fatalf("refShort(%q, %q) = %q, want %q", c.refType, c.refName, got, c.want)
			}
		})
	}
}

func TestSCMRefLedgerRollsBackWhenDurableAdmissionFails(t *testing.T) {
	svc, emitter := newSyncRefsTestService(t)
	svc.SetDurableActionEvents(failingDurableActionRecorder{})
	provider := &fakeProvider{tags: []Tag{{Name: "v0.8.8", SHA: "abc123", CreatedAt: time.Now()}}}

	if err := svc.syncTagsAndReleases(context.Background(), provider, "owner", "repo", 1, 99, false); err == nil {
		t.Fatal("syncTagsAndReleases() succeeded with failed durable admission")
	}
	var processed int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM scm_processed_refs").Scan(&processed); err != nil {
		t.Fatalf("count processed refs: %v", err)
	}
	if processed != 0 {
		t.Fatalf("processed refs = %d, want rollback to zero", processed)
	}
	if got := len(emitter.snapshot()); got != 0 {
		t.Fatalf("legacy events = %d, want none when durable recorder is installed", got)
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"v*", "v1.2.3", true},
		{"v*", "staging-1", false},
		{"release/*", "release/2.0", true},
		{"release/*", "main", false},
		{"", "v1.0", false}, // empty pattern never matches
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/%s", c.pattern, c.name), func(t *testing.T) {
			if got := matchGlob(c.pattern, c.name); got != c.want {
				t.Fatalf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
			}
		})
	}
}

func TestSyncReleaseBranches_EmitsEvent_Once(t *testing.T) {
	svc, emitter := newSyncRefsTestService(t)
	prov := &fakeProvider{branches: []Branch{
		{Name: "release/2.0", SHA: "abc"},
		{Name: "main", SHA: "deadbeef"},
	}}

	if err := svc.syncReleaseBranches(context.Background(), prov, "owner", "repo", 1, 99, false); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := len(emitter.snapshot()); got != 1 {
		t.Fatalf("first run emitted %d events, want 1", got)
	}
	ev := emitter.snapshot()[0]
	if ev.EventType != models.ActionTriggerSCMReleaseBranchCreated {
		t.Fatalf("event type = %q, want %q", ev.EventType, models.ActionTriggerSCMReleaseBranchCreated)
	}
	if ev.WorkspaceID != 99 {
		t.Fatalf("workspace_id = %d, want 99", ev.WorkspaceID)
	}
	if got := ev.NewValues["ref.short"]; got != "2.0" {
		t.Fatalf("ref.short = %v, want \"2.0\"", got)
	}

	// Second run is a no-op via scm_processed_refs.
	if err := svc.syncReleaseBranches(context.Background(), prov, "owner", "repo", 1, 99, false); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := len(emitter.snapshot()); got != 1 {
		t.Fatalf("second run total events = %d, want 1 (idempotent)", got)
	}
}

func TestSyncTags_EmitsEvent_Once(t *testing.T) {
	svc, emitter := newSyncRefsTestService(t)
	now := time.Now()
	prov := &fakeProvider{tags: []Tag{
		{Name: "v1.0", SHA: "111", CreatedAt: now.Add(-time.Hour)},
	}}

	if err := svc.syncTagsAndReleases(context.Background(), prov, "owner", "repo", 1, 99, false); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := len(emitter.snapshot()); got != 1 {
		t.Fatalf("first run emitted %d events, want 1", got)
	}
	if err := svc.syncTagsAndReleases(context.Background(), prov, "owner", "repo", 1, 99, false); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := len(emitter.snapshot()); got != 1 {
		t.Fatalf("second run total events = %d, want 1 (idempotent)", got)
	}
}

func TestSyncTags_RespectsGlob(t *testing.T) {
	svc, emitter := newSyncRefsTestService(t)
	if _, err := svc.db.Exec(`UPDATE workspace_repositories SET milestone_tag_pattern = 'v*' WHERE id = 1`); err != nil {
		t.Fatalf("update pattern: %v", err)
	}
	prov := &fakeProvider{tags: []Tag{
		{Name: "v1.0", SHA: "111", CreatedAt: time.Now()},
		{Name: "staging-snapshot-2024", SHA: "222", CreatedAt: time.Now()},
	}}

	if err := svc.syncTagsAndReleases(context.Background(), prov, "owner", "repo", 1, 99, false); err != nil {
		t.Fatalf("sync: %v", err)
	}
	events := emitter.snapshot()
	if len(events) != 1 {
		t.Fatalf("emitted %d events, want 1 (glob filtered staging-* out)", len(events))
	}
	if events[0].NewValues["ref.name"] != "v1.0" {
		t.Fatalf("kept event for ref %v, want v1.0", events[0].NewValues["ref.name"])
	}
}

func TestSyncTags_PrevTagComputation(t *testing.T) {
	svc, emitter := newSyncRefsTestService(t)
	now := time.Now()
	prov := &fakeProvider{tags: []Tag{
		// Provider returns out of order; sync must sort by CreatedAt asc.
		{Name: "v1.2", SHA: "ccc", CreatedAt: now},
		{Name: "v1.0", SHA: "aaa", CreatedAt: now.Add(-2 * time.Hour)},
		{Name: "v1.1", SHA: "bbb", CreatedAt: now.Add(-time.Hour)},
	}}

	if err := svc.syncTagsAndReleases(context.Background(), prov, "owner", "repo", 1, 99, false); err != nil {
		t.Fatalf("sync: %v", err)
	}
	events := emitter.snapshot()
	if len(events) != 3 {
		t.Fatalf("emitted %d events, want 3", len(events))
	}
	// In emission order: v1.0 (no prev), v1.1 (prev=v1.0), v1.2 (prev=v1.1).
	wantPrev := map[string]string{"v1.0": "", "v1.1": "v1.0", "v1.2": "v1.1"}
	for _, ev := range events {
		name, _ := ev.NewValues["ref.name"].(string)
		gotPrev, _ := ev.NewValues["ref.prev_name"].(string)
		if gotPrev != wantPrev[name] {
			t.Fatalf("for %s: prev_name = %q, want %q", name, gotPrev, wantPrev[name])
		}
	}
}

func TestSyncTags_ProviderError_NoMark(t *testing.T) {
	svc, emitter := newSyncRefsTestService(t)
	ep := &errorRefProvider{listTagsErr: errors.New("rate limited")}

	err := svc.syncTagsAndReleases(context.Background(), ep, "owner", "repo", 1, 99, false)
	if err == nil {
		t.Fatal("expected error from sync, got nil")
	}
	if got := len(emitter.snapshot()); got != 0 {
		t.Fatalf("emitted %d events on error, want 0", got)
	}

	// Ledger must be empty so the next tick can retry.
	var n int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM scm_processed_refs`).Scan(&n); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if n != 0 {
		t.Fatalf("scm_processed_refs has %d rows after failed sync, want 0", n)
	}
}
