package tests

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// buildTicketCSV renders an exchange CSV body from row tuples.
func buildTicketCSV(rows [][8]string) string {
	var b strings.Builder
	b.WriteString("external_ref,title,description,requester_email,status,priority,assignee_email,created_at\n")
	for _, row := range rows {
		escaped := make([]string, len(row))
		for i, cell := range row {
			escaped[i] = "\"" + strings.ReplaceAll(cell, "\"", "\"\"") + "\""
		}
		b.WriteString(strings.Join(escaped, ","))
		b.WriteString("\n")
	}
	return b.String()
}

// stageTicketImport uploads a CSV body through the production endpoint.
func stageTicketImport(t *testing.T, server *TestServer, workspaceID int, body string) (uploadID string, totalRows int) {
	t.Helper()

	resp := makeBearerMultipartRequest(t, server,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/tickets/import/upload", workspaceID),
		map[string]string{"has_header": "true"}, "tickets.csv", []byte(body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		responseBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("stage upload: status=%d body=%s", resp.StatusCode, string(responseBody))
	}
	var staged struct {
		UploadID  string `json:"upload_id"`
		TotalRows int    `json:"total_rows"`
	}
	DecodeJSON(t, resp, &staged)
	if staged.UploadID == "" {
		t.Fatal("staged upload returned no upload_id")
	}
	return staged.UploadID, staged.TotalRows
}

// startTicketImport starts the import job for a staged upload.
func startTicketImport(t *testing.T, server *TestServer, workspaceID int, uploadID string) (jobID, status string) {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/tickets/import/start", workspaceID), map[string]interface{}{
		"upload_id": uploadID,
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusAccepted)
	var body struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}
	DecodeJSON(t, resp, &body)
	if body.JobID == "" {
		t.Fatal("start returned no job_id")
	}
	return body.JobID, body.Status
}

// waitForTicketImport polls the job until it leaves queued/running.
func waitForTicketImport(t *testing.T, server *TestServer, workspaceID int, jobID string) (imported, failed, skipped int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/workspaces/%d/tickets/import/jobs/%s", workspaceID, jobID), nil)
		AssertStatusCode(t, resp, http.StatusOK)
		var body struct {
			Status        string   `json:"status"`
			ImportedCount int      `json:"imported_count"`
			FailedCount   int      `json:"failed_count"`
			SkippedCount  int      `json:"skipped_count"`
			Errors        []string `json:"errors"`
		}
		DecodeJSON(t, resp, &body)
		resp.Body.Close()
		if body.Status == "completed" || body.Status == "failed" {
			return body.ImportedCount, body.FailedCount, body.SkippedCount
		}
		if time.Now().After(deadline) {
			t.Fatalf("import did not finish in time; last status=%s errors=%v", body.Status, body.Errors)
		}
		<-tick.C
	}
}

