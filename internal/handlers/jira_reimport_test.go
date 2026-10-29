package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/database"
	"windshift/internal/jira"
	"windshift/internal/validation"
)

type fakeJiraAttachmentImportClient struct {
	jira.Client
}

func (f *fakeJiraAttachmentImportClient) DownloadAttachment(
	_ context.Context,
	_ string,
) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader("attachment evidence\n")), "text/plain", nil
}

func TestForcedJiraReimportUpdatesExistingItemByStableJiraID(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-reimport-test-secret", "", blobstore.Disabled())

	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)
	issue := jira.JiraIssue{
		ID:  "10001",
		Key: "APP-1",
		Fields: jira.JiraIssueFields{
			Summary:   "Original summary",
			IssueType: &jira.JiraIssueType{ID: "type"},
			Status:    &jira.JiraStatus{ID: "status"},
			Comment:   &jira.JiraCommentContainer{Comments: []jira.JiraComment{{ID: "comment-10001", Body: "Original comment"}}},
		},
	}
	importJiraReimportIssue(t, handler, "job-jsm", workspaceID, itemTypeID, statusID, &issue, false)

	var originalID int
	var originalFracIndex string
	if err := db.QueryRow(`
		SELECT id, frac_index FROM items WHERE workspace_id = ?
	`, workspaceID).Scan(&originalID, &originalFracIndex); err != nil {
		t.Fatalf("load original item: %v", err)
	}

	insertJiraImportJob(t, db, "job-reimport", "conn-jsm", "running", []string{"APP"})
	issue.Fields.Summary = "Updated from Jira"
	issue.Fields.Comment.Comments[0].Body = "Updated comment"
	importJiraReimportIssue(t, handler, "job-reimport", workspaceID, itemTypeID, statusID, &issue, true)

	var count, updatedID int
	var title, updatedFracIndex string
	if err := db.QueryRow(`
		SELECT COUNT(*), MIN(id), MIN(title), MIN(frac_index)
		FROM items WHERE workspace_id = ?
	`, workspaceID).Scan(&count, &updatedID, &title, &updatedFracIndex); err != nil {
		t.Fatalf("load reimported item: %v", err)
	}
	if count != 1 || updatedID != originalID || title != "Updated from Jira" {
		t.Fatalf("reimport result count=%d id=%d title=%q, want one updated item %d", count, updatedID, title, originalID)
	}
	if updatedFracIndex == originalFracIndex {
		t.Fatalf("frac_index was not regenerated for ranked re-import: %q", updatedFracIndex)
	}
	var commentCount int
	var commentContent string
	if err := db.QueryRow(`
		SELECT COUNT(*), MIN(content) FROM comments WHERE item_id = ?
	`, originalID).Scan(&commentCount, &commentContent); err != nil {
		t.Fatalf("load reimported comment: %v", err)
	}
	if commentCount != 1 || commentContent != "Updated comment" {
		t.Fatalf("reimported comment count=%d content=%q", commentCount, commentContent)
	}

	var oldMetadataJSON, newMetadataJSON string
	if err := db.QueryRow(`
		SELECT metadata_json FROM jira_import_id_mappings
		WHERE job_id = 'job-jsm' AND entity_type = 'item' AND jira_id = '10001'
	`).Scan(&oldMetadataJSON); err != nil {
		t.Fatalf("load old mapping metadata: %v", err)
	}
	if err := db.QueryRow(`
		SELECT metadata_json FROM jira_import_id_mappings
		WHERE job_id = 'job-reimport' AND entity_type = 'item' AND jira_id = '10001'
	`).Scan(&newMetadataJSON); err != nil {
		t.Fatalf("load new mapping metadata: %v", err)
	}
	var oldMetadata, newMetadata map[string]interface{}
	if err := json.Unmarshal([]byte(oldMetadataJSON), &oldMetadata); err != nil {
		t.Fatalf("decode old metadata: %v", err)
	}
	if err := json.Unmarshal([]byte(newMetadataJSON), &newMetadata); err != nil {
		t.Fatalf("decode new metadata: %v", err)
	}
	if oldMetadata["was_created"] != false || oldMetadata["superseded_by_job_id"] != "job-reimport" {
		t.Fatalf("old ownership metadata = %#v", oldMetadata)
	}
	if newMetadata["was_created"] != true ||
		newMetadata["action"] != "update_existing" ||
		newMetadata["reimported_from_job_id"] != "job-jsm" {
		t.Fatalf("new ownership metadata = %#v", newMetadata)
	}
}

