//go:build test

package scheduler

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"windshift/internal/testutils"
)

func TestFieldScrubRemovesDeletedFieldFromItemsAndAssets(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	var workspaceID, assetSetID, assetTypeID, itemID, assetID, emptyAssetID int
	for _, fixture := range []struct {
		query string
		dest  *int
	}{
		{`INSERT INTO workspaces (name, key) VALUES ('Cleanup', 'CLN') RETURNING id`, &workspaceID},
		{`INSERT INTO asset_management_sets (name) VALUES ('Cleanup assets') RETURNING id`, &assetSetID},
	} {
		if err := db.QueryRow(fixture.query).Scan(fixture.dest); err != nil {
			t.Fatalf("seed cleanup fixture: %v", err)
		}
	}
	if err := db.QueryRow(`INSERT INTO asset_types (set_id, name) VALUES (?, 'Server') RETURNING id`, assetSetID).Scan(&assetTypeID); err != nil {
		t.Fatalf("insert asset type: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index, custom_field_values)
		VALUES (?, 1, 'Cleanup item', '', 'a', '{"91":"remove","7":"keep"}') RETURNING id
	`, workspaceID).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO assets (set_id, asset_type_id, title, custom_field_values)
		VALUES (?, ?, 'Cleanup asset', '{"91":"remove","7":"keep"}') RETURNING id
	`, assetSetID, assetTypeID).Scan(&assetID); err != nil {
		t.Fatalf("insert asset: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO assets (set_id, asset_type_id, title, custom_field_values)
		VALUES (?, ?, 'Cleanup empty asset', '{"91":"remove"}') RETURNING id
	`, assetSetID, assetTypeID).Scan(&emptyAssetID); err != nil {
		t.Fatalf("insert empty asset: %v", err)
	}

	scheduler := NewCFVCleanupScheduler(db)
	scheduler.batchSize = 1
	processed, err := scheduler.processJob(91)
	if err != nil {
		t.Fatalf("processJob: %v", err)
	}
	if processed != 3 {
		t.Fatalf("processed = %d, want item and two assets", processed)
	}

	for _, row := range []struct {
		table string
		id    int
	}{
		{table: "items", id: itemID},
		{table: "assets", id: assetID},
	} {
		var raw string
		if err := db.QueryRow(`SELECT custom_field_values FROM `+row.table+` WHERE id = ?`, row.id).Scan(&raw); err != nil {
			t.Fatalf("load %s custom fields: %v", row.table, err)
		}
		var values map[string]any
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			t.Fatalf("decode %s custom fields: %v", row.table, err)
		}
		if len(values) != 1 || values["7"] != "keep" {
			t.Fatalf("%s custom fields = %v, want only the retained field", row.table, values)
		}
	}

	var emptyRaw sql.NullString
	if err := db.QueryRow(`SELECT custom_field_values FROM assets WHERE id = ?`, emptyAssetID).Scan(&emptyRaw); err != nil {
		t.Fatalf("load empty asset custom fields: %v", err)
	}
	if emptyRaw.Valid {
		t.Fatalf("empty asset custom fields = %q, want NULL", emptyRaw.String)
	}
}

func TestStripCFVOptionIDsRemovesNumericStrings(t *testing.T) {
	tests := []struct {
		name      string
		fieldType string
		input     string
		want      map[string]any
	}{
		{
			name:      "select",
			fieldType: "select",
			input:     `{"91":"5","7":"keep"}`,
			want:      map[string]any{"7": "keep"},
		},
		{
			name:      "multiselect",
			fieldType: "multiselect",
			input:     `{"91":["5",6,7],"7":"keep"}`,
			want:      map[string]any{"91": []any{float64(6), float64(7)}, "7": "keep"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaned, changed, err := stripCFVOptionIDs(tt.input, "91", tt.fieldType, map[int]bool{5: true})
			if err != nil {
				t.Fatalf("stripCFVOptionIDs: %v", err)
			}
			if !changed {
				t.Fatal("changed = false, want numeric-string option removed")
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(cleaned), &got); err != nil {
				t.Fatalf("decode cleaned value: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("cleaned = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCleanupJobRetriesAndSurfacesPermanentFailure(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	var jobID int
	if err := db.QueryRow(`
		INSERT INTO pending_custom_field_cleanups (field_id, job_type, status)
		VALUES (91, 'unsupported_job', 'pending') RETURNING id
	`).Scan(&jobID); err != nil {
		t.Fatalf("insert failing cleanup job: %v", err)
	}

	scheduler := NewCFVCleanupScheduler(db)
	scheduler.RunOnceForTests()

	var status string
	var attempts int
	var retryScheduled bool
	if err := db.QueryRow(`
		SELECT status, attempt_count, next_attempt_at IS NOT NULL
		FROM pending_custom_field_cleanups WHERE id = ?
	`, jobID).Scan(&status, &attempts, &retryScheduled); err != nil {
		t.Fatalf("load retry state: %v", err)
	}
	if status != "pending" || attempts != 1 || !retryScheduled {
		t.Fatalf("retry state = status %q attempts %d scheduled %v, want pending, 1, true", status, attempts, retryScheduled)
	}

	if _, err := db.ExecWrite(`
		UPDATE pending_custom_field_cleanups
		SET attempt_count = 2, next_attempt_at = NULL
		WHERE id = ?
	`, jobID); err != nil {
		t.Fatalf("advance job to final attempt: %v", err)
	}
	scheduler.RunOnceForTests()

	var completed, retryCleared bool
	var message string
	if err := db.QueryRow(`
		SELECT status, attempt_count, completed_at IS NOT NULL,
		       next_attempt_at IS NULL, COALESCE(error_message, '')
		FROM pending_custom_field_cleanups WHERE id = ?
	`, jobID).Scan(&status, &attempts, &completed, &retryCleared, &message); err != nil {
		t.Fatalf("load terminal failure: %v", err)
	}
	if status != "failed" || attempts != 3 || !completed || !retryCleared || !strings.Contains(message, "unknown job type") {
		t.Fatalf("terminal state = status %q attempts %d completed %v retry cleared %v message %q", status, attempts, completed, retryCleared, message)
	}

	var runSuccess bool
	var runError string
	if err := db.QueryRow(`
		SELECT success, COALESCE(error_message, '')
		FROM scheduler_runs WHERE scheduler_name = ?
		ORDER BY started_at DESC LIMIT 1
	`, schedulerName).Scan(&runSuccess, &runError); err != nil {
		t.Fatalf("load admin-visible scheduler run: %v", err)
	}
	if runSuccess || !strings.Contains(runError, "failed permanently after 3 attempts") {
		t.Fatalf("scheduler run = success %v error %q, want surfaced permanent failure", runSuccess, runError)
	}
}
