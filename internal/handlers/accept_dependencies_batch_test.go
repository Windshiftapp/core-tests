//go:build test

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"windshift/internal/contextkeys"
	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// WI-1909: AcceptDependencies used to resolve the source and target item
// workspaces with two queries per suggestion. It must now resolve every
// referenced item in one batched query.

func grantSystemAdminForBatchTest(t *testing.T, db database.Database, userID int) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO user_global_permissions (user_id, permission_id, granted_by, granted_at)
		VALUES (?, (SELECT id FROM permissions WHERE permission_key = 'system.admin'), ?, ?)
	`, userID, userID, time.Now().UTC()); err != nil {
		t.Fatalf("grant system admin: %v", err)
	}
}

func insertBatchTestItemFor(t *testing.T, db database.Database, seed testutils.TestDataSet, number int) int {
	t.Helper()
	return testutils.InsertID(t, db, `
		INSERT INTO items (
			workspace_id, workspace_item_number, title, description,
			status_id, priority_id, frac_index, created_at, updated_at
		) VALUES (?, ?, ?, '', ?, ?, ?, ?, ?)
	`, seed.WorkspaceID, number, "n+1 dependency item",
		seed.StatusID, seed.PriorityID, testutils.NextTestFracIndex(),
		time.Now().UTC(), time.Now().UTC())
}

func TestAcceptDependenciesResolvesWorkspacesInOneQuery(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	grantSystemAdminForBatchTest(t, db, seed.UserID)

	linkTypeID := testutils.InsertID(t, db,
		`INSERT INTO link_types (name, forward_label, reverse_label) VALUES (?, ?, ?)`,
		"n+1 dependency", "depends on", "blocks")

	const suggestionCount = 6
	type itemPair struct{ source, target int }
	pairs := make([]itemPair, 0, suggestionCount)
	for i := 0; i < suggestionCount; i++ {
		source := insertBatchTestItemFor(t, db, seed, i*2+1)
		target := insertBatchTestItemFor(t, db, seed, i*2+2)
		pairs = append(pairs, itemPair{source: source, target: target})
	}

	counting := testutils.CountQueries(db)
	config := services.DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	permService, err := services.NewPermissionService(counting, config)
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permService.Close() })

	handler := &AIHandler{db: counting, permService: permService}

	encoded := make([]string, 0, suggestionCount)
	for _, pair := range pairs {
		encoded = append(encoded, fmt.Sprintf(
			`{"source_item_id":%d,"target_item_id":%d,"link_type_id":%d}`,
			pair.source, pair.target, linkTypeID,
		))
	}
	body := `{"suggestions":[` + strings.Join(encoded, ",") + `]}`

	req := httptest.NewRequest(http.MethodPost, "/api/ai/items/1/dependencies/accept", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	req = req.WithContext(context.WithValue(req.Context(), contextkeys.User, &models.User{ID: seed.UserID}))
	recorder := httptest.NewRecorder()

	counting.ResetQueries()
	handler.AcceptDependencies(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var resp AcceptDependenciesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, recorder.Body.String())
	}
	if resp.Created != suggestionCount || resp.Skipped != 0 {
		t.Fatalf("created=%d skipped=%d, want created=%d skipped=0", resp.Created, resp.Skipped, suggestionCount)
	}

	if got := counting.CountQueriesContaining("SELECT workspace_id FROM items WHERE id = ?"); got != 0 {
		t.Fatalf("handler still issues %d per-item workspace lookups", got)
	}
	if got := counting.CountQueriesContaining("SELECT id, workspace_id FROM items WHERE id IN ("); got != 1 {
		t.Fatalf("batched workspace lookup ran %d times, want 1", got)
	}
}
