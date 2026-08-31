package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"windshift/internal/services"
	"windshift/internal/testutils"
)

type publicFormAttachmentFixture struct {
	db             *testutils.TestDB
	handler        *FormHandler
	channelID      int
	requestTypeID  int
	attachmentPath string
}

func newPublicFormAttachmentFixture(t *testing.T, attachmentPath string, allowAttachments bool) *publicFormAttachmentFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	if !testutils.IsPostgres() {
		t.Cleanup(func() { _ = tdb.Close() })
	}

	insertID := func(label, query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := tdb.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return id
	}

	workspaceID := insertID("workspace", `
		INSERT INTO workspaces (name, key)
		VALUES ('Public attachment forms', 'PAF') RETURNING id
	`)
	var itemTypeID int
	if err := tdb.QueryRow(`SELECT id FROM item_types ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("select seeded item type: %v", err)
	}
	channelConfig := fmt.Sprintf(`{"form_slug":"attachment-form","form_workspace_ids":[%d]}`, workspaceID)
	channelID := insertID("channel", `
		INSERT INTO channels (name, type, direction, status, config, public_slug)
		VALUES ('Attachment form', 'form', 'inbound', 'enabled', ?, 'attachment-form') RETURNING id
	`, channelConfig)
	requestTypeConfig := `{}`
	if allowAttachments {
		requestTypeConfig = `{"allow_attachments":true}`
	}
	requestTypeID := insertID("request type", `
		INSERT INTO request_types (channel_id, name, item_type_id, workspace_id, is_active, config)
		VALUES (?, 'Attachment request', ?, ?, true, ?) RETURNING id
	`, channelID, itemTypeID, workspaceID, requestTypeConfig)
	if _, err := tdb.ExecWrite(`
		INSERT INTO request_type_fields
			(request_type_id, field_identifier, field_type, is_required, display_order)
		VALUES
			(?, 'title', 'default', true, 1),
			(?, 'description', 'default', true, 2)
	`, requestTypeID, requestTypeID); err != nil {
		t.Fatalf("insert request type fields: %v", err)
	}

	if _, err := tdb.ExecWrite(`DELETE FROM attachment_settings`); err != nil {
		t.Fatalf("clear attachment settings: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		INSERT INTO attachment_settings
			(max_file_size, allowed_mime_types, attachment_path, enabled)
		VALUES (5242880, '["text/plain; charset=utf-8","text/plain"]', ?, true)
	`, attachmentPath); err != nil {
		t.Fatalf("insert attachment settings: %v", err)
	}

	handler := NewFormHandler(tdb, nil, nil, nil, nil)
	handler.SetItemAttachmentService(services.NewItemAttachmentService(tdb, attachmentPath, nil))
	return &publicFormAttachmentFixture{
		db:             tdb,
		handler:        handler,
		channelID:      channelID,
		requestTypeID:  requestTypeID,
		attachmentPath: attachmentPath,
	}
}

