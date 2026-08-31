//go:build test

package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/repository"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

func TestNextAppendKeyPreservesRankRepresentation(t *testing.T) {
	tests := []struct {
		name    string
		byteMax *string
		want    string
	}{
		{name: "empty table", want: "a0"},
		{name: "legacy rank", byteMax: diagnosticsStringPointer("a1"), want: "a2"},
		{name: "canonical bucket zero", byteMax: diagnosticsStringPointer("0|a1"), want: "0|a2"},
		{name: "canonical bucket two", byteMax: diagnosticsStringPointer("2|a1"), want: "2|a2"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := nextAppendKey(test.byteMax)
			if err != nil {
				t.Fatalf("nextAppendKey() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("nextAppendKey() = %q, want %q", got, test.want)
			}
		})
	}
}

func diagnosticsStringPointer(value string) *string {
	return &value
}

func TestDiagnosticsGlobalRankMigrationControls(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	handler := &DiagnosticsHandler{fracIndexRepo: repository.NewFracIndexRepository(tdb.DB)}

	assertControl := func(action string, wantStatus int, wantPhase string) {
		t.Helper()
		body, err := json.Marshal(map[string]string{"action": action})
		if err != nil {
			t.Fatalf("marshal action: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/admin/diagnostics/frac-index/migration", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ControlGlobalRankMigration(response, req)
		if response.Code != wantStatus {
			t.Fatalf("%s status = %d body=%s, want %d", action, response.Code, response.Body.String(), wantStatus)
		}
		if wantPhase == "" {
			return
		}
		state, err := handler.fracIndexRepo.GetGlobalRankState()
		if err != nil {
			t.Fatalf("load state after %s: %v", action, err)
		}
		if string(state.Phase) != wantPhase {
			t.Fatalf("phase after %s = %q, want %q", action, state.Phase, wantPhase)
		}
	}

	assertControl("invalid", http.StatusBadRequest, "")
	assertControl("start", http.StatusOK, "migrating")
	assertControl("start", http.StatusConflict, "")
	assertControl("pause", http.StatusOK, "paused")
	assertControl("resume", http.StatusOK, "migrating")
}

func TestDiagnosticsGlobalRankMigrationControlResetsFailedState(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	if _, err := tdb.DB.Exec(`
		UPDATE global_rank_state
		SET target_bucket = 1,
		    phase = 'failed',
		    direction = 'high_to_low',
		    frontier = '0|a1',
		    lease_owner = 'failed-worker',
		    lease_expires_at = CURRENT_TIMESTAMP,
		    migrated_count = 1,
		    total_count = 1,
		    last_error = 'invalid active-bucket rank'
		WHERE id = 1`); err != nil {
		t.Fatalf("set failed migration state: %v", err)
	}

	handler := &DiagnosticsHandler{fracIndexRepo: repository.NewFracIndexRepository(tdb.DB)}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/diagnostics/frac-index/migration", bytes.NewBufferString(`{"action":"reset"}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ControlGlobalRankMigration(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("reset status = %d body=%s, want %d", response.Code, response.Body.String(), http.StatusOK)
	}

	state, err := handler.fracIndexRepo.GetGlobalRankState()
	if err != nil {
		t.Fatalf("load reset state: %v", err)
	}
	if state.Phase != repository.GlobalRankPhaseStable || state.TargetBucket != nil || state.Direction != nil || state.LastError != nil {
		t.Fatalf("reset state = %+v, want cleared stable state", state)
	}
}

func TestDiagnosticsGlobalRankMigrationControlRefusesSplitReset(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	// Corruption fixture: the API cannot create a failed split migration.
	var workspaceID int
	if err := tdb.DB.QueryRow(`
		INSERT INTO workspaces (name, key, description, active, is_personal, created_at, updated_at)
		VALUES ('Split reset', 'SPLIT', 'Test', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id`, true, false).Scan(&workspaceID); err != nil {
		t.Fatalf("create split-reset workspace: %v", err)
	}
	testFactory := factory.NewTestFactory(tdb.GetDatabase())
	for number := range 2 {
		itemID, err := testFactory.CreateItem(factory.CreateItemOpts{
			WorkspaceID: workspaceID,
			Title:       fmt.Sprintf("Split item %d", number+1),
			IsTask:      true,
		})
		if err != nil {
			t.Fatalf("create split-reset item %d: %v", number+1, err)
		}
		if number == 1 {
			// A failed migration is the only supported source of split buckets.
			if _, err := tdb.DB.Exec(`UPDATE items SET frac_index = '1|a1' WHERE id = ?`, itemID); err != nil {
				t.Fatalf("move split-reset item to target bucket: %v", err)
			}
		}
	}
	if _, err := tdb.DB.Exec(`
		UPDATE global_rank_state
		SET target_bucket = 1,
		    phase = 'failed',
		    direction = 'high_to_low',
		    frontier = '0|a2',
		    migrated_count = 1,
		    total_count = 2,
		    last_error = 'invalid active-bucket rank'
		WHERE id = 1`); err != nil {
		t.Fatalf("set split failed migration: %v", err)
	}

	handler := &DiagnosticsHandler{fracIndexRepo: repository.NewFracIndexRepository(tdb.DB)}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/diagnostics/frac-index/migration", bytes.NewBufferString(`{"action":"reset"}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ControlGlobalRankMigration(response, req)
	if response.Code != http.StatusConflict {
		t.Fatalf("reset status = %d body=%s, want %d", response.Code, response.Body.String(), http.StatusConflict)
	}
	if !strings.Contains(response.Body.String(), "resume the migration to completion") {
		t.Fatalf("reset body = %s, want actionable resume remedy", response.Body.String())
	}
}

func TestDiagnosticsFracIndexIncludesMigrationIntegrity(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	handler := &DiagnosticsHandler{fracIndexRepo: repository.NewFracIndexRepository(tdb.DB)}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/diagnostics/frac-index", nil)
	response := httptest.NewRecorder()

	handler.GetFracIndexState(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["migration"] == nil || body["integrity"] == nil {
		t.Fatalf("diagnostics response lacks migration integrity: %+v", body)
	}
	if healthy, ok := body["healthy"].(bool); !ok || !healthy {
		t.Fatalf("healthy = %#v, want true", body["healthy"])
	}
}

func TestDiagnosticsFracIndexReportsInvalidDurableMigrationState(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	// Corruption fixture: the production API cannot create a migrating state
	// without a target/direction, but diagnostics must make it operator-visible.
	if _, err := tdb.DB.Exec(`
		UPDATE global_rank_state
		SET phase = 'migrating', target_bucket = NULL, direction = NULL
		WHERE id = 1`); err != nil {
		t.Fatalf("corrupt migration markers: %v", err)
	}

	handler := &DiagnosticsHandler{fracIndexRepo: repository.NewFracIndexRepository(tdb.DB)}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/diagnostics/frac-index", nil)
	response := httptest.NewRecorder()
	handler.GetFracIndexState(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want diagnostics response", response.Code, response.Body.String())
	}
	var body struct {
		Healthy   bool `json:"healthy"`
		Integrity struct {
			Healthy bool     `json:"healthy"`
			Issues  []string `json:"issues"`
		} `json:"integrity"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Healthy || body.Integrity.Healthy || len(body.Integrity.Issues) == 0 {
		t.Fatalf("diagnostics = %+v, want unhealthy state with an operator-visible issue", body)
	}
}

func TestDiagnosticsFracIndexReportsFailedMigrationReason(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	// Corruption fixture: the production API cannot create a split failed
	// migration, which is the state diagnostics must expose here.
	var workspaceID int
	if err := tdb.DB.QueryRow(`
		INSERT INTO workspaces (name, key, description, active, is_personal, created_at, updated_at)
		VALUES ('Diagnostics', 'DIAG', 'Test', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id`, true, false).Scan(&workspaceID); err != nil {
		t.Fatalf("create diagnostics workspace: %v", err)
	}
	testFactory := factory.NewTestFactory(tdb.GetDatabase())
	for number := range 2 {
		itemID, err := testFactory.CreateItem(factory.CreateItemOpts{
			WorkspaceID: workspaceID,
			Title:       fmt.Sprintf("Diagnostics item %d", number+1),
			IsTask:      true,
		})
		if err != nil {
			t.Fatalf("create diagnostics item %d: %v", number+1, err)
		}
		if number == 1 {
			// A failed migration is the only supported source of split buckets.
			if _, err := tdb.DB.Exec(`UPDATE items SET frac_index = '1|a1' WHERE id = ?`, itemID); err != nil {
				t.Fatalf("move diagnostics item to target bucket: %v", err)
			}
		}
	}
	const failureReason = "item 42 has invalid active-bucket rank"
	if _, err := tdb.DB.Exec(`
		UPDATE global_rank_state
		SET target_bucket = 1,
		    phase = 'failed',
		    direction = 'high_to_low',
		    lease_owner = NULL,
		    lease_expires_at = NULL,
		    last_error = ?
		WHERE id = 1`, failureReason); err != nil {
		t.Fatalf("set failed migration state: %v", err)
	}

	handler := &DiagnosticsHandler{fracIndexRepo: repository.NewFracIndexRepository(tdb.DB)}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/diagnostics/frac-index", nil)
	response := httptest.NewRecorder()
	handler.GetFracIndexState(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want diagnostics response", response.Code, response.Body.String())
	}
	var body struct {
		Healthy   bool `json:"healthy"`
		Migration struct {
			Phase     string  `json:"phase"`
			LastError *string `json:"last_error"`
		} `json:"migration"`
		Integrity struct {
			Healthy         bool     `json:"healthy"`
			PopulationSplit bool     `json:"population_split"`
			Issues          []string `json:"issues"`
		} `json:"integrity"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Healthy || body.Integrity.Healthy || body.Migration.Phase != "failed" {
		t.Fatalf("diagnostics = %+v, want unhealthy failed migration", body)
	}
	if body.Migration.LastError == nil || *body.Migration.LastError != failureReason {
		t.Fatalf("last_error = %v, want %q", body.Migration.LastError, failureReason)
	}
	if !diagnosticsContains(body.Integrity.Issues, "migration is failed") {
		t.Fatalf("integrity issues = %v, want failed-migration issue", body.Integrity.Issues)
	}
	if !body.Integrity.PopulationSplit || !diagnosticsContains(body.Integrity.Issues, "item ranks are split across buckets outside an active migration") {
		t.Fatalf("integrity = %+v, want explicit split-population issue", body.Integrity)
	}
}

func diagnosticsContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
