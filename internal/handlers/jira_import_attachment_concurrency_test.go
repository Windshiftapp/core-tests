//go:build test

package handlers

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"windshift/internal/blobstore"
	"windshift/internal/jira"
)

// blockingAttachmentClient holds each download until `target` downloads have
// started, so a correctly concurrent importer reaches that concurrency to make
// progress. A timeout on the import context ends the wait instead of hanging.
type blockingAttachmentClient struct {
	jira.Client
	target int

	mu          sync.Mutex
	inFlight    int
	maxInFlight int
	started     int
	release     chan struct{}
	closeOnce   sync.Once
}

func newBlockingAttachmentClient(target int) *blockingAttachmentClient {
	return &blockingAttachmentClient{target: target, release: make(chan struct{})}
}

func (c *blockingAttachmentClient) DownloadAttachment(ctx context.Context, _, _ string) (io.ReadCloser, string, error) {
	c.mu.Lock()
	c.inFlight++
	c.started++
	if c.inFlight > c.maxInFlight {
		c.maxInFlight = c.inFlight
	}
	if c.started >= c.target {
		c.closeOnce.Do(func() { close(c.release) })
	}
	c.mu.Unlock()

	select {
	case <-c.release:
	case <-ctx.Done():
	}

	c.mu.Lock()
	c.inFlight--
	c.mu.Unlock()

	return io.NopCloser(strings.NewReader("attachment evidence\n")), "text/plain", nil
}

func (c *blockingAttachmentClient) peakConcurrency() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxInFlight
}

func attachmentConcurrencyIssue(count int) jira.JiraIssue {
	attachments := make([]jira.JiraAttachment, 0, count)
	for i := 0; i < count; i++ {
		attachments = append(attachments, jira.JiraAttachment{
			ID:       fmt.Sprintf("attachment-%d", i),
			Filename: fmt.Sprintf("evidence-%d.txt", i),
			Content:  fmt.Sprintf("https://example.invalid/attachment-%d", i),
		})
	}
	return jira.JiraIssue{
		ID:  "10010",
		Key: "APP-10",
		Fields: jira.JiraIssueFields{
			Summary:    "Many attachments",
			IssueType:  &jira.JiraIssueType{ID: "type"},
			Status:     &jira.JiraStatus{ID: "status"},
			Attachment: attachments,
		},
	}
}

func attachmentConcurrencyImportContext(workspaceID, itemTypeID, statusID int, client jira.Client) *jiraImportContext {
	return &jiraImportContext{
		jobID:             "job-jsm",
		progress:          &ImportProgress{},
		workspaceID:       workspaceID,
		statusMap:         map[string]int{"status": statusID},
		itemTypeMap:       map[string]int{"type": itemTypeID},
		userMap:           map[string]int{},
		usernameMap:       map[string]string{},
		portalCustomerMap: map[string]int{},
		versionMap:        map[string]int{},
		iterationMap:      map[string]int{},
		customFieldIDMap:  map[string]int{},
		choiceOptionIDs:   map[string]map[string]int{},
		client:            client,
	}
}

