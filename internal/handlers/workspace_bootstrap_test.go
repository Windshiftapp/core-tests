package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func TestWorkspaceBootstrapRequiresAuthentication(t *testing.T) {
	handler := NewWorkspaceBootstrapHandler(&WorkspaceHandler{}, nil, nil, nil, nil)
	recorder := httptest.NewRecorder()

	handler.Get(recorder, httptest.NewRequest(http.MethodGet, "/api/workspaces/1/bootstrap", nil))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

// TestWorkspaceBootstrapPlanningCompleteness pins the lookahead boundary: a
// family is complete only when no row exists past the returned cap. Exactly
// planningListLimit rows is complete; one extra row makes it partial while the
// response keeps the first planningListLimit rows.
func TestWorkspaceBootstrapPlanningCompleteness(t *testing.T) {
	cases := []struct {
		name         string
		rows         int
		wantCount    int
		wantComplete bool
	}{
		{name: "empty", rows: 0, wantCount: 0, wantComplete: true},
		{name: "below_limit", rows: 999, wantCount: 999, wantComplete: true},
		{name: "exactly_limit", rows: 1000, wantCount: 1000, wantComplete: true},
		{name: "over_limit", rows: 1001, wantCount: 1000, wantComplete: false},
	}

	for _, family := range []string{"milestones", "iterations"} {
		for _, tc := range cases {
			t.Run(family+"/"+tc.name, func(t *testing.T) {
				db, workspaceID := newBootstrapPlanningDB(t)
				insertBootstrapPlanningRows(t, db, workspaceID, family, tc.rows)
				handler := NewWorkspaceBootstrapHandler(nil, nil, services.NewPlanningService(db), nil, nil)

				var got int
				var complete bool
				var err error
				switch family {
				case "milestones":
					var items []models.Milestone
					items, complete, err = handler.listMilestones(1, workspaceID)
					got = len(items)
				case "iterations":
					var items []models.Iteration
					items, complete, err = handler.listIterations(workspaceID)
					got = len(items)
				}
				if err != nil {
					t.Fatalf("load %s: %v", family, err)
				}
				if got != tc.wantCount {
					t.Fatalf("count = %d, want %d", got, tc.wantCount)
				}
				if complete != tc.wantComplete {
					t.Fatalf("complete = %v, want %v", complete, tc.wantComplete)
				}
			})
		}
	}
}

func newBootstrapPlanningDB(t *testing.T) (database.Database, int) {
	t.Helper()
	db := testutils.CreateTestDB(t, true).GetDatabase()
	workspaceID := testutils.InsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal, created_at, updated_at)
		VALUES ('Bootstrap Test Workspace', 'BST', 'Test workspace', TRUE, FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	return db, workspaceID
}

func insertBootstrapPlanningRows(t *testing.T, db database.Database, workspaceID int, family string, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		switch family {
		case "milestones":
			testutils.InsertID(t, db, `
				INSERT INTO milestones (name, status, is_global, workspace_id, position, created_at, updated_at)
				VALUES (?, 'planning', false, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			`, fmt.Sprintf("Milestone %d", i), workspaceID, i)
		case "iterations":
			testutils.InsertID(t, db, `
				INSERT INTO iterations (name, start_date, end_date, status, is_global, workspace_id, created_at, updated_at)
				VALUES (?, '2026-01-01', '2026-01-14', 'planned', false, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			`, fmt.Sprintf("Iteration %d", i), workspaceID)
		}
	}
}

func (h *WorkspaceHandler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := RequireAuth(w, r)
	if !ok {
		return
	}
	id, ok := requireWorkspaceIDParam(w, r, h.keyCache, "id")
	if !ok {
		return
	}
	workspace, err := h.loadWorkspaceForUser(user, id)
	if errors.Is(err, repository.ErrNotFound) {
		respondNotFound(w, r, "workspace")
		return
	}
	if err != nil {
		respondInternalError(w, r, err)
		return
	}
	h.trackWorkspaceVisit(user.ID, workspace.ID)
	respondJSONOK(w, workspace)
}
