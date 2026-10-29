//go:build test

package jiraimport

import (
	"database/sql"
	"errors"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/testutils"
)

func TestUpdateConnectionReplacesMutableFields(t *testing.T) {
	t.Parallel()
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-update")
	service := New(tdb.Database, blobstore.Disabled())

	if err := service.UpdateConnection("connection-update", ConnectionUpdate{
		Email:                "rotated@example.invalid",
		InstanceName:         "Rotated Jira",
		EncryptedCredentials: "rotated-ciphertext",
	}); err != nil {
		t.Fatalf("update connection: %v", err)
	}

	var email, instanceName, credentials string
	if err := tdb.QueryRow(`
		SELECT email, instance_name, encrypted_credentials
		FROM jira_import_connections WHERE id = 'connection-update'
	`).Scan(&email, &instanceName, &credentials); err != nil {
		t.Fatalf("load updated connection: %v", err)
	}
	if email != "rotated@example.invalid" || instanceName != "Rotated Jira" || credentials != "rotated-ciphertext" {
		t.Fatalf("updated connection = (%q, %q, %q)", email, instanceName, credentials)
	}

	loaded, err := service.LoadConnectionCredentials("connection-update")
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if loaded.Email != "rotated@example.invalid" || loaded.EncryptedCredentials != "rotated-ciphertext" {
		t.Fatalf("LoadConnectionCredentials() = %#v", loaded)
	}
}

func TestUpdateConnectionRejectsUnknownConnection(t *testing.T) {
	t.Parallel()
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	service := New(tdb.Database, blobstore.Disabled())

	err := service.UpdateConnection("missing", ConnectionUpdate{Email: "a@example.invalid", EncryptedCredentials: "x"})
	if !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("UpdateConnection() error = %v, want ErrConnectionNotFound", err)
	}
}

func TestDeleteConnectionSoftDeletesAndPreservesImportHistory(t *testing.T) {
	t.Parallel()
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-delete")
	insertJob(t, tdb, "job-delete", "connection-delete", "completed")
	service := New(tdb.Database, blobstore.Disabled())

	if err := service.DeleteConnection("connection-delete"); err != nil {
		t.Fatalf("delete connection: %v", err)
	}

	var deletedAt sql.NullTime
	var credentials string
	if err := tdb.QueryRow(`
		SELECT deleted_at, encrypted_credentials
		FROM jira_import_connections WHERE id = 'connection-delete'
	`).Scan(&deletedAt, &credentials); err != nil {
		t.Fatalf("load deleted connection: %v", err)
	}
	if !deletedAt.Valid {
		t.Fatal("deleted connection deleted_at is NULL, want a timestamp")
	}
	if credentials != "" {
		t.Fatalf("deleted connection credentials = %q, want cleared", credentials)
	}

	var jobCount int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM jira_import_jobs WHERE id = 'job-delete'`).Scan(&jobCount); err != nil {
		t.Fatalf("count preserved job: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("preserved job count = %d, want 1", jobCount)
	}

	connections, err := service.ListConnections()
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if len(connections) != 0 {
		t.Fatalf("ListConnections() = %#v, want deleted connection hidden", connections)
	}

	if _, err := service.LoadConnectionCredentials("connection-delete"); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("LoadConnectionCredentials() error = %v, want ErrConnectionNotFound", err)
	}
	if _, err := service.ConnectionDeploymentType("connection-delete"); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("ConnectionDeploymentType() error = %v, want ErrConnectionNotFound", err)
	}
}

func TestDeleteConnectionRejectsActiveImport(t *testing.T) {
	t.Parallel()
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-active")
	insertJob(t, tdb, "job-active", "connection-active", "running")
	service := New(tdb.Database, blobstore.Disabled())

	err := service.DeleteConnection("connection-active")
	if !errors.Is(err, ErrConnectionActive) {
		t.Fatalf("DeleteConnection() error = %v, want ErrConnectionActive", err)
	}

	var deletedAt sql.NullTime
	if err := tdb.QueryRow(`SELECT deleted_at FROM jira_import_connections WHERE id = 'connection-active'`).Scan(&deletedAt); err != nil {
		t.Fatalf("load connection: %v", err)
	}
	if deletedAt.Valid {
		t.Fatal("active connection was soft-deleted, want it retained")
	}
}

func TestDeleteConnectionRejectsUnknownConnection(t *testing.T) {
	t.Parallel()
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	service := New(tdb.Database, blobstore.Disabled())

	if err := service.DeleteConnection("missing"); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("DeleteConnection() error = %v, want ErrConnectionNotFound", err)
	}
}