func (f *publicFormAttachmentFixture) submit(t *testing.T, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	submission, err := json.Marshal(map[string]interface{}{
		"request_type_id": f.requestTypeID,
		"title":           "Printer evidence",
		"description":     "The printer displays an error.",
		"custom_fields":   map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("marshal submission: %v", err)
	}
	if err := writer.WriteField("submission", string(submission)); err != nil {
		t.Fatalf("write submission field: %v", err)
	}
	part, err := writer.CreateFormFile("attachments", filename)
	if err != nil {
		t.Fatalf("create attachment part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write attachment part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/forms/attachment-form/submit", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.SetPathValue("slug", "attachment-form")
	recorder := httptest.NewRecorder()
	f.handler.SubmitForm(recorder, request)
	return recorder
}

func (f *publicFormAttachmentFixture) rowCount(t *testing.T, table string) int {
	t.Helper()
	if table != "items" && table != "attachments" {
		t.Fatalf("unsupported count table %q", table)
	}
	var count int
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
	if err := f.db.QueryRow(query).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func TestPublicFormBootstrapExposesAttachmentLimitsWithoutStoragePath(t *testing.T) {
	attachmentPath := t.TempDir()
	fixture := newPublicFormAttachmentFixture(t, attachmentPath, true)

	request := httptest.NewRequest(http.MethodGet, "/api/forms/attachment-form/bootstrap", nil)
	request.SetPathValue("slug", "attachment-form")
	recorder := httptest.NewRecorder()
	fixture.handler.GetBootstrap(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), attachmentPath) {
		t.Fatalf("bootstrap leaked attachment storage path %q: %s", attachmentPath, recorder.Body.String())
	}
	var response PublicFormBootstrapResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if !response.Channel.Attachments.Enabled {
		t.Fatal("attachments.enabled = false, want true")
	}
	if response.Channel.Attachments.MaxFileSize != 5242880 {
		t.Fatalf("attachments.max_file_size = %d, want 5242880", response.Channel.Attachments.MaxFileSize)
	}
	if response.Channel.Attachments.MaxFiles != publicFormMaxAttachmentCount {
		t.Fatalf("attachments.max_files = %d, want %d", response.Channel.Attachments.MaxFiles, publicFormMaxAttachmentCount)
	}
	if len(response.Forms) != 1 || response.Forms[0].Config == nil || !response.Forms[0].Config.AllowAttachments {
		t.Fatalf("form config = %+v, want allow_attachments=true", response.Forms)
	}
}

func TestPublicFormMultipartSubmissionPersistsAnonymousAttachment(t *testing.T) {
	fixture := newPublicFormAttachmentFixture(t, t.TempDir(), true)
	content := []byte("public form evidence\n")

	recorder := fixture.submit(t, "evidence.txt", content)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		ItemID          int64 `json:"item_id"`
		AttachmentCount int   `json:"attachment_count"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode submission response: %v", err)
	}
	if response.ItemID <= 0 || response.AttachmentCount != 1 {
		t.Fatalf("response = %+v, want a created item with one attachment", response)
	}
	if got := fixture.rowCount(t, "items"); got != 1 {
		t.Fatalf("item count = %d, want 1", got)
	}
	if got := fixture.rowCount(t, "attachments"); got != 1 {
		t.Fatalf("attachment count = %d, want 1", got)
	}

	var itemID int
	var originalFilename, filePath, mimeType string
	var uploaderID sql.NullInt64
	if err := fixture.db.QueryRow(`
		SELECT item_id, original_filename, file_path, mime_type, uploaded_by
		FROM attachments
	`).Scan(&itemID, &originalFilename, &filePath, &mimeType, &uploaderID); err != nil {
		t.Fatalf("read attachment row: %v", err)
	}
	if int64(itemID) != response.ItemID || originalFilename != "evidence.txt" {
		t.Fatalf("attachment target/name = %d/%q, want %d/evidence.txt", itemID, originalFilename, response.ItemID)
	}
	if uploaderID.Valid {
		t.Fatalf("uploaded_by = %d, want NULL for anonymous submission", uploaderID.Int64)
	}
	if mimeType != "text/plain; charset=utf-8" {
		t.Fatalf("mime_type = %q, want text/plain; charset=utf-8", mimeType)
	}
	stored, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read stored attachment: %v", err)
	}
	if !bytes.Equal(stored, content) {
		t.Fatalf("stored content = %q, want %q", stored, content)
	}
	rootPrefix := filepath.Clean(fixture.attachmentPath) + string(os.PathSeparator)
	if !strings.HasPrefix(filepath.Clean(filePath), rootPrefix) {
		t.Fatalf("stored path %q is outside attachment root %q", filePath, fixture.attachmentPath)
	}
}

func TestPublicFormRejectsSpoofedAttachmentBeforeCreatingItem(t *testing.T) {
	fixture := newPublicFormAttachmentFixture(t, t.TempDir(), true)

	recorder := fixture.submit(t, "spoofed.png", []byte("this is not a png\n"))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if got := fixture.rowCount(t, "items"); got != 0 {
		t.Fatalf("item count = %d, want 0 after attachment validation failure", got)
	}
	if got := fixture.rowCount(t, "attachments"); got != 0 {
		t.Fatalf("attachment count = %d, want 0 after attachment validation failure", got)
	}
}

func TestPublicFormRejectsAttachmentOutsideMimeAllowlistBeforeCreatingItem(t *testing.T) {
	fixture := newPublicFormAttachmentFixture(t, t.TempDir(), true)
	pngSignature := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

	recorder := fixture.submit(t, "disallowed.png", pngSignature)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if got := fixture.rowCount(t, "items"); got != 0 {
		t.Fatalf("item count = %d, want 0 after MIME allowlist rejection", got)
	}
	if got := fixture.rowCount(t, "attachments"); got != 0 {
		t.Fatalf("attachment count = %d, want 0 after MIME allowlist rejection", got)
	}
}

func TestPublicFormRejectsOversizedAttachmentBeforeCreatingItem(t *testing.T) {
	fixture := newPublicFormAttachmentFixture(t, t.TempDir(), true)
	oversized := bytes.Repeat([]byte{'a'}, publicFormMaxAttachmentBytes+1)

	recorder := fixture.submit(t, "oversized.txt", oversized)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if got := fixture.rowCount(t, "items"); got != 0 {
		t.Fatalf("item count = %d, want 0 after size-limit rejection", got)
	}
	if got := fixture.rowCount(t, "attachments"); got != 0 {
		t.Fatalf("attachment count = %d, want 0 after size-limit rejection", got)
	}
}

func TestPublicFormRejectsAttachmentWhenFormHasNotOptedIn(t *testing.T) {
	fixture := newPublicFormAttachmentFixture(t, t.TempDir(), false)

	recorder := fixture.submit(t, "forged.txt", []byte("forged attachment\n"))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Attachments are not enabled for this form") {
		t.Fatalf("body = %s, want form attachment denial", recorder.Body.String())
	}
	if got := fixture.rowCount(t, "items"); got != 0 {
		t.Fatalf("item count = %d, want 0 after form opt-in denial", got)
	}
	if got := fixture.rowCount(t, "attachments"); got != 0 {
		t.Fatalf("attachment count = %d, want 0 after form opt-in denial", got)
	}
}

func TestPublicFormAttachmentStorageFailureRollsBackAndRetryCreatesOneItem(t *testing.T) {
	tempDir := t.TempDir()
	blockedPath := filepath.Join(tempDir, "attachment-root")
	if err := os.WriteFile(blockedPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("create blocked attachment path: %v", err)
	}
	fixture := newPublicFormAttachmentFixture(t, blockedPath, true)
	content := []byte("retry-safe evidence\n")

	failed := fixture.submit(t, "retry.txt", content)

	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d, want 500; body=%s", failed.Code, failed.Body.String())
	}
	if got := fixture.rowCount(t, "items"); got != 0 {
		t.Fatalf("item count after failed storage = %d, want 0", got)
	}
	if got := fixture.rowCount(t, "attachments"); got != 0 {
		t.Fatalf("attachment count after failed storage = %d, want 0", got)
	}

	if err := os.Remove(blockedPath); err != nil {
		t.Fatalf("remove blocked attachment path: %v", err)
	}
	if err := os.Mkdir(blockedPath, 0o750); err != nil {
		t.Fatalf("create attachment directory: %v", err)
	}
	retried := fixture.submit(t, "retry.txt", content)

	if retried.Code != http.StatusCreated {
		t.Fatalf("retry status = %d, want 201; body=%s", retried.Code, retried.Body.String())
	}
	if got := fixture.rowCount(t, "items"); got != 1 {
		t.Fatalf("item count after retry = %d, want 1", got)
	}
	if got := fixture.rowCount(t, "attachments"); got != 1 {
		t.Fatalf("attachment count after retry = %d, want 1", got)
	}
}
