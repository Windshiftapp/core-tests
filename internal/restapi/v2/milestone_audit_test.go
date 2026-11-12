//go:build test

package v2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

// TestMilestoneMutationsRecordAudit covers the WI-1898 recorder contract for the
// milestone comment and page-link routes: each mutation must queue one audit
// event with the request actor before the handler returns.
func TestMilestoneMutationsRecordAudit(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	seed := tdb.SeedTestData(t)
	auditor := logger.NewAuditor(tdb.GetDatabase())

	run := func(t *testing.T, method, milestoneID string, pathValues map[string]string, operation commandOperation) {
		t.Helper()
		recorder := logger.NewAuditRecorder()
		request := httptest.NewRequest(method, "/api/v2/milestones/"+milestoneID, nil)
		request.SetPathValue("milestone_id", milestoneID)
		for name, value := range pathValues {
			request.SetPathValue(name, value)
		}
		request = request.WithContext(context.WithValue(request.Context(), contextkeys.User, &models.User{ID: seed.UserID, Username: "ada"}))
		request = request.WithContext(logger.WithAuditRecorder(request.Context(), recorder))
		if err := operation(request); err != nil {
			t.Fatalf("%s milestone %s: %v", method, milestoneID, err)
		}
		auditor.Flush(recorder, true)
	}

	run(t, http.MethodPost, "5", nil, func(r *http.Request) error {
		_, err := createMilestoneComment(fakeMilestoneComments{})(r, milestoneCommentCreateRequest{Content: "hello"})
		return err
	})
	run(t, http.MethodPatch, "5", map[string]string{"comment_id": "9"}, func(r *http.Request) error {
		_, err := updateMilestoneComment(fakeMilestoneComments{})(r, milestoneCommentPatchRequest{Content: Optional[string]{Set: true, Value: "edited"}})
		return err
	})
	run(t, http.MethodDelete, "5", map[string]string{"comment_id": "9"}, deleteMilestoneComment(fakeMilestoneComments{}))
	run(t, http.MethodPost, "5", nil, func(r *http.Request) error {
		_, err := createMilestonePageLink(fakeMilestonePageLinks{})(r, milestonePageLinkCreateRequest{PageID: 7})
		return err
	})
	run(t, http.MethodDelete, "5", map[string]string{"link_id": "3"}, deleteMilestonePageLink(fakeMilestonePageLinks{}))

	rows, err := tdb.Query(`SELECT action_type, resource_type FROM audit_logs WHERE resource_type IN (?, ?) ORDER BY id`,
		logger.ResourceMilestoneComment, logger.ResourceMilestonePageLink)
	if err != nil {
		t.Fatalf("query audit rows: %v", err)
	}
	defer func() { _ = rows.Close() }()

	type auditRow struct{ action, resource string }
	var got []auditRow
	for rows.Next() {
		var row auditRow
		if err := rows.Scan(&row.action, &row.resource); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit rows: %v", err)
	}

	want := []auditRow{
		{logger.ActionMilestoneCommentCreate, logger.ResourceMilestoneComment},
		{logger.ActionMilestoneCommentUpdate, logger.ResourceMilestoneComment},
		{logger.ActionMilestoneCommentDelete, logger.ResourceMilestoneComment},
		{logger.ActionMilestonePageLinkCreate, logger.ResourceMilestonePageLink},
		{logger.ActionMilestonePageLinkDelete, logger.ResourceMilestonePageLink},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("audit rows = %v, want %v", got, want)
	}
}
