//go:build test

package services_test

import (
	"testing"
	"time"

	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func createTeamService(t *testing.T, tdb *testutils.TestDB) *services.TeamService {
	t.Helper()
	teamRepo := repository.NewTeamRepository(tdb.GetDatabase())
	leaveRepo := repository.NewLeaveRepository(tdb.GetDatabase())
	return services.NewTeamService(tdb.GetDatabase(), teamRepo, leaveRepo)
}

// seedTeamWithMembers creates a team and adds direct members, returning teamID
func seedTeamWithMembers(t *testing.T, tdb *testutils.TestDB, memberIDs []int) int {
	t.Helper()
	db := tdb.GetDatabase()

	var teamID int
	err := db.QueryRow(`
		INSERT INTO teams (name, description, is_active, created_by, created_at, updated_at)
		VALUES ('Test Team', 'desc', true, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`).Scan(&teamID)
	if err != nil {
		t.Fatalf("Failed to create team: %v", err)
	}

	for _, uid := range memberIDs {
		_, err := db.Exec(`
			INSERT INTO team_members (team_id, user_id, role, added_by, added_at, created_at)
			VALUES (?, ?, 'member', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, teamID, uid)
		if err != nil {
			t.Fatalf("Failed to add member %d: %v", uid, err)
		}
	}

	return teamID
}

// seedUsers creates additional users with the given IDs
func seedUsers(t *testing.T, tdb *testutils.TestDB, ids []int) {
	t.Helper()
	db := tdb.GetDatabase()
	for _, id := range ids {
		_, err := db.Exec(`
			INSERT INTO users (id, email, username, first_name, last_name, password_hash, is_active)
			VALUES (?, ?, ?, 'User', ?, '$2a$10$hash', true) ON CONFLICT (id) DO NOTHING
		`, id, "user"+testutils.IntToString(id)+"@example.com", "user"+testutils.IntToString(id), testutils.IntToString(id))
		if err != nil {
			t.Fatalf("Failed to create user %d: %v", id, err)
		}
	}
}

// --- GetResolvedMembersForAssignment ---

func TestTeamService_GetResolvedMembersForAssignment_Basic(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	seedUsers(t, tdb, []int{2, 3})

	svc := createTeamService(t, tdb)
	teamID := seedTeamWithMembers(t, tdb, []int{1, 2, 3})

	members, err := svc.GetResolvedMembersForAssignment(teamID, false, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("Expected 3 members, got %d", len(members))
	}
	// Should be sorted by user_id
	if members[0] != 1 || members[1] != 2 || members[2] != 3 {
		t.Errorf("Expected sorted [1,2,3], got %v", members)
	}
}

func TestTeamService_GetResolvedMembersForAssignment_EmptyTeam(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)

	svc := createTeamService(t, tdb)
	teamID := seedTeamWithMembers(t, tdb, []int{})

	_, err := svc.GetResolvedMembersForAssignment(teamID, false, false)
	if err == nil {
		t.Fatal("Expected error for empty team, got nil")
	}
}

func TestTeamService_GetResolvedMembersForAssignment_SkipOnLeave(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	seedUsers(t, tdb, []int{2, 3})

	svc := createTeamService(t, tdb)
	teamID := seedTeamWithMembers(t, tdb, []int{1, 2, 3})

	// Put user 2 on leave
	now := time.Now().UTC()
	_, err := tdb.GetDatabase().Exec(`
		INSERT INTO user_leave_periods (user_id, start_date, end_date, reason, is_active, created_at, updated_at)
		VALUES (2, ?, ?, 'Sick', true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, now.AddDate(0, 0, -1), now.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("Failed to create leave: %v", err)
	}

	members, err := svc.GetResolvedMembersForAssignment(teamID, true, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("Expected 2 members (skipping on-leave), got %d", len(members))
	}
	for _, m := range members {
		if m == 2 {
			t.Error("User 2 should have been skipped (on leave)")
		}
	}
}

func TestTeamService_GetResolvedMembersForAssignment_UseSubstitutes(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	seedUsers(t, tdb, []int{2, 3, 4})

	svc := createTeamService(t, tdb)
	teamID := seedTeamWithMembers(t, tdb, []int{1, 2, 3})

	// Put user 2 on leave with user 4 as substitute
	now := time.Now().UTC()
	_, err := tdb.GetDatabase().Exec(`
		INSERT INTO user_leave_periods (user_id, substitute_user_id, start_date, end_date, reason, is_active, created_at, updated_at)
		VALUES (2, 4, ?, ?, 'Vacation', true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, now.AddDate(0, 0, -1), now.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("Failed to create leave: %v", err)
	}

	members, err := svc.GetResolvedMembersForAssignment(teamID, true, true)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should have user 1, 3, and 4 (substitute for 2)
	found4 := false
	for _, m := range members {
		if m == 2 {
			t.Error("User 2 should not be in the list (on leave)")
		}
		if m == 4 {
			found4 = true
		}
	}
	if !found4 {
		t.Error("Expected substitute user 4 in the list")
	}
}

// --- GetNextRoundRobinAssignee ---

func TestTeamService_GetNextRoundRobinAssignee_FirstAssignment(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	seedUsers(t, tdb, []int{2, 3})

	svc := createTeamService(t, tdb)
	teamID := seedTeamWithMembers(t, tdb, []int{1, 2, 3})

	assignee, err := svc.GetNextRoundRobinAssignee(100, teamID, false, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if assignee != 1 {
		t.Errorf("Expected first assignment to user 1, got %d", assignee)
	}
}

func TestTeamService_GetNextRoundRobinAssignee_Rotation(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	seedUsers(t, tdb, []int{2, 3})

	svc := createTeamService(t, tdb)
	teamID := seedTeamWithMembers(t, tdb, []int{1, 2, 3})

	// First assignment -> user 1
	a1, err := svc.GetNextRoundRobinAssignee(200, teamID, false, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if a1 != 1 {
		t.Errorf("Expected user 1, got %d", a1)
	}

	// Second assignment -> user 2
	a2, err := svc.GetNextRoundRobinAssignee(200, teamID, false, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if a2 != 2 {
		t.Errorf("Expected user 2, got %d", a2)
	}

	// Third assignment -> user 3
	a3, err := svc.GetNextRoundRobinAssignee(200, teamID, false, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if a3 != 3 {
		t.Errorf("Expected user 3, got %d", a3)
	}
}

func TestTeamService_GetNextRoundRobinAssignee_WrapAround(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	seedUsers(t, tdb, []int{2})

	svc := createTeamService(t, tdb)
	teamID := seedTeamWithMembers(t, tdb, []int{1, 2})

	// Assign to 1, then 2, then should wrap to 1
	svc.GetNextRoundRobinAssignee(300, teamID, false, false) // -> 1
	svc.GetNextRoundRobinAssignee(300, teamID, false, false) // -> 2

	a3, err := svc.GetNextRoundRobinAssignee(300, teamID, false, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if a3 != 1 {
		t.Errorf("Expected wrap-around to user 1, got %d", a3)
	}
}

func TestTeamService_GetNextRoundRobinAssignee_PerNodeState(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	seedUsers(t, tdb, []int{2, 3})

	svc := createTeamService(t, tdb)
	teamID := seedTeamWithMembers(t, tdb, []int{1, 2, 3})

	// Node 400: first assignment -> user 1
	a1, _ := svc.GetNextRoundRobinAssignee(400, teamID, false, false)

	// Node 500: first assignment -> user 1 (independent state)
	b1, _ := svc.GetNextRoundRobinAssignee(500, teamID, false, false)

	// Node 400: second assignment -> user 2
	a2, _ := svc.GetNextRoundRobinAssignee(400, teamID, false, false)

	if a1 != 1 || b1 != 1 {
		t.Errorf("Expected both first assignments to be user 1, got %d and %d", a1, b1)
	}
	if a2 != 2 {
		t.Errorf("Expected node 400 second assignment to be user 2, got %d", a2)
	}

	// Node 500: second assignment -> user 2 (still at its own position)
	b2, _ := svc.GetNextRoundRobinAssignee(500, teamID, false, false)
	if b2 != 2 {
		t.Errorf("Expected node 500 second assignment to be user 2, got %d", b2)
	}
}
