package email

import (
	"path/filepath"
	"testing"

	"windshift/internal/database"
)

// The external processor test overlay replaces core's package-local processor
// test file. Keep the shared integration helper available to package email.
func newProcessorTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "email-processor.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	if err := db.Initialize(); err != nil {
		_ = db.Close()
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
