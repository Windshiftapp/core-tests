//go:build test

package jiraimport

import (
	"database/sql"
	"errors"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/testutils"
)

func TestHideJobRemovesItFromHistoryWithoutDeletingIt(t *testing.T) {
	t.Parallel()
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-hide")
	insertJob(t, tdb, "job-hide", "connection-hide", "completed")
	service := New(tdb.Database, blobstore.Disabled())

	if err := service.HideJob("job-hide"); err != nil {
		t.Fatalf("hide job: %v", err)
	}

	var status string
	var hiddenAt sql.NullTime
	if err := tdb.QueryRow(`
		SELECT status, hidden_at FROM jira_import_jobs WHERE id = 'job-hide'
	`).Scan(&status, &hiddenAt); err != nil {
		t.Fatalf("load hidden job: %v", err)
	}
	if status != "completed" {
		t.Fatalf("hidden job status = %q, want completed", status)
	}
	if !hiddenAt.Valid {
		t.Fatal("hidden job hidden_at is NULL, want a timestamp")
	}

	jobs, err := service.ListJobs()
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("ListJobs() = %#v, want hidden job excluded", jobs)
	}
}

func TestHideJobIsIdempotent(t *testing.T) {
	t.Parallel()
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-hide-twice")
	insertJob(t, tdb, "job-hide-twice", "connection-hide-twice", "completed")
	service := New(tdb.Database, blobstore.Disabled())

	if err := service.HideJob("job-hide-twice"); err != nil {
		t.Fatalf("first hide: %v", err)
	}
	if err := service.HideJob("job-hide-twice"); err != nil {
		t.Fatalf("second hide: %v", err)
	}
}

func TestHideJobRejectsActiveImport(t *testing.T) {
	t.Parallel()
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-hide-active")
	insertJob(t, tdb, "job-hide-active", "connection-hide-active", "running")
	service := New(tdb.Database, blobstore.Disabled())

	if err := service.HideJob("job-hide-active"); !errors.Is(err, ErrJobActive) {
		t.Fatalf("HideJob() error = %v, want ErrJobActive", err)
	}

	var hiddenAt sql.NullTime
	if err := tdb.QueryRow(`SELECT hidden_at FROM jira_import_jobs WHERE id = 'job-hide-active'`).Scan(&hiddenAt); err != nil {
		t.Fatalf("load active job: %v", err)
	}
	if hiddenAt.Valid {
		t.Fatal("active job was hidden, want it retained in the history")
	}
}

func TestHideJobRejectsUnknownJob(t *testing.T) {
	t.Parallel()
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	service := New(tdb.Database, blobstore.Disabled())

	if err := service.HideJob("missing"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("HideJob() error = %v, want ErrJobNotFound", err)
	}
}
