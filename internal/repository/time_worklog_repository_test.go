package repository

import (
	"errors"
	"path/filepath"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

func TestTimeWorklogRepositoryCookieAuthDetailsLifecycle(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "time-worklogs.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	userID := insertTestRow(t, db, `
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('worklog@example.test', 'worklog-user', 'Worklog', 'User')
	`)
	workspaceID := insertTestRow(t, db, `
		INSERT INTO workspaces (name, key) VALUES ('Worklog workspace', 'WL')
	`)
	itemID := insertTestRow(t, db, `
		INSERT INTO items (workspace_id, workspace_item_number, title, frac_index)
		VALUES (?, 7, 'Tracked item', ?)
	`, workspaceID, testutils.NextTestFracIndex())
	customerID := insertTestRow(t, db, `
		INSERT INTO customer_organisations (name) VALUES ('Worklog customer')
	`)
	projectID := insertTestRow(t, db, `
		INSERT INTO time_projects (customer_id, name, status, settings)
		VALUES (?, 'Worklog project', 'Active', '{"max_hours": 20}')
	`, customerID)

	repo := NewTimeWorklogRepository(db)
	worklogID, err := repo.Create(NewWorklog{
		ProjectID:       projectID,
		UserID:          userID,
		ItemID:          &itemID,
		Description:     "Initial description",
		DateUnix:        1000,
		StartTimeUnix:   1100,
		EndTimeUnix:     4700,
		DurationMinutes: 60,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	detail, err := repo.GetDetail(int(worklogID))
	if err != nil {
		t.Fatalf("GetDetail: %v", err)
	}
	if detail.UserID == nil || *detail.UserID != userID || detail.UserName != "Worklog User" {
		t.Fatalf("user details = id %v, name %q", detail.UserID, detail.UserName)
	}
	if detail.ItemID == nil || *detail.ItemID != itemID || detail.WorkspaceID == nil || *detail.WorkspaceID != workspaceID || detail.WorkspaceKey != "WL" {
		t.Fatalf("item details = %+v", detail)
	}
	if detail.ProjectMaxHours == nil || *detail.ProjectMaxHours != 20 || detail.ProjectTotalHours == nil || *detail.ProjectTotalHours != 1 {
		t.Fatalf("project totals = max %v, total %v", detail.ProjectMaxHours, detail.ProjectTotalHours)
	}
	if detail.CustomerID != customerID || detail.CustomerName != "Worklog customer" {
		t.Fatalf("derived customer = id %d, name %q, want %d/Worklog customer", detail.CustomerID, detail.CustomerName, customerID)
	}

	listed, err := repo.ListDetails(WorklogDetailFilter{
		AccessibleProjectIDs: []int{projectID},
		ProjectID:            &projectID,
		ItemID:               &itemID,
	})
	if err != nil {
		t.Fatalf("ListDetails: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != int(worklogID) {
		t.Fatalf("ListDetails = %+v", listed)
	}

	secondID, err := repo.Create(NewWorklog{
		ProjectID: projectID, UserID: userID,
		Description: "Exclusive boundary", DateUnix: 2000, StartTimeUnix: 2100,
		EndTimeUnix: 5700, DurationMinutes: 60,
	})
	if err != nil {
		t.Fatalf("create boundary worklog: %v", err)
	}
	from, endExclusive := int64(1000), int64(2000)
	listed, err = repo.ListDetails(WorklogDetailFilter{
		AccessibleProjectIDs: []int{projectID}, DateFromUnix: &from, DateToExclusiveUnix: &endExclusive,
	})
	if err != nil {
		t.Fatalf("ListDetails bounded: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != int(worklogID) {
		t.Fatalf("exclusive ListDetails = %+v, want only worklog %d", listed, worklogID)
	}
	page, total, err := repo.ListDetailsPage(WorklogDetailFilter{
		AccessibleProjectIDs: []int{projectID}, Limit: 1, Offset: 1,
	})
	if err != nil {
		t.Fatalf("ListDetailsPage: %v", err)
	}
	if total != 2 || len(page) != 1 || page[0].ID != int(worklogID) {
		t.Fatalf("ListDetailsPage = total %d rows %+v, want older worklog %d", total, page, worklogID)
	}
	forUser, total, err := repo.ListForUser(WorklogListFilter{
		UserID: userID, DateFromUnix: &from, DateToExclusiveUnix: &endExclusive, Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListForUser bounded: %v", err)
	}
	if total != 1 || len(forUser) != 1 || forUser[0].ID != int(worklogID) {
		t.Fatalf("exclusive ListForUser = total %d rows %+v", total, forUser)
	}
	t.Cleanup(func() { _ = repo.Delete(int(secondID)) })

	if err := repo.Update(UpdateWorklog{
		ID:              int(worklogID),
		ProjectID:       projectID,
		ItemID:          &itemID,
		Description:     "Updated description",
		DateUnix:        2000,
		StartTimeUnix:   2100,
		EndTimeUnix:     3900,
		DurationMinutes: 30,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, err := repo.GetDetail(int(worklogID))
	if err != nil {
		t.Fatalf("GetDetail after update: %v", err)
	}
	if updated.Description != "Updated description" || updated.DurationMins != 30 {
		t.Fatalf("updated worklog = %+v", updated)
	}

	if err := repo.Delete(int(worklogID)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetDetail(int(worklogID)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetDetail after delete error = %v, want ErrNotFound", err)
	}
}

// TestTimeWorklogSurvivesCustomerOrganisationDelete reproduces GitHub #403: a
// worklog must not be deleted when its project is unlinked from an organisation
// and that organisation is then deleted. Worklogs derive their customer from
// the project, so the organisation is no longer a worklog parent.
func TestTimeWorklogSurvivesCustomerOrganisationDelete(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "worklog-org-delete.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	userID := insertTestRow(t, db, `
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('org-delete@example.test', 'org-delete', 'Org', 'Delete')
	`)
	orgID := insertTestRow(t, db, `
		INSERT INTO customer_organisations (name) VALUES ('Doomed organisation')
	`)
	projectID := insertTestRow(t, db, `
		INSERT INTO time_projects (customer_id, name, status)
		VALUES (?, 'Unlinked project', 'Active')
	`, orgID)
	var noCustomerID int
	if err := db.QueryRow(`SELECT id FROM customer_organisations WHERE builtin_key = 'no_customer'`).Scan(&noCustomerID); err != nil {
		t.Fatalf("load no-customer organisation: %v", err)
	}

	repo := NewTimeWorklogRepository(db)
	worklogID, err := repo.Create(NewWorklog{
		ProjectID:       projectID,
		UserID:          userID,
		Description:     "Billable time",
		DateUnix:        1000,
		StartTimeUnix:   1100,
		EndTimeUnix:     4700,
		DurationMinutes: 60,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Unlink the project from the organisation, then delete the organisation.
	if _, err := db.ExecWrite("UPDATE time_projects SET customer_id = ? WHERE id = ?", noCustomerID, projectID); err != nil {
		t.Fatalf("unlink project: %v", err)
	}
	if _, err := db.ExecWrite("DELETE FROM customer_organisations WHERE id = ?", orgID); err != nil {
		t.Fatalf("delete organisation: %v", err)
	}

	detail, err := repo.GetDetail(int(worklogID))
	if err != nil {
		t.Fatalf("GetDetail after organisation delete: %v", err)
	}
	if detail.CustomerID != noCustomerID {
		t.Fatalf("derived customer = %d, want no-customer %d", detail.CustomerID, noCustomerID)
	}
}
