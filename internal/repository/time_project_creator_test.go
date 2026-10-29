//go:build test

package repository

import (
	"testing"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestTimeProjectCreatorAssignmentFailureRollsBackProject(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	project := models.TimeProject{Name: "Atomic creator", Status: "Active"}
	// A nonexistent grantor violates the production foreign key after project insertion.
	err := NewTimeProjectRepository(db).CreateWithManager(&project, 987654321)
	if err == nil {
		t.Fatal("expected creator assignment foreign-key failure")
	}
	var projects, managers int
	if err := db.QueryRow("SELECT COUNT(*) FROM time_projects WHERE name = ?", project.Name).Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM time_project_managers WHERE granted_by = ?", 987654321).Scan(&managers); err != nil {
		t.Fatal(err)
	}
	if projects != 0 || managers != 0 || project.ID != 0 {
		t.Fatalf("failed create persisted projects=%d managers=%d id=%d", projects, managers, project.ID)
	}
}