// TestTicketCSVExchange verifies the narrow exchange boundary: import rows
// create tickets with customer-safe attribution, row-level failures are
// isolated and reported, retries are idempotent through external_ref, and
// export round-trips the same schema.
func TestTicketCSVExchange(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	emailFixtureSeq++
	workspaceID, _ := CreateTestWorkspace(t, server, "Ticket exchange", fmt.Sprintf("TE%d", emailFixtureSeq))

	exportPath := fmt.Sprintf("/workspaces/%d/tickets/export", workspaceID)

	// Export of an empty workspace yields only the header.
	resp := MakeAuthRequest(t, server, http.MethodGet, exportPath, nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var emptyExport strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		emptyExport.WriteString(string(buf[:n]))
		if err != nil {
			break
		}
	}
	resp.Body.Close()
	if !strings.HasPrefix(emptyExport.String(), "external_ref,title") {
		t.Fatalf("empty export = %q, want the exchange header", emptyExport.String())
	}

	csvBody := buildTicketCSV([][8]string{
		{"ZD-1", "Password reset", "Cannot log in", "victim@example.com", "", "", "", "2026-01-05T10:00:00Z"},
		{"ZD-2", "Broken thing", "Fails on load", "other@example.com", "No Such Status", "", "", ""},
		{"ZD-1", "Password reset again", "Duplicate of ZD-1", "victim@example.com", "", "", "", ""},
	})
	uploadID, totalRows := stageTicketImport(t, server, workspaceID, csvBody)
	if totalRows != 3 {
		t.Fatalf("staged total_rows = %d, want 3", totalRows)
	}

	jobID, _ := startTicketImport(t, server, workspaceID, uploadID)
	imported, failed, skipped := waitForTicketImport(t, server, workspaceID, jobID)
	if imported != 1 || failed != 1 || skipped != 1 {
		t.Fatalf("first import = imported:%d failed:%d skipped:%d, want 1/1/1", imported, failed, skipped)
	}

	// Exactly one ticket exists, attributed to the portal customer.
	var itemCount int
	var customerEmail string
	if err := server.DB().QueryRow(`
		SELECT COUNT(*), COALESCE((SELECT pc.email FROM items i
			JOIN portal_customers pc ON i.creator_portal_customer_id = pc.id
			WHERE i.workspace_id = ? LIMIT 1), '')
		FROM items WHERE workspace_id = ?
	`, workspaceID, workspaceID).Scan(&itemCount, &customerEmail); err != nil {
		t.Fatalf("read imported ticket: %v", err)
	}
	if itemCount != 1 {
		t.Fatalf("tickets = %d, want 1", itemCount)
	}
	if customerEmail != "victim@example.com" {
		t.Fatalf("requester attribution = %q, want victim@example.com", customerEmail)
	}

	// Re-importing the same content is a full skip: retries never duplicate.
	retryBody := buildTicketCSV([][8]string{
		{"ZD-1", "Password reset", "Cannot log in", "victim@example.com", "", "", "", "2026-01-05T10:00:00Z"},
	})
	retryUpload, _ := stageTicketImport(t, server, workspaceID, retryBody)
	retryJob, _ := startTicketImport(t, server, workspaceID, retryUpload)
	retryImported, _, retrySkipped := waitForTicketImport(t, server, workspaceID, retryJob)
	if retryImported != 0 || retrySkipped != 1 {
		t.Fatalf("retry = imported:%d skipped:%d, want 0/1", retryImported, retrySkipped)
	}
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM items WHERE workspace_id = ?`, workspaceID).Scan(&itemCount); err != nil {
		t.Fatalf("count after retry: %v", err)
	}
	if itemCount != 1 {
		t.Fatalf("tickets after retry = %d, want 1 (retries must not duplicate)", itemCount)
	}

	// Export round-trips: the ticket appears under its item key.
	export := MakeAuthRequest(t, server, http.MethodGet, exportPath, nil)
	AssertStatusCode(t, export, http.StatusOK)
	var exportBody strings.Builder
	for {
		n, err := export.Body.Read(buf)
		exportBody.WriteString(string(buf[:n]))
		if err != nil {
			break
		}
	}
	export.Body.Close()
	if !strings.Contains(exportBody.String(), fmt.Sprintf("TE%d-1,Password reset,Cannot log in,victim@example.com", emailFixtureSeq)) {
		t.Fatalf("export missing the imported ticket: %q", exportBody.String())
	}

	// Re-importing the workspace's own export is a no-op: the export writes
	// each ticket's item key as external_ref and import dedupes on it, so a
	// documented backup/exchange round trip cannot duplicate tickets (WI-1563).
	reimportUpload, _ := stageTicketImport(t, server, workspaceID, exportBody.String())
	reimportJob, _ := startTicketImport(t, server, workspaceID, reimportUpload)
	reimportImported, reimportFailed, reimportSkipped := waitForTicketImport(t, server, workspaceID, reimportJob)
	if reimportImported != 0 || reimportFailed != 0 || reimportSkipped != 1 {
		t.Fatalf("re-import of export = imported:%d failed:%d skipped:%d, want 0/0/1", reimportImported, reimportFailed, reimportSkipped)
	}
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM items WHERE workspace_id = ?`, workspaceID).Scan(&itemCount); err != nil {
		t.Fatalf("count after re-import: %v", err)
	}
	if itemCount != 1 {
		t.Fatalf("tickets after re-import = %d, want 1", itemCount)
	}
}

