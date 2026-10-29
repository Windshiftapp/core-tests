//go:build test

package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/llm"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type briefingHealthResponse struct {
	Since    string                `json:"since"`
	Feature  BriefingFeatureStatus `json:"feature"`
	LastRun  *models.SchedulerRun  `json:"last_run"`
	Coverage BriefingCoverage      `json:"coverage"`
	Recent   []BriefingHealthRow   `json:"recent"`
}

// briefingHealthFixture seeds four active users and one latest briefing row for
// the first three: a generated row, an empty row, and a failed row. The fourth
// user has no row, which the diagnostic must report as missing.
func briefingHealthFixture(t *testing.T) (*DiagnosticsHandler, *testutils.TestDB) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	for _, u := range []struct {
		id    int
		email string
		name  string
	}{
		{2, "second@example.com", "second"},
		{3, "third@example.com", "third"},
		{4, "fourth@example.com", "fourth"},
	} {
		if _, err := db.Exec(`INSERT INTO users (id, email, username, first_name, last_name, password_hash, is_active)
			VALUES (?, ?, ?, ?, ?, '$2a$10$hash', true)`, u.id, u.email, u.name, u.name, "User"); err != nil {
			t.Fatalf("seed user %d: %v", u.id, err)
		}
	}

	now := time.Now().UTC()
	insertBriefing := func(userID int, date, content string, errMsg any, provenance any, lockUntil any, updatedAt time.Time) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO daily_briefings (user_id, date, content, source_workspace_ids, error, lock_until, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, userID, date, content, provenance, errMsg, lockUntil, updatedAt); err != nil {
			t.Fatalf("seed briefing for user %d: %v", userID, err)
		}
	}

	// A stale failure followed by a successful generation: the newest row wins.
	insertBriefing(1, "2026-10-11", "", "old failure", nil, nil, now.Add(-48*time.Hour))
	insertBriefing(1, "2026-10-12", "briefing body", nil, "[1]", nil, now.Add(-1*time.Hour))
	insertBriefing(2, "2026-10-12", "", nil, "[1]", nil, now.Add(-2*time.Hour))
	insertBriefing(3, "2026-10-12", "", "LLM API error: status 404", "[1]", nil, now.Add(-3*time.Hour))

	if err := repository.NewSchedulerRunRepository(db).Insert(t.Context(), &models.SchedulerRun{
		SchedulerName: "briefing",
		StartedAt:     now.Add(-2 * time.Hour),
		Success:       true,
	}); err != nil {
		t.Fatalf("seed scheduler run: %v", err)
	}

	handler := &DiagnosticsHandler{
		aiRepo:           repository.NewAIRepository(db),
		schedulerRunRepo: repository.NewSchedulerRunRepository(db),
		settingsRepo:     repository.NewSystemSettingRepository(db),
		llmManager:       llm.NewConnectionManager(db, nil, nil),
	}
	return handler, tdb
}

