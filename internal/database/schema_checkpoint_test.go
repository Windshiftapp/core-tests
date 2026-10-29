package database

import (
	"strings"
	"testing"
)

func TestValidateCanonicalSchemaCheckpointAcceptsFreshDatabase(t *testing.T) {
	db := newGlobalRankTestDB(t)
	if err := ValidateCanonicalSchemaCheckpoint(db); err != nil {
		t.Fatalf("validate fresh schema checkpoint: %v", err)
	}
}

func TestValidateCanonicalSchemaCheckpointRefusesMissingMarker(t *testing.T) {
	db := newGlobalRankTestDB(t)
	if _, err := db.Exec("DROP TABLE schema_checkpoint"); err != nil {
		t.Fatalf("remove schema checkpoint: %v", err)
	}

	err := ValidateCanonicalSchemaCheckpoint(db)
	if err == nil || !strings.Contains(err.Error(), "back up the database") || !strings.Contains(err.Error(), CanonicalSchemaCheckpointVersion) {
		t.Fatalf("validate missing checkpoint error = %v, want actionable backup and upgrade guidance", err)
	}
}

func TestValidateCanonicalSchemaCheckpointRefusesUnsupportedVersion(t *testing.T) {
	db := newGlobalRankTestDB(t)
	if _, err := db.Exec("UPDATE schema_checkpoint SET version = ? WHERE id = 1", "0.8.4"); err != nil {
		t.Fatalf("change schema checkpoint version: %v", err)
	}

	err := ValidateCanonicalSchemaCheckpoint(db)
	if err == nil || !strings.Contains(err.Error(), "unsupported") || !strings.Contains(err.Error(), "0.8.4") {
		t.Fatalf("validate unsupported checkpoint error = %v, want unsupported-version refusal", err)
	}
}

func TestValidateCanonicalSchemaCheckpointAcceptsOnlineRankPhases(t *testing.T) {
	tests := []struct {
		name      string
		phase     string
		lastError interface{}
	}{
		{name: "migrating", phase: "migrating"},
		{name: "paused", phase: "paused"},
		{name: "failed", phase: "failed", lastError: "invalid active-bucket rank"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := newGlobalRankTestDB(t)
			if _, err := db.Exec(`
				UPDATE global_rank_state
				SET target_bucket = 1, phase = ?, direction = 'high_to_low', last_error = ?
				WHERE id = 1`, test.phase, test.lastError); err != nil {
				t.Fatalf("set online rank phase: %v", err)
			}

			if err := ValidateCanonicalSchemaCheckpoint(db); err != nil {
				t.Fatalf("validate %s online rank phase: %v", test.phase, err)
			}
		})
	}
}

func TestValidateCanonicalSchemaCheckpointRefusesLegacyRankPhase(t *testing.T) {
	// 'legacy' is the retired 0.8.5 checkpoint-converter value; the production
	// CHECK constraint still admits it so converted databases load, but
	// startup must refuse it like any other non-checkpoint phase.
	db := newGlobalRankTestDB(t)
	if _, err := db.Exec(`
		UPDATE global_rank_state
		SET target_bucket = NULL, phase = 'legacy', direction = NULL
		WHERE id = 1`); err != nil {
		t.Fatalf("set legacy rank phase: %v", err)
	}

	err := ValidateCanonicalSchemaCheckpoint(db)
	if err == nil || !strings.Contains(err.Error(), "invalid global rank phase") || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("validate legacy phase error = %v, want invalid-phase refusal", err)
	}
}

func TestValidateCanonicalSchemaCheckpointRefusesNonCanonicalRankIndex(t *testing.T) {
	db := newGlobalRankTestDB(t)
	if _, err := db.Exec("DROP INDEX idx_items_frac_index"); err != nil {
		t.Fatalf("drop canonical rank index: %v", err)
	}
	if _, err := db.Exec("CREATE INDEX idx_items_frac_index ON items(frac_index)"); err != nil {
		t.Fatalf("create non-unique rank index: %v", err)
	}

	err := ValidateCanonicalSchemaCheckpoint(db)
	if err == nil || !strings.Contains(err.Error(), "items.frac_index is not canonical") {
		t.Fatalf("validate non-canonical rank index error = %v, want existing schema refusal", err)
	}
}

func TestCanonicalRankPrefixValidationUsesFracIndexRangeIndex(t *testing.T) {
	db := newGlobalRankTestDB(t)
	rows, err := db.Query("EXPLAIN QUERY PLAN " + canonicalRankPrefixValidationQuery)
	if err != nil {
		t.Fatalf("explain canonical rank validation: %v", err)
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan canonical rank validation plan: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate canonical rank validation plan: %v", err)
	}
	plan := strings.Join(details, "\n")
	if strings.Contains(plan, "SCAN items") {
		t.Fatalf("canonical rank validation performs a full items scan:\n%s", plan)
	}
	if !strings.Contains(plan, "idx_items_frac_index") {
		t.Fatalf("canonical rank validation does not use idx_items_frac_index:\n%s", plan)
	}
}

func newGlobalRankTestDB(t *testing.T) Database {
	t.Helper()
	dsn := "file:schema-checkpoint-" + strings.ReplaceAll(t.Name(), "/", "-") + "?mode=memory&cache=shared"
	db, err := NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize database: %v", err)
	}
	return db
}
