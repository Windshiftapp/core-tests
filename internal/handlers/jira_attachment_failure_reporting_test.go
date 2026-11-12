//go:build test

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/jira"
)

type failingAttachmentClient struct {
	jira.Client
	err error
}

func (c *failingAttachmentClient) DownloadAttachment(context.Context, string, string) (io.ReadCloser, string, error) {
	return nil, "", c.err
}

func newJiraAttachmentReportingFixture(t *testing.T, storage *blobstore.Store, root string) (*JiraImportHandler, int, int, int) {
	t.Helper()
	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-attachment-failure-secret", "", storage)
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)
	if storage.Enabled() {
		if _, err := db.ExecWrite(`INSERT INTO attachment_settings (attachment_path, enabled) VALUES (?, true)`, root); err != nil {
			t.Fatalf("configure attachments: %v", err)
		}
	}
	return handler, workspaceID, itemTypeID, statusID
}

// TestJiraAttachmentFailuresAreCountedAndReported is the WI-2061 regression:
// a failed attachment download must increment FailedAttachments, push the job
// to completed_with_errors, and persist a durable fidelity finding instead of
// looking like a clean import.
func TestJiraAttachmentFailuresAreCountedAndReported(t *testing.T) {
	root := t.TempDir()
	handler, workspaceID, itemTypeID, statusID := newJiraAttachmentReportingFixture(t, blobstore.NewLocal(root), root)
	db := handler.db

	const attachmentCount = 2
	issue := attachmentConcurrencyIssue(attachmentCount)
	client := &failingAttachmentClient{err: errors.New("jira attachment denied with 403")}
	im := attachmentConcurrencyImportContext(workspaceID, itemTypeID, statusID, client)

	if err := handler.importIssue(t.Context(), im, &issue); err != nil {
		t.Fatalf("import issue: %v", err)
	}

	if im.progress.TotalAttachments != attachmentCount {
		t.Fatalf("TotalAttachments = %d, want %d", im.progress.TotalAttachments, attachmentCount)
	}
	if im.progress.ImportedAttachments != 0 {
		t.Fatalf("ImportedAttachments = %d, want 0", im.progress.ImportedAttachments)
	}
	if im.progress.FailedAttachments != attachmentCount {
		t.Fatalf("FailedAttachments = %d, want %d", im.progress.FailedAttachments, attachmentCount)
	}

	// The issue itself imported, so the job must report completed_with_errors
	// rather than a clean completed after the attachment failures.
	im.progress.ImportedIssues = 1
	status, phase, _ := jiraImportTerminalOutcome(im.progress)
	if status != "completed_with_errors" || phase != "completed_with_errors" {
		t.Fatalf("terminal outcome = %s/%s, want completed_with_errors/completed_with_errors", status, phase)
	}

	claimJiraImportJobForTest(t, handler, db, "job-jsm")
	handler.persistJiraImportResult("job-jsm", im.progress)

	var resultJSON string
	if err := db.QueryRow(`SELECT result_json FROM jira_import_jobs WHERE id = 'job-jsm'`).Scan(&resultJSON); err != nil {
		t.Fatalf("load persisted import result: %v", err)
	}
	var result struct {
		Findings []jiraImportFidelityFinding `json:"fidelity_findings"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("decode persisted import result: %v", err)
	}
	var finding *jiraImportFidelityFinding
	for i := range result.Findings {
		if result.Findings[i].Code == "jira_attachments_failed" {
			finding = &result.Findings[i]
			break
		}
	}
	if finding == nil {
		t.Fatalf("persisted findings = %#v, want jira_attachments_failed", result.Findings)
	}
	if finding.Severity != "error" || finding.Count != attachmentCount {
		t.Fatalf("attachment finding = %#v, want error severity and count %d", *finding, attachmentCount)
	}
}

// TestJiraAttachmentStorageDisabledIsSkippedNotFailed proves intentionally
// disabled storage is distinguishable from a failed attempted download.
func TestJiraAttachmentStorageDisabledIsSkippedNotFailed(t *testing.T) {
	handler, workspaceID, itemTypeID, statusID := newJiraAttachmentReportingFixture(t, blobstore.Disabled(), "")

	const attachmentCount = 3
	issue := attachmentConcurrencyIssue(attachmentCount)
	im := attachmentConcurrencyImportContext(workspaceID, itemTypeID, statusID, &fakeJiraAttachmentImportClient{})

	if err := handler.importIssue(t.Context(), im, &issue); err != nil {
		t.Fatalf("import issue: %v", err)
	}

	if im.progress.SkippedAttachments != attachmentCount {
		t.Fatalf("SkippedAttachments = %d, want %d", im.progress.SkippedAttachments, attachmentCount)
	}
	if im.progress.FailedAttachments != 0 {
		t.Fatalf("FailedAttachments = %d, want 0 for disabled storage", im.progress.FailedAttachments)
	}
	status, _, _ := jiraImportTerminalOutcome(im.progress)
	if status != "completed" {
		t.Fatalf("terminal outcome = %s, want completed when storage is intentionally disabled", status)
	}
}
