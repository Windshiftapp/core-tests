package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/agentskills"
	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/restapi"
	"windshift/internal/services"
)

func grantSkillRequest(t *testing.T, db database.Database, req *http.Request, tokenID, workspaceID int, skills []models.SkillGrant) *http.Request {
	t.Helper()
	grants, err := json.Marshal(models.RunGrants{Skills: skills})
	if err != nil {
		t.Fatalf("marshal grants: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_runs(workspace_id, status, run_token_id, grants_json)
		VALUES (?, 'running', ?, ?)
	`, workspaceID, tokenID, string(grants)); err != nil {
		t.Fatalf("seed run grants: %v", err)
	}
	ctx := context.WithValue(req.Context(), restapi.ContextKeyAPIToken, &models.APIToken{ID: tokenID})
	return req.WithContext(ctx)
}

// Page content is snapshotted when the skill is saved and copied again into
// the run grant. Later page edits cannot alter an in-flight run's disclosure.

func TestV1AgentSkillHandler_Get_InlinesReferencedPages(t *testing.T) {
	db := newSearchTestDB(t)
	perm := newSearchPermService(t, db)
	h := NewAgentSkillHandler(db, perm)
	const userID = 1
	seedSearchUser(t, db, userID)
	seedSearchUser(t, db, 2)
	seedSearchWorkspaceRole(t, db, 1, userID, "Administrator")

	pageSvc := services.NewPageService(db)
	p1, err := pageSvc.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "Release Checklist", Content: "1. cut the tag\n2. write notes"})
	if err != nil {
		t.Fatalf("create page 1: %v", err)
	}
	p2, err := pageSvc.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "Tone Guide", Content: "Write plainly."})
	if err != nil {
		t.Fatalf("create page 2: %v", err)
	}

	skillRepo := repository.NewWorkspaceAgentSkillRepository(db)
	skillID, err := skillRepo.Insert(context.Background(), &models.WorkspaceAgentSkill{
		WorkspaceID: 1,
		Name:        "release-notes",
		Description: "How we write release notes",
		Body:        "# Release notes\nStructure every note as...",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("insert skill: %v", err)
	}
	if err := skillRepo.ReplaceSkillPages(context.Background(), skillID, 1, []int{p2.ID, p1.ID}); err != nil {
		t.Fatalf("reference pages: %v", err)
	}
	refs, err := skillRepo.PageRefsForSkill(context.Background(), skillID)
	if err != nil {
		t.Fatalf("load snapshots: %v", err)
	}
	if _, err := pageSvc.Update(userID, services.UpdatePageInput{ID: p1.ID, Title: "Changed title", Content: "new live content"}); err != nil {
		t.Fatalf("edit page after snapshot: %v", err)
	}
	refs, err = skillRepo.PageRefsForSkill(context.Background(), skillID)
	if err != nil {
		t.Fatalf("reload snapshots: %v", err)
	}
	if !refs[0].Stale || refs[0].SnapshotTitle != "Release Checklist" || refs[0].ContentSnapshot != "1. cut the tag\n2. write notes" {
		t.Fatalf("page edit did not preserve and mark the snapshot: %+v", refs[0])
	}
	rendered, _, err := agentskills.RenderActivation("# Release notes\nStructure every note as...", refs)
	if err != nil {
		t.Fatalf("render snapshot: %v", err)
	}

	// User 2 has no workspace or page role. The explicit run snapshot grant is
	// the reviewed ACL-widening action that permits this saved copy.
	req := searchRequest("/workspaces/1/agent-skills/1", 2, map[string]string{"id": "1", "skillId": "1"})
	req = grantSkillRequest(t, db, req, 91, 1, []models.SkillGrant{{
		ID: skillID, Name: "release-notes", Description: "How we write release notes", Body: rendered,
	}})
	rr := httptest.NewRecorder()
	h.Get(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var skill models.WorkspaceAgentSkill
	if err := json.Unmarshal(rr.Body.Bytes(), &skill); err != nil {
		t.Fatalf("decode: %v", err)
	}

	body := skill.Body
	if !strings.Contains(body, "# Release notes") {
		t.Errorf("body lost the original skill content:\n%s", body)
	}
	if !strings.Contains(body, "## Referenced pages") {
		t.Errorf("body missing the Referenced pages section:\n%s", body)
	}
	for _, want := range []string{"### Release Checklist", "cut the tag", "### Tone Guide", "Write plainly."} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{"Changed title", "new live content"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("live page edit leaked into saved snapshot %q:\n%s", unwanted, body)
		}
	}
	// Title order: Release Checklist must appear before Tone Guide.
	if strings.Index(body, "### Release Checklist") > strings.Index(body, "### Tone Guide") {
		t.Errorf("referenced pages out of title order:\n%s", body)
	}
}

func TestV1AgentSkillHandler_Get_NoReferencesLeavesBodyUntouched(t *testing.T) {
	db := newSearchTestDB(t)
	perm := newSearchPermService(t, db)
	h := NewAgentSkillHandler(db, perm)
	const userID = 1
	seedSearchUser(t, db, userID)
	seedSearchWorkspaceRole(t, db, 1, userID, "Administrator")

	skillRepo := repository.NewWorkspaceAgentSkillRepository(db)
	skillID, err := skillRepo.Insert(context.Background(), &models.WorkspaceAgentSkill{
		WorkspaceID: 1, Name: "plain", Description: "d", Body: "just a body", Enabled: true,
	})
	if err != nil {
		t.Fatalf("insert skill: %v", err)
	}
	if _, err := skillRepo.Update(context.Background(), &models.WorkspaceAgentSkill{
		ID: skillID, WorkspaceID: 1, Name: "plain", Description: "changed", Body: "changed body", Enabled: false,
	}); err != nil {
		t.Fatalf("disable skill after run snapshot: %v", err)
	}

	req := searchRequest("/workspaces/1/agent-skills/1", userID, map[string]string{"id": "1", "skillId": "1"})
	req = grantSkillRequest(t, db, req, 92, 1, []models.SkillGrant{{ID: skillID, Name: "plain", Description: "d", Body: "just a body"}})
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var skill models.WorkspaceAgentSkill
	if err := json.Unmarshal(rr.Body.Bytes(), &skill); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if skill.Body != "just a body" {
		t.Errorf("body changed with no references: got %q", skill.Body)
	}
	if strings.Contains(skill.Body, "Referenced pages") {
		t.Errorf("Referenced pages section emitted with no references")
	}
}

func TestV1AgentSkillHandler_DeniesWorkspaceTokenWithoutRunGrant(t *testing.T) {
	db := newSearchTestDB(t)
	h := NewAgentSkillHandler(db, newSearchPermService(t, db))
	seedSearchUser(t, db, 1)
	seedSearchWorkspaceRole(t, db, 1, 1, "Administrator")
	req := searchRequest("/workspaces/1/agent-skills/4", 1, map[string]string{"id": "1", "skillId": "4"})
	req = req.WithContext(context.WithValue(req.Context(), restapi.ContextKeyAPIToken, &models.APIToken{ID: 700}))
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("ordinary workspace token: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestV1AgentSkillHandler_DeniesSkillOutsideRunSnapshot(t *testing.T) {
	db := newSearchTestDB(t)
	h := NewAgentSkillHandler(db, newSearchPermService(t, db))
	seedSearchUser(t, db, 1)
	seedSearchWorkspaceRole(t, db, 1, 1, "Administrator")
	req := searchRequest("/workspaces/1/agent-skills/5", 1, map[string]string{"id": "1", "skillId": "5"})
	req = grantSkillRequest(t, db, req, 701, 1, []models.SkillGrant{{ID: 4, Name: "attached", Body: "saved"}})
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unattached skill: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestV1AgentSkillHandler_GetReturnsTypedOversizeError(t *testing.T) {
	db := newSearchTestDB(t)
	h := NewAgentSkillHandler(db, newSearchPermService(t, db))
	seedSearchUser(t, db, 1)
	seedSearchWorkspaceRole(t, db, 1, 1, "Administrator")
	req := searchRequest("/workspaces/1/agent-skills/4", 1, map[string]string{"id": "1", "skillId": "4"})
	req = grantSkillRequest(t, db, req, 702, 1, []models.SkillGrant{{
		ID: 4, Name: "legacy-large", Error: "agent skill activation exceeds the aggregate context budget",
	}})
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), "SKILL_ACTIVATION_TOO_LARGE") {
		t.Fatalf("oversize activation: want typed 422, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestReplaceSkillPages_RejectsForeignWorkspacePage pins that a page from
// another workspace cannot be referenced — the workspace-boundary invariant.
func TestReplaceSkillPages_RejectsForeignWorkspacePage(t *testing.T) {
	db := newSearchTestDB(t)
	const userID = 1
	seedSearchUser(t, db, userID)
	seedSearchWorkspaceRole(t, db, 1, userID, "Administrator")
	seedSearchWorkspaceRole(t, db, 2, userID, "Administrator")

	pageSvc := services.NewPageService(db)
	foreign, err := pageSvc.Create(userID, services.CreatePageInput{WorkspaceID: 2, Title: "Other WS Page", Content: "x"})
	if err != nil {
		t.Fatalf("create foreign page: %v", err)
	}

	skillRepo := repository.NewWorkspaceAgentSkillRepository(db)
	skillID, err := skillRepo.Insert(context.Background(), &models.WorkspaceAgentSkill{
		WorkspaceID: 1, Name: "s", Description: "d", Body: "b", Enabled: true,
	})
	if err != nil {
		t.Fatalf("insert skill: %v", err)
	}
	err = skillRepo.ReplaceSkillPages(context.Background(), skillID, 1, []int{foreign.ID})
	if err == nil {
		t.Fatal("expected ReplaceSkillPages to reject a foreign-workspace page")
	}
}
