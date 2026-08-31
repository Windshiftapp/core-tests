//go:build test

package handlers

import (
	"net/http"
	"testing"
	"time"

	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func recurrenceVolumeDiagnosticsFixture(t *testing.T, ruleCount int) (*DiagnosticsHandler, *testutils.TestDB) {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	itemRepo := repository.NewItemRepository(tdb.GetDatabase())
	recurrenceRepo := repository.NewRecurrenceRepository(tdb.GetDatabase())

	tx, err := tdb.Begin()
	if err != nil {
		t.Fatalf("begin item transaction: %v", err)
	}
	itemIDs := make([]int, 0, ruleCount)
	for number := 1; number <= ruleCount; number++ {
		statusID, creatorID := data.StatusID, data.UserID
		itemID, createErr := itemRepo.Create(tx, &models.Item{
			WorkspaceID:         data.WorkspaceID,
			WorkspaceItemNumber: number,
			Title:               "Diagnostic recurrence template",
			StatusID:            &statusID,
			CreatorID:           &creatorID,
		})
		if createErr != nil {
			_ = tx.Rollback()
			t.Fatalf("create diagnostic item %d: %v", number, createErr)
		}
		itemIDs = append(itemIDs, itemID)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit diagnostic items: %v", err)
	}

	for _, itemID := range itemIDs {
		createdBy := data.UserID
		if _, err := recurrenceRepo.Create(&models.RecurrenceRule{
			TemplateItemID:   itemID,
			WorkspaceID:      data.WorkspaceID,
			RRule:            "FREQ=DAILY;COUNT=2",
			DtStart:          time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC),
			Timezone:         "UTC",
			LeadTimeDays:     14,
			CopyAssignee:     true,
			CopyPriority:     true,
			CopyCustomFields: true,
			CopyDescription:  true,
			IsActive:         true,
			CreatedBy:        &createdBy,
		}); err != nil {
			t.Fatalf("create diagnostic recurrence rule: %v", err)
		}
	}

	return &DiagnosticsHandler{
		recurrenceRepo: recurrenceRepo,
		settingsRepo:   repository.NewSystemSettingRepository(tdb.GetDatabase()),
		auditor:        logger.NewAuditor(tdb.GetDatabase()),
	}, tdb
}

func TestDiagnosticsRecurrenceVolumeThresholdCanBeAdministered(t *testing.T) {
	handler, _ := recurrenceVolumeDiagnosticsFixture(t, 3)

	updateReq := testutils.CreateJSONRequest(t, http.MethodPut, "/api/admin/diagnostics/recurrence-volume", map[string]interface{}{
		"diagnostic_enabled": true,
		"warning_threshold":  2,
	})
	updateRR := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateRecurrenceVolumeSettings, updateReq, nil)
	updateRR.AssertStatusCode(http.StatusOK)

	getReq := testutils.CreateJSONRequest(t, http.MethodGet, "/api/admin/diagnostics/recurrence-volume", nil)
	getRR := testutils.ExecuteAuthenticatedRequest(t, handler.GetRecurrenceVolume, getReq, nil)
	getRR.AssertStatusCode(http.StatusOK)
	var snapshot recurrenceVolumeSnapshot
	getRR.AssertJSONResponse(&snapshot)
	if snapshot.WarningThreshold != 2 || !snapshot.DiagnosticEnabled {
		t.Fatalf("diagnostic settings = enabled %v threshold %d, want true/2", snapshot.DiagnosticEnabled, snapshot.WarningThreshold)
	}
	if snapshot.TotalRules != 3 || snapshot.ActiveRules != 3 || snapshot.DueRules != 3 {
		t.Fatalf("recurrence totals = total %d active %d due %d, want 3/3/3", snapshot.TotalRules, snapshot.ActiveRules, snapshot.DueRules)
	}
	if snapshot.Healthy || len(snapshot.Workspaces) != 1 || !snapshot.Workspaces[0].Warning {
		t.Fatalf("warning snapshot = %+v, want one warning workspace and unhealthy state", snapshot)
	}

	disableReq := testutils.CreateJSONRequest(t, http.MethodPut, "/api/admin/diagnostics/recurrence-volume", map[string]interface{}{
		"diagnostic_enabled": false,
		"warning_threshold":  2,
	})
	disableRR := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateRecurrenceVolumeSettings, disableReq, nil)
	disableRR.AssertStatusCode(http.StatusOK)

	getReq = testutils.CreateJSONRequest(t, http.MethodGet, "/api/admin/diagnostics/recurrence-volume", nil)
	getRR = testutils.ExecuteAuthenticatedRequest(t, handler.GetRecurrenceVolume, getReq, nil)
	getRR.AssertJSONResponse(&snapshot)
	if snapshot.DiagnosticEnabled || !snapshot.Healthy || snapshot.Workspaces[0].Warning {
		t.Fatalf("disabled diagnostic snapshot = %+v, want counts without warning state", snapshot)
	}
}

func TestDiagnosticsRecurrenceVolumeRejectsThresholdAboveHardLimit(t *testing.T) {
	handler, _ := recurrenceVolumeDiagnosticsFixture(t, 0)
	req := testutils.CreateJSONRequest(t, http.MethodPut, "/api/admin/diagnostics/recurrence-volume", map[string]interface{}{
		"diagnostic_enabled": true,
		"warning_threshold":  101,
	})
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateRecurrenceVolumeSettings, req, nil)
	rr.AssertStatusCode(http.StatusBadRequest)
}