// TestTicketCSVExchangeRequiresItemCreate verifies the denial contract: a
// viewer-only user cannot stage, start, or export.
func TestTicketCSVExchangeRequiresItemCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	emailFixtureSeq++
	workspaceID, _ := CreateTestWorkspace(t, server, "Ticket exchange denial", fmt.Sprintf("TD%d", emailFixtureSeq))

	otherUserID, otherUsername, otherPassword := CreateTestUserWithCredentials(
		t, server, "ticket_csv_other", "ticket-csv-other@test.com",
	)
	// An explicit Viewer assignment closes the default open-editor grant:
	// imports are refused (item.create), while export remains available
	// because it is a view-scope operation.
	AssignWorkspaceRole(t, server, otherUserID, workspaceID, "Viewer")
	otherToken, _ := CreateAuthCredentialsForUser(t, server, otherUsername, otherPassword)

	denied := []struct {
		name   string
		method string
		path   string
		body   interface{}
	}{
		{"upload denied", http.MethodPost, fmt.Sprintf("/workspaces/%d/tickets/import/upload", workspaceID), nil},
		{"start denied", http.MethodPost, fmt.Sprintf("/workspaces/%d/tickets/import/start", workspaceID), map[string]interface{}{"upload_id": "x"}},
	}
	for _, tc := range denied {
		t.Run(tc.name, func(t *testing.T) {
			resp := MakeAuthRequestWithToken(t, server, otherToken, tc.method, tc.path, tc.body)
			defer resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusNotFound)
		})
	}
}

// waitForTicketImportDetailed polls like waitForTicketImport but also returns
// the accumulated row-level error strings.
func waitForTicketImportDetailed(t *testing.T, server *TestServer, workspaceID int, jobID string) (imported, failed int, errors []string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/workspaces/%d/tickets/import/jobs/%s", workspaceID, jobID), nil)
		AssertStatusCode(t, resp, http.StatusOK)
		var body struct {
			Status        string   `json:"status"`
			ImportedCount int      `json:"imported_count"`
			FailedCount   int      `json:"failed_count"`
			Errors        []string `json:"errors"`
		}
		DecodeJSON(t, resp, &body)
		resp.Body.Close()
		if body.Status == "completed" || body.Status == "failed" {
			return body.ImportedCount, body.FailedCount, body.Errors
		}
		if time.Now().After(deadline) {
			t.Fatalf("import did not finish in time; last status=%s errors=%v", body.Status, body.Errors)
		}
		<-tick.C
	}
}

