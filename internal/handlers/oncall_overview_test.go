package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/contextkeys"
	"windshift/internal/database"
	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
)

func TestListSchedulesIncludesCompleteOverviewForTeamMembers(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "oncall-handler-overview.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	permissionService, err := services.NewPermissionService(db, services.PermissionCacheConfig{
		TTL: 0, MaxCacheSize: 8, WarmupOnStartup: false, PreWarmActive: false, BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })

	insertID := func(label, query string, args ...interface{}) int {
		t.Helper()
		result, err := db.ExecWrite(query, args...)
		if err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId for %s: %v", label, err)
		}
		return int(id)
	}
	memberID := insertID(
		"team member",
		`INSERT INTO users (email, username, first_name, last_name) VALUES ('member@example.test', 'member', 'Team', 'Member')`,
	)
	viewerID := insertID(
		"unrelated viewer",
		`INSERT INTO users (email, username, first_name, last_name) VALUES ('viewer@example.test', 'viewer', 'Other', 'Viewer')`,
	)
	overrideUserID := insertID(
		"override user",
		`INSERT INTO users (email, username, first_name, last_name) VALUES ('override@example.test', 'override', 'Covering', 'Member')`,
	)
	teamID := insertID("team", `INSERT INTO teams (name) VALUES ('Handler Overview Team')`)
	if _, err := db.ExecWrite(
		`INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, 'member')`,
		teamID,
		memberID,
	); err != nil {
		t.Fatalf("insert team membership: %v", err)
	}
	scheduleID := insertID(
		"schedule",
		`INSERT INTO on_call_schedules (team_id, name, description, timezone) VALUES (?, 'Primary Schedule', '', 'UTC')`,
		teamID,
	)
	layerID := insertID(
		"layer",
		`INSERT INTO on_call_schedule_layers (schedule_id, name, priority, rotation_type, rotation_interval_days, handoff_time, start_date) VALUES (?, 'Primary', 1, 'daily', 1, '00:00', '2000-01-01')`,
		scheduleID,
	)
	if _, err := db.ExecWrite(
		`INSERT INTO on_call_schedule_layer_members (layer_id, user_id, position) VALUES (?, ?, 1)`,
		layerID,
		memberID,
	); err != nil {
		t.Fatalf("insert layer member: %v", err)
	}
	if _, err := db.ExecWrite(
		`INSERT INTO on_call_schedule_overrides (schedule_id, user_id, override_user_id, start_time, end_time, reason, created_by) VALUES (?, ?, ?, ?, ?, 'Coverage', ?)`,
		scheduleID,
		memberID,
		overrideUserID,
		time.Now().Add(-time.Hour),
		time.Now().Add(time.Hour),
		memberID,
	); err != nil {
		t.Fatalf("insert override: %v", err)
	}

	onCallRepo := repository.NewOnCallRepository(db)
	handler := NewOnCallHandler(
		onCallRepo,
		repository.NewTeamRepository(db),
		services.NewOnCallService(db, onCallRepo, repository.NewLeaveRepository(db)),
		permissionService,
		logger.NewAuditor(db),
	)
	requestFor := func(userID int) *http.Request {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/teams/%d/on-call/schedules", teamID), nil)
		request.SetPathValue("id", fmt.Sprintf("%d", teamID))
		return request.WithContext(context.WithValue(request.Context(), contextkeys.User, &models.User{ID: userID}))
	}

	memberRecorder := httptest.NewRecorder()
	handler.ListSchedules(memberRecorder, requestFor(memberID))
	if memberRecorder.Code != http.StatusOK {
		t.Fatalf("member status = %d, want 200; body=%s", memberRecorder.Code, memberRecorder.Body.String())
	}
	var memberSchedules []models.OnCallSchedule
	if err := json.NewDecoder(memberRecorder.Body).Decode(&memberSchedules); err != nil {
		t.Fatalf("decode member response: %v", err)
	}
	if len(memberSchedules) != 1 || len(memberSchedules[0].Layers) != 1 || len(memberSchedules[0].Layers[0].Members) != 1 || len(memberSchedules[0].Overrides) != 1 {
		t.Fatalf("member schedules = %+v, want complete schedule graph", memberSchedules)
	}
	current := memberSchedules[0].CurrentOnCall
	if current == nil || len(current.OnCall) != 1 || current.OnCall[0].UserID != overrideUserID || current.OnCall[0].UserName != "Covering Member" {
		t.Fatalf("current on-call = %+v, want override user %d", current, overrideUserID)
	}

	viewerRecorder := httptest.NewRecorder()
	handler.ListSchedules(viewerRecorder, requestFor(viewerID))
	if viewerRecorder.Code != http.StatusOK {
		t.Fatalf("viewer status = %d, want 200; body=%s", viewerRecorder.Code, viewerRecorder.Body.String())
	}
	var viewerSchedules []models.OnCallSchedule
	if err := json.NewDecoder(viewerRecorder.Body).Decode(&viewerSchedules); err != nil {
		t.Fatalf("decode viewer response: %v", err)
	}
	if len(viewerSchedules) != 1 || len(viewerSchedules[0].Layers) != 1 {
		t.Fatalf("viewer schedules = %+v, want schedule and layer metadata", viewerSchedules)
	}
	if len(viewerSchedules[0].Layers[0].Members) != 0 || len(viewerSchedules[0].Overrides) != 0 || viewerSchedules[0].CurrentOnCall != nil {
		t.Fatalf("viewer schedules = %+v, want no protected roster identities", viewerSchedules)
	}
}