func TestJiraImportBoundsOversizedCommentWithoutDroppingIt(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-comment-bound-test-secret", "", blobstore.Disabled())
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)
	source := strings.Repeat("x", validation.MarkdownMaxBytes-1) + "øtrailing"
	issue := jira.JiraIssue{
		ID:  "10002",
		Key: "APP-2",
		Fields: jira.JiraIssueFields{
			Summary:   "Issue with oversized comment",
			IssueType: &jira.JiraIssueType{ID: "type"},
			Status:    &jira.JiraStatus{ID: "status"},
			Comment: &jira.JiraCommentContainer{Comments: []jira.JiraComment{{
				ID:   "comment-10002",
				Body: source,
			}}},
		},
	}

	importJiraReimportIssue(t, handler, "job-jsm", workspaceID, itemTypeID, statusID, &issue, false)

	var itemCount, commentCount, contentLength int
	if err := db.QueryRow("SELECT COUNT(*) FROM items WHERE workspace_id = ?", workspaceID).Scan(&itemCount); err != nil {
		t.Fatalf("count imported items: %v", err)
	}
	if err := db.QueryRow(`
		SELECT COUNT(*), COALESCE(MAX(LENGTH(c.content)), 0)
		FROM comments c
		JOIN items i ON i.id = c.item_id
		WHERE i.workspace_id = ?
	`, workspaceID).Scan(&commentCount, &contentLength); err != nil {
		t.Fatalf("load imported comment: %v", err)
	}
	if itemCount != 1 || commentCount != 1 || contentLength != validation.MarkdownMaxBytes-1 {
		t.Fatalf("imported items=%d comments=%d content bytes=%d", itemCount, commentCount, contentLength)
	}
}

func TestJiraLinksOutsideImportScopeBecomeExternalIssueLinks(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-external-link-test-secret", "", blobstore.Disabled())
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)

	issue := jira.JiraIssue{
		ID:  "10001",
		Key: "APP-1",
		Fields: jira.JiraIssueFields{
			Summary:   "Linked issue",
			IssueType: &jira.JiraIssueType{ID: "type"},
			Status:    &jira.JiraStatus{ID: "status"},
			IssueLinks: []jira.JiraIssueLink{{
				ID: "link-1",
				Type: &jira.JiraLinkType{
					ID:      "10",
					Name:    "Blocks",
					Outward: "blocks",
					Inward:  "is blocked by",
				},
				OutwardIssue: &jira.JiraIssue{ID: "999", Key: "OTHER-9"},
			}},
		},
	}
	importJiraReimportIssue(t, handler, "job-jsm", workspaceID, itemTypeID, statusID, &issue, false)
	handler.importIssueLinks("job-jsm")
	handler.importIssueLinks("job-jsm")

	var count int
	var externalID, externalURL, title, providerType, metadataJSON string
	if err := db.QueryRow(`
		SELECT COUNT(*), MIN(l.external_id), MIN(l.external_url), MIN(l.title),
		       MIN(p.provider_type), MIN(l.link_metadata)
		FROM item_integration_links l
		JOIN integration_providers p ON p.id = l.integration_provider_id
	`).Scan(&count, &externalID, &externalURL, &title, &providerType, &metadataJSON); err != nil {
		t.Fatalf("load external Jira issue link: %v", err)
	}
	if count != 1 || externalID != "OTHER-9" ||
		externalURL != "https://example.invalid/browse/OTHER-9" ||
		title != "blocks: OTHER-9" || providerType != "jira" {
		t.Fatalf("external link count=%d id=%q url=%q title=%q provider=%q",
			count, externalID, externalURL, title, providerType)
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("decode link metadata: %v", err)
	}
	if metadata["direction"] != "outward" ||
		metadata["jira_link_type"] != "Blocks" ||
		metadata["local_issue_key"] != "APP-1" {
		t.Fatalf("external link metadata = %#v", metadata)
	}
}