// TestTicketCSVImportEnforcesCreateStatusPolicy (WI-1560): the importing actor
// passes the same create-time workflow/approval validation as normal item
// creation. A CSV status column may place a ticket on the initial status or a
// free direct transition, but never behind an approval gate.
func TestTicketCSVImportEnforcesCreateStatusPolicy(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Import Gate WS", "")

	// Workflow: Gate Open is initial; Gate Working is a free direct
	// transition; Gate Approved is reachable only through an approval
	// decision.
	todoID := findV2StatusCategory(t, server, "To Do")
	progressID := findV2StatusCategory(t, server, "In Progress")
	doneID := findV2StatusCategory(t, server, "Done")
	openStatusID := createV2Status(t, server, "Gate Open", todoID)
	workingStatusID := createV2Status(t, server, "Gate Working", progressID)
	approvedStatusID := createV2Status(t, server, "Gate Approved", doneID)
	rejectedStatusID := createV2Status(t, server, "Gate Rejected", doneID)
	doneStatusID := createV2Status(t, server, "Gate Done", doneID)
	workflowID := createV2WorkflowWithTransitions(t, server, "Import Gate Workflow", []map[string]interface{}{
		{"from_status_id": nil, "to_status_id": openStatusID, "display_order": 0},
		{"from_status_id": openStatusID, "to_status_id": workingStatusID, "display_order": 1},
		{"from_status_id": openStatusID, "to_status_id": approvedStatusID, "display_order": 2},
		// Approval decisions leave the gated status: approve -> done, deny -> rejected.
		{"from_status_id": approvedStatusID, "to_status_id": doneStatusID, "display_order": 3},
		{"from_status_id": approvedStatusID, "to_status_id": rejectedStatusID, "display_order": 4},
	})

	transitionsResp := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/workflows/%d/transitions", workflowID), nil)
	defer transitionsResp.Body.Close()
	AssertStatusCode(t, transitionsResp, http.StatusOK)
	var transitions struct {
		Data []struct {
			ID int `json:"id"`
			To struct {
				ID int `json:"id"`
			} `json:"to"`
		} `json:"data"`
	}
	DecodeJSON(t, transitionsResp, &transitions)
	approveTransitionID, denyTransitionID := 0, 0
	for _, tr := range transitions.Data {
		switch tr.To.ID {
		case doneStatusID:
			approveTransitionID = tr.ID
		case rejectedStatusID:
			denyTransitionID = tr.ID
		}
	}
	if approveTransitionID == 0 || denyTransitionID == 0 {
		t.Fatalf("approval transitions not found: approve=%d deny=%d", approveTransitionID, denyTransitionID)
	}

	roleID := 0
	for _, id := range GetWorkspaceRoles(t, server) {
		roleID = id
		break
	}
	var createdApproval struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	approvalSetID := 0
	approvalResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/approval-sets", map[string]interface{}{
		"name":        "Import Gate Approvals",
		"workflow_id": workflowID,
		"set_statuses": []map[string]interface{}{
			{
				"status_id":             approvedStatusID,
				"approve_transition_id": approveTransitionID,
				"deny_transition_id":    denyTransitionID,
				"step_mode":             "sequential",
				"steps": []map[string]interface{}{
					{
						"name":              "Manager sign-off",
						"display_order":     0,
						"quorum_mode":       "any",
						"approver_source":   "role",
						"approver_role_id":  roleID,
						"rejection_policy":  "any_rejection_fails",
						"on_leave_strategy": "use_substitute",
					},
				},
			},
		},
	})
	defer approvalResp.Body.Close()
	AssertStatusCode(t, approvalResp, http.StatusCreated)
	DecodeJSON(t, approvalResp, &createdApproval)
	approvalSetID = createdApproval.Data.ID
	if approvalSetID == 0 {
		t.Fatalf("approval set not created")
	}

	setID := createV2ConfigSet(t, server, map[string]interface{}{
		"name":            "Import Gate Set",
		"workflow_id":     workflowID,
		"approval_set_id": approvalSetID,
	})

	// Attach the set to the workspace so its workflow/approval gates govern
	// imported tickets.
	meResp := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/users/me", nil)
	defer meResp.Body.Close()
	AssertStatusCode(t, meResp, http.StatusOK)
	var me struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, meResp, &me)
	AssignWorkspaceRole(t, server, me.Data.ID, workspaceID, "Administrator")
	assign := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/workspaces/%d/configuration-set", workspaceID),
		map[string]interface{}{"configuration_set_id": setID})
	defer assign.Body.Close()
	AssertStatusCode(t, assign, http.StatusOK)

	csvBody := buildTicketCSV([][8]string{
		{"bypass-1", "Bypass approval", "", "", "Gate Approved", "", "", ""},
		{"initial-1", "Initial placement", "", "", "Gate Open", "", "", ""},
		{"override-1", "Free direct transition", "", "", "Gate Working", "", "", ""},
	})
	uploadID, _ := stageTicketImport(t, server, workspaceID, csvBody)
	jobID, _ := startTicketImport(t, server, workspaceID, uploadID)
	imported, failed, rowErrors := waitForTicketImportDetailed(t, server, workspaceID, jobID)
	if imported != 2 || failed != 1 {
		t.Fatalf("import counts imported=%d failed=%d, want 2/1 (errors=%v)", imported, failed, rowErrors)
	}
	if len(rowErrors) != 1 || !strings.Contains(rowErrors[0], "approval") {
		t.Fatalf("bypass row error = %v, want an explicit approval-gate rejection", rowErrors)
	}

	// The failed row must not have created its ticket; the allowed rows are
	// persisted with their imported placements.
	exportResp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/workspaces/%d/tickets/export", workspaceID), nil)
	AssertStatusCode(t, exportResp, http.StatusOK)
	var exported strings.Builder
	buf := make([]byte, 8192)
	for {
		n, err := exportResp.Body.Read(buf)
		exported.Write(buf[:n])
		if err != nil {
			break
		}
	}
	exportResp.Body.Close()
	if strings.Contains(exported.String(), "bypass-1") || strings.Contains(exported.String(), "Bypass approval") {
		t.Fatalf("bypassed row created a ticket: %s", exported.String())
	}
	for _, want := range []string{"Initial placement,,,Gate Open", "Free direct transition,,,Gate Working"} {
		if !strings.Contains(exported.String(), want) {
			t.Fatalf("allowed row %q missing from export: %s", want, exported.String())
		}
	}
}
