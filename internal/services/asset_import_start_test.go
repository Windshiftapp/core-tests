//go:build test

package services

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/repository"
	"windshift/internal/testutils"
	"windshift/internal/csvimport"
)

type importStartBarrierDB struct {
	database.Database
	release   chan struct{}
	started   chan struct{}
	completed chan struct{}
}

func (db *importStartBarrierDB) ExecWrite(query string, args ...interface{}) (sql.Result, error) {
	if strings.Contains(query, "SET status = 'running'") {
		db.started <- struct{}{}
		<-db.release
	}
	result, err := db.Database.ExecWrite(query, args...)
	if strings.Contains(query, "completed_at = ?") {
		db.completed <- struct{}{}
	}
	return result, err
}

func TestAssetImportStartRetriesShareOneWorker(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := &importStartBarrierDB{Database: tdb.GetDatabase(), release: make(chan struct{}), started: make(chan struct{}, 2), completed: make(chan struct{}, 2)}
	userID, setID, typeID := assetEventFixture(t, db)
	repo := repository.NewAssetRepository(db)
	roleID, err := repo.GetAssetRoleIDByName("Administrator")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AssignUserRole(setID, userID, roleID, userID); err != nil {
		t.Fatal(err)
	}
	config := DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	perms, err := NewPermissionService(db, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = perms.Close() })
	app := NewAssetApplicationService(db, perms, nil, nil).WithImportStorage(t.TempDir())
	upload, err := app.UploadCSV(userID, setID, "assets.csv", true, "comma", strings.NewReader("title\nOnly once\n"))
	if err != nil {
		t.Fatal(err)
	}
	input := StartAssetImport{UploadID: upload.UploadID, AssetTypeID: typeID, HasHeader: true, Delimiter: "comma", Mappings: AssetImportMappings{Title: 0, Description: -1, AssetTag: -1, CategoryID: -1, StatusID: -1}}
	first, err := app.StartImport(userID, setID, AuditActor{UserID: userID}, input)
	if err != nil {
		t.Fatal(err)
	}
	workers := 1
	released := false
	wait := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			t.Fatal("import worker did not reach barrier")
		}
	}
	t.Cleanup(func() {
		if !released {
			close(db.release)
			for range workers {
				wait(db.completed)
			}
		}
	})
	wait(db.started)
	retry, err := app.StartImport(userID, setID, AuditActor{UserID: userID}, input)
	if err != nil {
		t.Fatal(err)
	}
	if retry.JobID != first.JobID {
		workers++
		t.Fatalf("retry started job %s instead of %s", retry.JobID, first.JobID)
	}
	changed := input
	changed.Delimiter = "tab"
	if _, err := app.StartImport(userID, setID, AuditActor{UserID: userID}, changed); !errors.Is(err, csvimport.ErrConfigConflict) {
		t.Fatalf("changed settings = %v", err)
	}
	close(db.release)
	released = true
	wait(db.completed)
	completed, err := app.StartImport(userID, setID, AuditActor{UserID: userID}, input)
	if err != nil {
		t.Fatal(err)
	}
	if completed.JobID != first.JobID || completed.Status != "completed" || completed.Progress.ImportedCount != 1 {
		t.Fatalf("completed retry = %+v", completed)
	}
	for _, check := range []struct {
		query string
		want  int
	}{
		{"SELECT COUNT(*) FROM import_jobs", 1},
		{"SELECT COUNT(*) FROM assets", 1},
		{"SELECT COUNT(*) FROM domain_events WHERE event_type = 'asset.created.v1'", 1},
	} {
		var got int
		if err := db.QueryRow(check.query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Fatalf("%s = %d, want %d", check.query, got, check.want)
		}
	}
}
