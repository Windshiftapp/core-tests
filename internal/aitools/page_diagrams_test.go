//go:build test

package aitools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/logger"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func TestPageDiagramTools_RegistrationLifecycleAuthorizationAndAudit(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	permissions, err := services.NewPermissionService(tdb.GetDatabase(), services.DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	pageAuth := services.NewPagePermissionService(tdb.GetDatabase(), permissions)
	pageService := services.NewPageService(tdb.GetDatabase())
	pageApplication := services.NewPageApplicationService(pageService, pageAuth)
	pageDiagrams := services.NewPageDiagramService(
		tdb.GetDatabase(),
		t.TempDir(),
		pageApplication,
		pageAuth,
		permissions,
	)
	env := &Env{
		DB:                     tdb.GetDatabase(),
		UserID:                 data.UserID,
		Username:               "testuser",
		Source:                 SourceMCP,
		AccessibleWorkspaceIDs: []int{data.WorkspaceID},
		PermService:            permissions,
		PageApplicationService: pageApplication,
		PageDiagramService:     pageDiagrams,
	}
	page, err := pageApplication.Create(pageAuditActor(env), services.CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Agent diagrams",
		Content:     "# Agent",
	})
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	for name, scopes := range map[string][]string{
		"create_page_diagram": {auth.ScopePagesWrite},
		"list_page_diagrams":  {auth.ScopePagesRead},
		"get_page_diagram":    {auth.ScopePagesRead},
		"update_page_diagram": {auth.ScopePagesWrite},
	} {
		entry, ok := Default.Lookup(name)
		if !ok {
			t.Fatalf("tool %q is not registered", name)
		}
		if !reflect.DeepEqual(entry.Scopes, scopes) {
			t.Fatalf("%s scopes = %v, want %v", name, entry.Scopes, scopes)
		}
		if (name == "create_page_diagram" || name == "update_page_diagram") &&
			!strings.Contains(string(entry.Schema), "expected_content_hash") {
			t.Fatalf("%s schema omitted expected_content_hash: %s", name, entry.Schema)
		}
	}

	createdResult := runPageDiagramTool(t, env, "create_page_diagram", map[string]any{
		"page_id":               page.ID,
		"name":                  "Agent flow",
		"mermaid":               "graph TD; A-->B",
		"placement":             "end",
		"expected_content_hash": page.ContentHash,
	})
	created, ok := createdResult.(*services.PageDiagram)
	if !ok {
		t.Fatalf("create result type = %T", createdResult)
	}
	if created.AttachmentID == 0 || created.Kind != services.DiagramKindMermaid ||
		created.ContentHash == "" || created.RevisionNumber != 2 {
		t.Fatalf("created diagram = %+v", created)
	}

	listedResult := runPageDiagramTool(t, env, "list_page_diagrams", map[string]any{"page_id": page.ID})
	listed, ok := listedResult.(map[string]any)
	if !ok {
		t.Fatalf("list result type = %T", listedResult)
	}
	if diagrams, ok := listed["diagrams"].([]services.PageDiagram); !ok || len(diagrams) != 1 {
		t.Fatalf("list result = %#v", listedResult)
	}

	fetchedResult := runPageDiagramTool(t, env, "get_page_diagram", map[string]any{
		"page_id":       page.ID,
		"attachment_id": created.AttachmentID,
	})
	fetched, ok := fetchedResult.(*services.PageDiagram)
	if !ok || fetched.AttachmentID != created.AttachmentID || len(fetched.Payload) == 0 {
		t.Fatalf("get result = %#v", fetchedResult)
	}

	staleResult := runPageDiagramTool(t, env, "update_page_diagram", map[string]any{
		"page_id":               page.ID,
		"attachment_id":         created.AttachmentID,
		"mermaid":               "graph TD; B-->C",
		"expected_content_hash": page.ContentHash,
	})
	if stale, ok := staleResult.(map[string]string); !ok ||
		stale["error"] != "page content changed since it was read" {
		t.Fatalf("stale result = %#v", staleResult)
	}

	deniedEnv := *env
	deniedEnv.AccessibleWorkspaceIDs = nil
	deniedResult := runPageDiagramTool(t, &deniedEnv, "get_page_diagram", map[string]any{
		"page_id":       page.ID,
		"attachment_id": created.AttachmentID,
	})
	if denied, ok := deniedResult.(map[string]string); !ok || denied["error"] != "page not found" {
		t.Fatalf("denied result = %#v", deniedResult)
	}

	var action, details string
	if err := tdb.QueryRow(`
		SELECT action_type, details
		FROM audit_logs
		WHERE resource_type = ? AND resource_id = ?
		ORDER BY id DESC LIMIT 1
	`, logger.ResourcePage, page.ID).Scan(&action, &details); err != nil {
		t.Fatalf("load Page diagram audit: %v", err)
	}
	if action != logger.ActionPageUpdate || !strings.Contains(details, `"source":"mcp"`) {
		t.Fatalf("audit action/details = %q/%s", action, details)
	}

	humanUpdated, err := pageDiagrams.Update(services.AuditActor{
		UserID:   data.UserID,
		Username: "testuser",
		Source:   "cookie",
	}, services.UpdatePageDiagramInput{
		PageID:              page.ID,
		AttachmentID:        created.AttachmentID,
		Name:                "Human edited flow",
		Excalidraw:          json.RawMessage(`{"elements":[{"id":"human","type":"rectangle"}],"appState":{},"files":{}}`),
		ExpectedContentHash: &created.ContentHash,
	})
	if err != nil {
		t.Fatalf("human update through shared service: %v", err)
	}
	agentReadResult := runPageDiagramTool(t, env, "get_page_diagram", map[string]any{
		"page_id":       page.ID,
		"attachment_id": humanUpdated.AttachmentID,
	})
	agentRead, ok := agentReadResult.(*services.PageDiagram)
	if !ok || agentRead.Name != "Human edited flow" ||
		agentRead.Kind != services.DiagramKindExcalidraw ||
		len(agentRead.Payload) == 0 {
		t.Fatalf("agent read after human update = %#v", agentReadResult)
	}
}

func runPageDiagramTool(t *testing.T, env *Env, name string, input any) any {
	t.Helper()
	entry, ok := Default.Lookup(name)
	if !ok {
		t.Fatalf("tool %q is not registered", name)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal %s input: %v", name, err)
	}
	args := entry.NewArgs()
	if err := json.Unmarshal(raw, args); err != nil {
		t.Fatalf("decode %s input: %v", name, err)
	}
	result, err := entry.Run(context.Background(), env, args)
	if err != nil {
		t.Fatalf("run %s: %v", name, err)
	}
	return result
}