func TestDiagnosticsBriefingHealthReportsPerUserState(t *testing.T) {
	handler, _ := briefingHealthFixture(t)

	req := httptest.NewRequest("GET", "/api/admin/diagnostics/briefing-health?since=24h", nil)
	rr := httptest.NewRecorder()
	handler.GetBriefingHealth(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp briefingHealthResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Coverage.UsersTotal != 4 {
		t.Fatalf("users_total = %d, want 4", resp.Coverage.UsersTotal)
	}
	if resp.Coverage.Generated != 1 || resp.Coverage.Empty != 1 || resp.Coverage.Failed != 1 || resp.Coverage.Missing != 1 {
		t.Fatalf("coverage = %+v, want generated/empty/failed/missing = 1/1/1/1", resp.Coverage)
	}
	if resp.Coverage.Stuck != 0 || resp.Coverage.WithoutProvenance != 0 {
		t.Fatalf("coverage = %+v, want no stuck or provenance-missing rows", resp.Coverage)
	}
	if len(resp.Recent) != 3 {
		t.Fatalf("recent rows = %d, want 3 (one per user with a row): %+v", len(resp.Recent), resp.Recent)
	}
	statusByUser := map[int]string{}
	contentByUser := map[int]int{}
	for _, row := range resp.Recent {
		statusByUser[row.UserID] = row.Status
		contentByUser[row.UserID] = row.ContentLength
	}
	if statusByUser[1] != "generated" || statusByUser[2] != "empty" || statusByUser[3] != "failed" {
		t.Fatalf("status by user = %+v", statusByUser)
	}
	if contentByUser[1] != len("briefing body") {
		t.Fatalf("user 1 content length = %d, want %d", contentByUser[1], len("briefing body"))
	}
	if resp.LastRun == nil || !resp.LastRun.Success {
		t.Fatalf("last_run = %+v, want the successful briefing tick", resp.LastRun)
	}
	if !resp.Feature.Resolved || resp.Feature.Available {
		t.Fatalf("feature = %+v, want resolved-but-unavailable with no DB connections", resp.Feature)
	}
}

func TestDiagnosticsBriefingHealthReportsFeatureConfig(t *testing.T) {
	handler, _ := briefingHealthFixture(t)
	if err := handler.settingsRepo.Upsert("ai_feature_config",
		`{"daily_briefing":{"mode":"disabled","schedule":"every_6h"}}`, "json", "", "ai"); err != nil {
		t.Fatalf("seed ai feature config: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/admin/diagnostics/briefing-health", nil)
	rr := httptest.NewRecorder()
	handler.GetBriefingHealth(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp briefingHealthResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Feature.Mode != "disabled" || resp.Feature.Schedule != "every_6h" {
		t.Fatalf("feature = %+v, want disabled/every_6h", resp.Feature)
	}
	if resp.Feature.Error == "" {
		t.Fatalf("feature error empty, want the disabled-feature error surfaced")
	}
}

func TestDiagnosticsBriefingHealthFlagsHiddenProvenanceAndHeldLease(t *testing.T) {
	handler, tdb := briefingHealthFixture(t)
	db := tdb.GetDatabase()
	now := time.Now().UTC()

	// User 4 generates content, but without provenance the read path hides it.
	if _, err := db.Exec(`INSERT INTO daily_briefings (user_id, date, content, source_workspace_ids, error, lock_until, updated_at)
		VALUES (4, '2026-10-12', 'hidden body', NULL, NULL, NULL, ?)`, now.Add(-30*time.Minute)); err != nil {
		t.Fatalf("seed provenance-less briefing: %v", err)
	}
	// User 2's existing row moves to an unexpired lease with no content yet.
	if _, err := db.Exec(`UPDATE daily_briefings
		SET content = '', source_workspace_ids = '[1]', error = NULL, lock_until = ?, updated_at = ?
		WHERE user_id = 2 AND date = '2026-10-12'`, now.Add(10*time.Minute), now.Add(-5*time.Minute)); err != nil {
		t.Fatalf("seed held lease: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/admin/diagnostics/briefing-health?since=24h", nil)
	rr := httptest.NewRecorder()
	handler.GetBriefingHealth(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp briefingHealthResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Coverage.Generated != 2 || resp.Coverage.WithoutProvenance != 1 {
		t.Fatalf("coverage = %+v, want 2 generated with 1 missing provenance", resp.Coverage)
	}
	if resp.Coverage.Stuck != 1 || resp.Coverage.Empty != 0 || resp.Coverage.Missing != 0 {
		t.Fatalf("coverage = %+v, want 1 stuck and no empty/missing", resp.Coverage)
	}
	for _, row := range resp.Recent {
		switch row.UserID {
		case 4:
			if row.Status != "generated" || row.HasProvenance {
				t.Fatalf("user 4 row = %+v, want generated without provenance", row)
			}
		case 2:
			if row.Status != "stuck" || !row.LockHeld {
				t.Fatalf("user 2 row = %+v, want stuck with a held lease", row)
			}
		}
	}
}

func TestClassifyBriefingStatus(t *testing.T) {
	cases := []struct {
		name        string
		contentLen  int
		errMsg      string
		lockHeld    bool
		wantStatus  string
	}{
		{"error wins over content", 100, "boom", false, "failed"},
		{"content wins over lease", 100, "", true, "generated"},
		{"held lease with no content", 0, "", true, "stuck"},
		{"released empty row", 0, "", false, "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyBriefingStatus(tc.contentLen, tc.errMsg, tc.lockHeld); got != tc.wantStatus {
				t.Fatalf("classifyBriefingStatus(%d, %q, %v) = %q, want %q", tc.contentLen, tc.errMsg, tc.lockHeld, got, tc.wantStatus)
			}
		})
	}
}
