//go:build test

package services

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"windshift/internal/testutils"
)

func TestAttachmentSettingsGetStatusReportsWritableDirectory(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })

	attachmentPath := t.TempDir()
	service := NewAttachmentSettingsService(tdb.GetDatabase())
	if err := service.Initialize(attachmentPath); err != nil {
		t.Fatalf("initialize attachment settings: %v", err)
	}

	status, err := service.GetStatus()
	if err != nil {
		t.Fatalf("get attachment status: %v", err)
	}
	if !status.Enabled {
		t.Fatal("enabled = false, want true")
	}
	if !status.Writable {
		t.Fatal("writable = false, want true")
	}

	matches, err := filepath.Glob(filepath.Join(attachmentPath, ".windshift-write-test-*"))
	if err != nil {
		t.Fatalf("glob write probes: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("write probe files were not cleaned up: %v", matches)
	}
}

func TestAttachmentSettingsGetStatusLogsPathProbeFailure(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })

	attachmentPath := filepath.Join(t.TempDir(), "missing")
	service := NewAttachmentSettingsService(tdb.GetDatabase())
	if err := service.Initialize(attachmentPath); err != nil {
		t.Fatalf("initialize attachment settings: %v", err)
	}

	var output bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	status, err := service.GetStatus()
	if err != nil {
		t.Fatalf("get attachment status: %v", err)
	}
	if status.Writable {
		t.Fatal("writable = true, want false")
	}

	logged := output.String()
	if !strings.Contains(logged, "attachment storage path stat failed") {
		t.Fatalf("log output missing probe failure: %s", logged)
	}
	if !strings.Contains(logged, attachmentPath) {
		t.Fatalf("log output missing attachment path %q: %s", attachmentPath, logged)
	}
	if !strings.Contains(logged, `"error"`) {
		t.Fatalf("log output missing filesystem error: %s", logged)
	}
}