// TestJiraAttachmentImportDownloadsConcurrently is the WI-1982 contract: an
// issue's attachment downloads run in parallel up to the configured limit.
func TestJiraAttachmentImportDownloadsConcurrently(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	root := t.TempDir()
	handler := NewJiraImportHandler(db, "jira-attachment-concurrency-secret", "", blobstore.NewLocal(root)).
		WithImportConcurrency(4)
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)
	if _, err := db.ExecWrite(`INSERT INTO attachment_settings (attachment_path, enabled) VALUES (?, true)`, root); err != nil {
		t.Fatalf("configure attachments: %v", err)
	}

	const attachmentCount = 6
	client := newBlockingAttachmentClient(3)
	issue := attachmentConcurrencyIssue(attachmentCount)
	im := attachmentConcurrencyImportContext(workspaceID, itemTypeID, statusID, client)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if err := handler.importIssue(ctx, im, &issue); err != nil {
		t.Fatalf("import issue: %v", err)
	}

	var imported int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM attachments a JOIN items i ON a.item_id = i.id WHERE i.workspace_id = ?
	`, workspaceID).Scan(&imported); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if imported != attachmentCount {
		t.Fatalf("imported attachments = %d, want %d", imported, attachmentCount)
	}
	if peak := client.peakConcurrency(); peak < 3 {
		t.Fatalf("peak concurrent downloads = %d, want at least 3", peak)
	}
}

// TestJiraAttachmentImportParallelMatchesSequential proves the concurrent
// download path produces the same persisted attachments as the sequential one.
func TestJiraAttachmentImportParallelMatchesSequential(t *testing.T) {
	const attachmentCount = 5

	run := func(t *testing.T, concurrency int) []string {
		t.Helper()
		db := newJiraServiceManagementTestDB(t)
		root := t.TempDir()
		handler := NewJiraImportHandler(db, "jira-attachment-parity-secret", "", blobstore.NewLocal(root)).
			WithImportConcurrency(concurrency)
		workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)
		if _, err := db.ExecWrite(`INSERT INTO attachment_settings (attachment_path, enabled) VALUES (?, true)`, root); err != nil {
			t.Fatalf("configure attachments: %v", err)
		}

		issue := attachmentConcurrencyIssue(attachmentCount)
		im := attachmentConcurrencyImportContext(workspaceID, itemTypeID, statusID, &fakeJiraAttachmentImportClient{})
		if err := handler.importIssue(t.Context(), im, &issue); err != nil {
			t.Fatalf("import issue: %v", err)
		}

		rows, err := db.Query(`
			SELECT a.original_filename
			FROM attachments a JOIN items i ON a.item_id = i.id
			WHERE i.workspace_id = ?
			ORDER BY a.original_filename
		`, workspaceID)
		if err != nil {
			t.Fatalf("list attachments: %v", err)
		}
		defer func() { _ = rows.Close() }()

		var filenames []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatalf("scan attachment: %v", err)
			}
			filenames = append(filenames, name)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate attachments: %v", err)
		}
		return filenames
	}

	sequential := run(t, 1)
	parallel := run(t, 4)
	if len(sequential) != attachmentCount {
		t.Fatalf("sequential imported %d attachments, want %d", len(sequential), attachmentCount)
	}
	if strings.Join(sequential, ",") != strings.Join(parallel, ",") {
		t.Fatalf("parallel attachments = %v, want the sequential set %v", parallel, sequential)
	}
}

// TestJiraAttachmentImportSkipsRecordsWhenContextCancelled is the regression
// guard for a cancelled import recording attachments with an empty file path.
// The download pool stops launching work once the context is done; those
// entries must be skipped rather than written as records that point at nothing.
func TestJiraAttachmentImportSkipsRecordsWhenContextCancelled(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	root := t.TempDir()
	handler := NewJiraImportHandler(db, "jira-attachment-cancel-secret", "", blobstore.NewLocal(root)).
		WithImportConcurrency(4)
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)
	if _, err := db.ExecWrite(`INSERT INTO attachment_settings (attachment_path, enabled) VALUES (?, true)`, root); err != nil {
		t.Fatalf("configure attachments: %v", err)
	}

	issue := attachmentConcurrencyIssue(4)
	im := attachmentConcurrencyImportContext(workspaceID, itemTypeID, statusID, &fakeJiraAttachmentImportClient{})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// Later subresources may fail on the cancelled context; only the attachment
	// records are under test.
	_ = handler.importIssue(ctx, im, &issue)

	var broken int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE COALESCE(file_path, '') = ''`).Scan(&broken); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if broken != 0 {
		t.Fatalf("cancelled import recorded %d attachment(s) with an empty file path", broken)
	}
}