func TestForcedJiraReimportTransfersInternalLinkOwnershipWithoutDuplicates(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-link-reimport-test-secret", "", blobstore.Disabled())
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)
	source := jira.JiraIssue{
		ID:  "10011",
		Key: "APP-11",
		Fields: jira.JiraIssueFields{
			Summary:   "Source",
			IssueType: &jira.JiraIssueType{ID: "type"},
			Status:    &jira.JiraStatus{ID: "status"},
			IssueLinks: []jira.JiraIssueLink{{
				Type:         &jira.JiraLinkType{Name: "Blocks", Outward: "blocks"},
				OutwardIssue: &jira.JiraIssue{ID: "10012", Key: "APP-12"},
			}},
		},
	}
	target := jira.JiraIssue{
		ID:  "10012",
		Key: "APP-12",
		Fields: jira.JiraIssueFields{
			Summary:   "Target",
			IssueType: &jira.JiraIssueType{ID: "type"},
			Status:    &jira.JiraStatus{ID: "status"},
		},
	}
	for _, issue := range []*jira.JiraIssue{&source, &target} {
		importJiraReimportIssue(t, handler, "job-jsm", workspaceID, itemTypeID, statusID, issue, false)
	}
	handler.importIssueLinks("job-jsm")

	insertJiraImportJob(t, db, "job-link-reimport", "conn-jsm", "running", []string{"APP"})
	for _, issue := range []*jira.JiraIssue{&source, &target} {
		importJiraReimportIssue(t, handler, "job-link-reimport", workspaceID, itemTypeID, statusID, issue, true)
	}
	handler.importIssueLinks("job-link-reimport")

	var linkCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_links`).Scan(&linkCount); err != nil {
		t.Fatalf("count item links: %v", err)
	}
	if linkCount != 1 {
		t.Fatalf("item link count = %d, want 1", linkCount)
	}
	var oldMetadataJSON, newMetadataJSON string
	for jobID, destination := range map[string]*string{
		"job-jsm":           &oldMetadataJSON,
		"job-link-reimport": &newMetadataJSON,
	} {
		if err := db.QueryRow(`
			SELECT metadata_json FROM jira_import_id_mappings
			WHERE job_id = ? AND entity_type = 'link'
		`, jobID).Scan(destination); err != nil {
			t.Fatalf("load %s link mapping: %v", jobID, err)
		}
	}
	var oldMetadata, newMetadata map[string]interface{}
	if err := json.Unmarshal([]byte(oldMetadataJSON), &oldMetadata); err != nil {
		t.Fatalf("decode old link metadata: %v", err)
	}
	if err := json.Unmarshal([]byte(newMetadataJSON), &newMetadata); err != nil {
		t.Fatalf("decode new link metadata: %v", err)
	}
	if oldMetadata["was_created"] != false ||
		newMetadata["was_created"] != true ||
		newMetadata["reimported_from_job_id"] != "job-jsm" {
		t.Fatalf("link ownership old=%#v new=%#v", oldMetadata, newMetadata)
	}
}

func TestJiraWatchersBecomeItemWatchesAndVotesAndSecurityRemainRetrievable(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-watchers-test-secret", "", blobstore.Disabled())
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)

	var userID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('watcher@example.invalid', 'jira-watcher', 'Jira', 'Watcher', true)
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("create watcher user: %v", err)
	}

	issue := jira.JiraIssue{
		ID:  "10002",
		Key: "APP-2",
		Fields: jira.JiraIssueFields{
			Summary:   "Restricted watched issue",
			IssueType: &jira.JiraIssueType{ID: "type"},
			Status:    &jira.JiraStatus{ID: "status"},
			Watches:   &jira.JiraWatchSummary{WatchCount: 2},
			Watchers: []jira.JiraUser{
				{AccountID: "watcher-1", DisplayName: "Jira Watcher"},
				{DisplayName: "Deleted watcher"},
			},
			WatcherIdentitiesAvailable: true,
			Votes: &jira.JiraVoteSummary{
				Votes: 3,
				Voters: []jira.JiraUser{{
					AccountID:   "voter-1",
					DisplayName: "Jira Voter",
				}},
			},
			Security: &jira.JiraIssueSecurityLevel{
				ID:          "security-1",
				Name:        "Management",
				Description: "Managers only",
			},
		},
	}
	im := &jiraImportContext{
		jobID:             "job-jsm",
		progress:          &ImportProgress{},
		workspaceID:       workspaceID,
		statusMap:         map[string]int{"status": statusID},
		itemTypeMap:       map[string]int{"type": itemTypeID},
		userMap:           map[string]int{"watcher-1": userID},
		usernameMap:       map[string]string{},
		portalCustomerMap: map[string]int{},
		versionMap:        map[string]int{},
		iterationMap:      map[string]int{},
		customFieldIDMap:  map[string]int{},
		choiceOptionIDs:   map[string]map[string]int{},
	}
	if err := handler.importIssue(t.Context(), im, &issue); err != nil {
		t.Fatalf("import issue: %v", err)
	}

	var itemID int
	var customFieldValuesJSON string
	if err := db.QueryRow(`
		SELECT id, custom_field_values FROM items WHERE workspace_id = ?
	`, workspaceID).Scan(&itemID, &customFieldValuesJSON); err != nil {
		t.Fatalf("load imported item: %v", err)
	}
	var active bool
	if err := db.QueryRow(`
		SELECT is_active FROM item_watches WHERE user_id = ? AND item_id = ?
	`, userID, itemID).Scan(&active); err != nil {
		t.Fatalf("load imported watch: %v", err)
	}
	if !active {
		t.Fatal("imported Jira watch is inactive")
	}

	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(customFieldValuesJSON), &metadata); err != nil {
		t.Fatalf("decode item metadata: %v", err)
	}
	votes, _ := metadata["_jira_votes"].(map[string]interface{})
	security, _ := metadata["_jira_security_level"].(map[string]interface{})
	watchers, _ := metadata["_jira_watchers"].([]interface{})
	if metadata["_jira_watcher_count"] != float64(2) ||
		metadata["_jira_watcher_identities_available"] != true ||
		len(watchers) != 2 {
		t.Fatalf("watcher metadata = %#v", metadata)
	}
	if votes["count"] != float64(3) || security["name"] != "Management" {
		t.Fatalf("votes=%#v security=%#v", votes, security)
	}

	var mappingMetadataJSON string
	if err := db.QueryRow(`
		SELECT metadata_json FROM jira_import_id_mappings
		WHERE job_id = 'job-jsm' AND entity_type = 'watch' AND jira_id = '10002:watcher-1'
	`).Scan(&mappingMetadataJSON); err != nil {
		t.Fatalf("load imported watch mapping: %v", err)
	}
	var mappingMetadata map[string]interface{}
	if err := json.Unmarshal([]byte(mappingMetadataJSON), &mappingMetadata); err != nil {
		t.Fatalf("decode watch mapping: %v", err)
	}
	if mappingMetadata["was_created"] != true || mappingMetadata["user_id"] != float64(userID) {
		t.Fatalf("watch mapping metadata = %#v", mappingMetadata)
	}
}

func TestJiraADFMediaLinksToImportedAttachmentInItemAndComment(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	attachmentRoot := t.TempDir()
	handler := NewJiraImportHandler(db, "jira-media-test-secret", "", blobstore.NewLocal(attachmentRoot))
	workspaceID, itemTypeID, statusID := createJiraReimportWorkspace(t, db)
	if _, err := db.ExecWrite(`
		INSERT INTO attachment_settings (attachment_path, enabled)
		VALUES (?, true)
	`, attachmentRoot); err != nil {
		t.Fatalf("configure attachments: %v", err)
	}
	adf := map[string]interface{}{
		"type": "doc",
		"content": []interface{}{map[string]interface{}{
			"type": "mediaSingle",
			"content": []interface{}{map[string]interface{}{
				"type": "media",
				"attrs": map[string]interface{}{
					"id":   "attachment-1",
					"type": "file",
				},
			}},
		}},
	}
	issue := jira.JiraIssue{
		ID:  "10003",
		Key: "APP-3",
		Fields: jira.JiraIssueFields{
			Summary:     "Issue with evidence",
			Description: adf,
			IssueType:   &jira.JiraIssueType{ID: "type"},
			Status:      &jira.JiraStatus{ID: "status"},
			Attachment: []jira.JiraAttachment{{
				ID:       "attachment-1",
				Filename: "evidence.txt",
				Content:  "https://example.invalid/attachment-1",
				Author:   &jira.JiraUser{DisplayName: "Deleted uploader"},
			}},
			Comment: &jira.JiraCommentContainer{Comments: []jira.JiraComment{{
				ID:     "comment-1",
				Body:   adf,
				Author: &jira.JiraUser{DisplayName: "Deleted commenter"},
			}}},
		},
	}
	im := &jiraImportContext{
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
		client:            &fakeJiraAttachmentImportClient{},
	}
	importErr := handler.importIssue(t.Context(), im, &issue)
	if importErr != nil {
		t.Fatalf("import issue: %v", importErr)
	}

	var attachmentID int
	var description, comment string
	if err := db.QueryRow(`
		SELECT a.id, i.description, c.content
		FROM items i
		JOIN attachments a ON a.item_id = i.id
		JOIN comments c ON c.item_id = i.id
		WHERE i.workspace_id = ?
	`, workspaceID).Scan(&attachmentID, &description, &comment); err != nil {
		t.Fatalf("load linked media: %v", err)
	}
	want := "/api/attachments/" + fmt.Sprint(attachmentID) + "/download"
	if !strings.Contains(description, want) || !strings.Contains(comment, want) {
		t.Fatalf("description=%q comment=%q, want linked attachment %q", description, comment, want)
	}
	for _, entityType := range []string{"attachment", "comment"} {
		var mappingMetadataJSON string
		if err := db.QueryRow(`
			SELECT metadata_json FROM jira_import_id_mappings
			WHERE job_id = 'job-jsm' AND entity_type = ?
		`, entityType).Scan(&mappingMetadataJSON); err != nil {
			t.Fatalf("load %s identity metadata: %v", entityType, err)
		}
		if !strings.Contains(mappingMetadataJSON, "Deleted ") {
			t.Fatalf("%s metadata = %s, want deleted Jira identity", entityType, mappingMetadataJSON)
		}
	}
}

func createJiraReimportWorkspace(
	t *testing.T,
	db database.Database,
) (workspaceID, itemTypeID, statusID int) {
	t.Helper()
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, active)
		VALUES ('Jira Reimport', 'APP', true)
		RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM item_types ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("find item type: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM statuses ORDER BY id LIMIT 1`).Scan(&statusID); err != nil {
		t.Fatalf("find status: %v", err)
	}
	return workspaceID, itemTypeID, statusID
}

func importJiraReimportIssue(
	t *testing.T,
	handler *JiraImportHandler,
	jobID string,
	workspaceID, itemTypeID, statusID int,
	issue *jira.JiraIssue,
	reimport bool,
) {
	t.Helper()
	im := &jiraImportContext{
		jobID:             jobID,
		forceReimport:     reimport,
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
	}
	if err := handler.importIssue(t.Context(), im, issue); err != nil {
		t.Fatalf("import issue %s: %v", issue.Key, err)
	}
}
