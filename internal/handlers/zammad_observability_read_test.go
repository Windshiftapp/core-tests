//go:build test

package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/logger"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func TestZammadObservabilityReadsRequireViewAndBoundLimit(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	seed := tdb.SeedTestData(t)
	if _, err := tdb.Exec(`INSERT INTO users (id, email, username, first_name, last_name, is_active)
        VALUES (2, 'outsider@example.test', 'outsider', 'Outside', 'User', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.Exec(`INSERT INTO items
        (id, workspace_id, workspace_item_number, title, description, frac_index, status_id, creator_id, last_active_at)
        VALUES (1, 1, 1, 'Observed item', '', 'a0', ?, 1, CURRENT_TIMESTAMP)`, seed.StatusID); err != nil {
		t.Fatal(err)
	}
	// An explicit Viewer assignment closes the implicit Everyone view grant.
	if _, err := tdb.Exec(`INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		SELECT 1, 1, id, CURRENT_TIMESTAMP FROM workspace_roles WHERE name = 'Viewer'`); err != nil {
		t.Fatal(err)
	}
	permission, _, _ := createTestServices(t, *tdb)
	zammadRepo := repository.NewZammadRepository(tdb.GetDatabase())
	handler := NewZammadHandler(
		repository.NewItemRepository(tdb.GetDatabase()),
		services.NewZammadService(tdb.GetDatabase(), zammadRepo, nil, nil, nil, nil, nil),
		permission,
		logger.NewAuditor(tdb.GetDatabase()),
	)

	for _, endpoint := range []struct {
		name, path, param string
		serve             http.HandlerFunc
	}{
		{"workspace overview", "/api/workspaces/1/zammad-overview", "workspaceId", handler.GetWorkspaceOverview},
		{"item history", "/api/items/1/zammad-history", "id", handler.GetItemHistory},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			call := func(userID int, limit string) int {
				t.Helper()
				url := endpoint.path
				if limit != "" {
					url += "?limit=" + limit
				}
				req := httptest.NewRequest(http.MethodGet, url, nil)
				req.SetPathValue(endpoint.param, "1")
				rec := httptest.NewRecorder()
				endpoint.serve(rec, testutils.WithAuthContext(req, testutils.TestUserWithID(userID)))
				return rec.Code
			}
			if got := call(2, "invalid"); got != http.StatusNotFound {
				t.Fatalf("outsider must receive 404 before limit parsing; got %d", got)
			}
			for _, value := range []string{"0", "101", "invalid"} {
				if got := call(1, value); got != http.StatusBadRequest {
					t.Errorf("limit %q: expected 400, got %d", value, got)
				}
			}
			for _, value := range []string{"", "1", "100"} {
				if got := call(1, value); got != http.StatusOK {
					t.Errorf("limit %q: expected 200, got %d", value, got)
				}
			}
		})
	}
}
