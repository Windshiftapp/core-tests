//go:build test

package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"windshift/internal/llm"
	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

type profileHandlerLLMRuntime struct{}

func (profileHandlerLLMRuntime) ConnectionRuntime(_ context.Context, id int) (*llm.ConnectionRuntimeConfig, error) {
	if id == 1 {
		return &llm.ConnectionRuntimeConfig{Model: "test-model"}, nil
	}
	return nil, fmt.Errorf("connection unavailable")
}

func (profileHandlerLLMRuntime) PromptConnection(_ context.Context, _ int, _ string) (string, error) {
	return "ok", nil
}

type profileHandlerStandardRuns struct{}

func (profileHandlerStandardRuns) StartItemRun(context.Context, *models.WorkspaceAgentBinding, int, int, int, *models.RunTrigger) error {
	return nil
}

func (profileHandlerStandardRuns) CancelForBinding(context.Context, int) error {
	return nil
}

func (profileHandlerStandardRuns) RunPrivateTest(context.Context, *models.WorkspaceAgentBinding, int, int, string) (*services.StandardPrivateTestResult, error) {
	return &services.StandardPrivateTestResult{
		Answer:     "Private profile response",
		Iterations: 1,
		ToolCalls:  0,
	}, nil
}

func TestCatalogAvailabilityOfflineCodingRemainsAssignable(t *testing.T) {
	validation := &services.ProfileValidationResult{Ready: true}
	coding := &models.WorkspaceAgentBinding{
		ProfileType: models.AgentProfileCoding,
		Lifecycle:   models.AgentLifecycleReady,
	}
	availability, available := catalogAvailability(coding, validation, services.AgentPresenceOffline)
	if availability != "offline" || !available {
		t.Fatalf("offline coding availability = %q, %v; want offline and assignable", availability, available)
	}

	standard := &models.WorkspaceAgentBinding{
		ProfileType: models.AgentProfileStandard,
		Lifecycle:   models.AgentLifecycleReady,
	}
	availability, available = catalogAvailability(standard, validation, services.AgentPresenceOffline)
	if availability != "ready" || !available {
		t.Fatalf("standard availability = %q, %v; want ready", availability, available)
	}
}

func TestWorkspaceRunnerOnboardingIsAdminAndPoolScoped(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	permissionService, err := services.NewPermissionService(db, services.PermissionCacheConfig{
		TTL:             time.Minute,
		MaxCacheSize:    32,
		WarmupOnStartup: false,
		PreWarmActive:   false,
		BatchSize:       10,
	})
	if err != nil {
		t.Fatalf("permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })
	identity, err := services.NewAgentActingIdentityService(
		services.NewUserReadService(db),
		repository.NewAgentSecurityRepository(db),
	)
	if err != nil {
		t.Fatalf("identity service: %v", err)
	}
	actionRepo := repository.NewActionRepository(db)
	poolID, err := actionRepo.CreateCapabilityWithWorkspaces(&models.ActionCapability{
		Name:                   "Workspace runner",
		CapabilityType:         models.CapabilityRunnerPool,
		Config:                 `{}`,
		IsEnabled:              true,
		AppliesToAllWorkspaces: false,
	}, []int{data.WorkspaceID})
	if err != nil {
		t.Fatalf("create authorized runner pool: %v", err)
	}
	foreignPoolID, err := actionRepo.CreateCapability(&models.ActionCapability{
		Name:                   "Foreign runner",
		CapabilityType:         models.CapabilityRunnerPool,
		Config:                 `{}`,
		IsEnabled:              true,
		AppliesToAllWorkspaces: false,
	})
	if err != nil {
		t.Fatalf("create foreign runner pool: %v", err)
	}
	bindingService, err := services.NewBindingService(services.BindingServiceOptions{
		DB:          db,
		Repo:        repository.NewWorkspaceAgentBindingRepository(db),
		Identity:    identity,
		Permissions: permissionService,
		Pools:       actionRepo,
	})
	if err != nil {
		t.Fatalf("binding service: %v", err)
	}
	handler := NewWorkspaceAgentBindingHandler(
		bindingService,
		identity,
		permissionService,
		logger.NewAuditor(db),
	)
	handler.SetRunnerOnboarding(
		services.NewRunnerRegistryService(repository.NewRunnerRepository(db), nil),
		"https://windshift.example",
	)

	var viewerID int
	if err := tdb.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, password_hash, is_active)
		VALUES ('viewer-runner@example.test', 'viewer-runner', 'Read', 'Only', '', true) RETURNING id
	`).Scan(&viewerID); err != nil {
		t.Fatalf("insert viewer: %v", err)
	}
	admin := testutils.DefaultTestUser()
	viewer := testutils.TestUserWithID(viewerID)
	workspacePath := strconv.Itoa(data.WorkspaceID)
	poolPath := strconv.Itoa(poolID)

	mint := func(user *models.User, selectedPool int, ttl int) *testutils.ResponseRecorder {
		t.Helper()
		req := testutils.CreateJSONRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/workspaces/%d/agent-runner-pools/%d/tokens", data.WorkspaceID, selectedPool),
			map[string]any{"description": "Agent Studio test", "ttl_hours": ttl},
		)
		req.SetPathValue("workspaceId", workspacePath)
		req.SetPathValue("poolId", strconv.Itoa(selectedPool))
		return testutils.ExecuteAuthenticatedRequest(t, handler.MintRunnerSetupToken, req, user)
	}

	mint(viewer, poolID, 1).AssertStatusCode(http.StatusForbidden)
	mint(admin, foreignPoolID, 1).AssertStatusCode(http.StatusNotFound)
	mint(admin, poolID, 721).AssertStatusCode(http.StatusBadRequest)

	mintedResponse := mint(admin, poolID, 720)
	mintedResponse.AssertStatusCode(http.StatusCreated)
	var minted mintRunnerTokenResponse
	mintedResponse.AssertJSONResponse(&minted)
	if minted.ID <= 0 || minted.Token == "" || !strings.Contains(minted.InstallCommand, minted.Token) {
		t.Fatalf("one-time runner response = %+v", minted)
	}
	if minted.ExpiresAt == nil || time.Until(*minted.ExpiresAt) > 720*time.Hour {
		t.Fatalf("token expiry = %v, want at most 720h", minted.ExpiresAt)
	}

	listReq := testutils.CreateJSONRequest(
		t,
		http.MethodGet,
		fmt.Sprintf("/api/workspaces/%d/agent-runner-pools/%d/tokens", data.WorkspaceID, poolID),
		nil,
	)
	listReq.SetPathValue("workspaceId", workspacePath)
	listReq.SetPathValue("poolId", poolPath)
	listResponse := testutils.ExecuteAuthenticatedRequest(t, handler.ListRunnerSetupTokens, listReq, admin)
	listResponse.AssertStatusCode(http.StatusOK)
	if strings.Contains(listResponse.Body.String(), minted.Token) {
		t.Fatal("token list leaked the one-time plaintext")
	}
	var listed []*models.RunnerRegistrationToken
	listResponse.AssertJSONResponse(&listed)
	if len(listed) != 1 || listed[0].ID != minted.ID || listed[0].RevokedAt != nil {
		t.Fatalf("listed token metadata = %+v", listed)
	}

	revokeReq := testutils.CreateJSONRequest(
		t,
		http.MethodDelete,
		fmt.Sprintf("/api/workspaces/%d/agent-runner-pools/%d/tokens/%d", data.WorkspaceID, poolID, minted.ID),
		nil,
	)
	revokeReq.SetPathValue("workspaceId", workspacePath)
	revokeReq.SetPathValue("poolId", poolPath)
	revokeReq.SetPathValue("tokenId", strconv.Itoa(minted.ID))
	testutils.ExecuteAuthenticatedRequest(t, handler.RevokeRunnerSetupToken, revokeReq, admin).
		AssertStatusCode(http.StatusOK)
}

func TestWorkspaceAgentProfileHandlerTemplatesCreateAndActivateRequireAdmin(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	permissionService, err := services.NewPermissionService(db, services.PermissionCacheConfig{
		TTL:             time.Minute,
		MaxCacheSize:    32,
		WarmupOnStartup: false,
		PreWarmActive:   false,
		BatchSize:       10,
	})
	if err != nil {
		t.Fatalf("permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })
	security := repository.NewAgentSecurityRepository(db)
	identity, err := services.NewAgentActingIdentityService(services.NewUserReadService(db), security)
	if err != nil {
		t.Fatalf("identity service: %v", err)
	}
	bindingRepo := repository.NewWorkspaceAgentBindingRepository(db)
	prompts := llm.NewPromptStore("")
	bindingService, err := services.NewBindingService(services.BindingServiceOptions{
		DB:           db,
		Repo:         bindingRepo,
		Identity:     identity,
		Permissions:  permissionService,
		Prompts:      prompts,
		LLMRuntime:   profileHandlerLLMRuntime{},
		StandardRuns: profileHandlerStandardRuns{},
	})
	if err != nil {
		t.Fatalf("binding service: %v", err)
	}
	handler := NewWorkspaceAgentBindingHandler(bindingService, identity, permissionService, logger.NewAuditor(db))
	handler.SetPromptStore(prompts)

	var viewerID int
	if err := tdb.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, password_hash, is_active)
		VALUES ('viewer.test', 'viewer', 'Read', 'Only', '', true) RETURNING id
	`).Scan(&viewerID); err != nil {
		t.Fatalf("insert viewer: %v", err)
	}
	viewer := testutils.TestUserWithID(viewerID)
	admin := testutils.DefaultTestUser()
	workspacePath := strconv.Itoa(data.WorkspaceID)

	templateRequest := func(user *models.User) *testutils.ResponseRecorder {
		t.Helper()
		req := testutils.CreateJSONRequest(t, http.MethodGet, "/api/workspaces/1/agent-templates", nil)
		req.SetPathValue("workspaceId", workspacePath)
		return testutils.ExecuteAuthenticatedRequest(t, handler.Templates, req, user)
	}
	templateRequest(viewer).AssertStatusCode(http.StatusForbidden)
	adminTemplates := templateRequest(admin)
	adminTemplates.AssertStatusCode(http.StatusOK)
	var templates []llm.AgentTemplate
	adminTemplates.AssertJSONResponse(&templates)
	if len(templates) != 8 {
		t.Fatalf("template count = %d, want 8", len(templates))
	}

	createBody := map[string]any{
		"template_key":      "workspace_guide",
		"name":              "Workspace Guide",
		"handle":            "workspace-guide-handler",
		"purpose":           "Help members navigate the workspace.",
		"llm_connection_id": 1,
	}
	createRequest := func(user *models.User) *testutils.ResponseRecorder {
		t.Helper()
		req := testutils.CreateJSONRequest(t, http.MethodPost, "/api/workspaces/1/agent-profiles", createBody)
		req.SetPathValue("workspaceId", workspacePath)
		return testutils.ExecuteAuthenticatedRequest(t, handler.CreateProfile, req, user)
	}
	createRequest(viewer).AssertStatusCode(http.StatusForbidden)
	createdResponse := createRequest(admin)
	createdResponse.AssertStatusCode(http.StatusCreated)
	var created bindingResponse
	createdResponse.AssertJSONResponse(&created)
	if created.Lifecycle != models.AgentLifecycleDraft ||
		created.IdentityClass != models.AgentIdentityWorkspaceManaged {
		t.Fatalf("created profile = %+v", created)
	}
	if created.Name != "Workspace Guide" || created.Handle != "workspace-guide-handler" {
		t.Fatalf("created identity = name %q handle %q", created.Name, created.Handle)
	}

	testRequest := func(user *models.User) *testutils.ResponseRecorder {
		t.Helper()
		req := testutils.CreateJSONRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/workspaces/1/agent-profiles/%d/test", created.ID),
			privateProfileTestBody{Prompt: "Do not persist this prompt."},
		)
		req.SetPathValue("workspaceId", workspacePath)
		req.SetPathValue("id", strconv.Itoa(created.ID))
		return testutils.ExecuteAuthenticatedRequest(t, handler.TestProfile, req, user)
	}
	testRequest(viewer).AssertStatusCode(http.StatusForbidden)
	privateResponse := testRequest(admin)
	privateResponse.AssertStatusCode(http.StatusOK)
	var privateResult services.PrivateProfileTestResult
	privateResponse.AssertJSONResponse(&privateResult)
	if privateResult.Mode != "standard" || privateResult.Answer != "Private profile response" {
		t.Fatalf("private profile test = %+v", privateResult)
	}
	oversizedRequest := testutils.CreateJSONRequest(
		t,
		http.MethodPost,
		fmt.Sprintf("/api/workspaces/1/agent-profiles/%d/test", created.ID),
		privateProfileTestBody{Prompt: strings.Repeat("x", maxPrivateProfileTestPromptRunes+1)},
	)
	oversizedRequest.SetPathValue("workspaceId", workspacePath)
	oversizedRequest.SetPathValue("id", strconv.Itoa(created.ID))
	testutils.ExecuteAuthenticatedRequest(t, handler.TestProfile, oversizedRequest, admin).
		AssertStatusCode(http.StatusBadRequest)

	activateRequest := testutils.CreateJSONRequest(
		t,
		http.MethodPost,
		fmt.Sprintf("/api/workspaces/1/agent-profiles/%d/ready", created.ID),
		nil,
	)
	activateRequest.SetPathValue("workspaceId", workspacePath)
	activateRequest.SetPathValue("id", strconv.Itoa(created.ID))
	activatedResponse := testutils.ExecuteAuthenticatedRequest(t, handler.ActivateProfile, activateRequest, admin)
	activatedResponse.AssertStatusCode(http.StatusOK)
	var activated bindingResponse
	activatedResponse.AssertJSONResponse(&activated)
	if activated.Lifecycle != models.AgentLifecycleReady {
		t.Fatalf("activated lifecycle = %q, want ready", activated.Lifecycle)
	}

	updateRequest := testutils.CreateJSONRequest(
		t,
		http.MethodPatch,
		fmt.Sprintf("/api/workspaces/1/agent-profiles/%d", created.ID),
		updateStudioProfileBody{
			ExpectedVersion: 1,
			Name:            "Workspace Navigator",
			Handle:          "workspace-navigator",
			Purpose:         "Guide teammates through delivery.",
		},
	)
	updateRequest.SetPathValue("workspaceId", workspacePath)
	updateRequest.SetPathValue("id", strconv.Itoa(created.ID))
	updateResponse := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateProfile, updateRequest, admin)
	updateResponse.AssertStatusCode(http.StatusOK)
	var updated bindingResponse
	updateResponse.AssertJSONResponse(&updated)
	if updated.Name != "Workspace Navigator" ||
		updated.Handle != "workspace-navigator" ||
		updated.Purpose != "Guide teammates through delivery." ||
		updated.ProfileVersion != 2 ||
		updated.Lifecycle != models.AgentLifecycleDraft {
		t.Fatalf("updated profile = %+v", updated)
	}

	staleRequest := testutils.CreateJSONRequest(
		t,
		http.MethodPatch,
		fmt.Sprintf("/api/workspaces/1/agent-profiles/%d", created.ID),
		updateStudioProfileBody{ExpectedVersion: 1, Purpose: "Stale overwrite"},
	)
	staleRequest.SetPathValue("workspaceId", workspacePath)
	staleRequest.SetPathValue("id", strconv.Itoa(created.ID))
	testutils.ExecuteAuthenticatedRequest(t, handler.UpdateProfile, staleRequest, admin).
		AssertStatusCode(http.StatusConflict)
}

func TestWorkspaceAgentProfileCatalogIsMemberVisibleAndConfigurationSafe(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	permissionService, err := services.NewPermissionService(db, services.PermissionCacheConfig{
		TTL:             time.Minute,
		MaxCacheSize:    32,
		WarmupOnStartup: false,
		PreWarmActive:   false,
		BatchSize:       10,
	})
	if err != nil {
		t.Fatalf("permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })
	security := repository.NewAgentSecurityRepository(db)
	identity, err := services.NewAgentActingIdentityService(services.NewUserReadService(db), security)
	if err != nil {
		t.Fatalf("identity service: %v", err)
	}
	prompts := llm.NewPromptStore("")
	bindingService, err := services.NewBindingService(services.BindingServiceOptions{
		DB:           db,
		Repo:         repository.NewWorkspaceAgentBindingRepository(db),
		Identity:     identity,
		Permissions:  permissionService,
		Prompts:      prompts,
		LLMRuntime:   profileHandlerLLMRuntime{},
		StandardRuns: profileHandlerStandardRuns{},
	})
	if err != nil {
		t.Fatalf("binding service: %v", err)
	}
	handler := NewWorkspaceAgentBindingHandler(bindingService, identity, permissionService, logger.NewAuditor(db))

	var viewerID int
	if err := tdb.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, password_hash, is_active)
		VALUES ('viewer@example.test', 'viewer', 'Read', 'Only', '', true) RETURNING id
	`).Scan(&viewerID); err != nil {
		t.Fatalf("insert viewer: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO user_workspace_roles(user_id, workspace_id, role_id, granted_by)
		SELECT ?, ?, id, 1 FROM workspace_roles WHERE name = 'Viewer'
	`, viewerID, data.WorkspaceID); err != nil {
		t.Fatalf("grant viewer role: %v", err)
	}
	viewer := testutils.TestUserWithID(viewerID)
	admin := testutils.DefaultTestUser()
	instructions := "private administrator-only instructions"
	llmConnectionID := 1
	profile, err := bindingService.CreateStudioProfile(context.Background(), services.CreateStudioProfileRequest{
		WorkspaceID:     data.WorkspaceID,
		CreatedByUserID: admin.ID,
		TemplateKey:     "workspace_guide",
		Name:            "Workspace Guide",
		Handle:          "workspace-guide-catalog",
		Purpose:         "Help members navigate this workspace.",
		Instructions:    &instructions,
		LLMConnectionID: &llmConnectionID,
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	if _, _, err := bindingService.ActivateStudioProfile(context.Background(), data.WorkspaceID, profile.ID); err != nil {
		t.Fatalf("activate profile: %v", err)
	}

	request := func(user *models.User, workspaceID int) *testutils.ResponseRecorder {
		t.Helper()
		req := testutils.CreateJSONRequest(t, http.MethodGet, "/api/workspaces/1/agent-profiles", nil)
		req.SetPathValue("workspaceId", strconv.Itoa(workspaceID))
		return testutils.ExecuteAuthenticatedRequest(t, handler.Catalog, req, user)
	}
	response := request(viewer, data.WorkspaceID)
	response.AssertStatusCode(http.StatusOK)
	var entries []agentCatalogEntry
	response.AssertJSONResponse(&entries)
	if len(entries) != 1 {
		t.Fatalf("catalog entries = %d, want 1", len(entries))
	}
	if entries[0].Name != "Workspace Guide" || entries[0].Availability != "ready" || !entries[0].Available {
		t.Fatalf("catalog entry = %+v", entries[0])
	}
	if entries[0].ModelSummary != "test-model" {
		t.Fatalf("catalog model summary = %q, want test-model", entries[0].ModelSummary)
	}
	body := response.Body.String()
	for _, forbidden := range []string{
		"instructions", "capability_groups", "llm_connection_id", "repos",
		"token_scopes", "owner_name", "private administrator-only instructions",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("member-safe catalog leaked %q: %s", forbidden, body)
		}
	}

	if _, err := tdb.Exec(`
		INSERT INTO users
			(id, email, username, first_name, last_name, password_hash, is_active)
		VALUES (9001, 'owner@example.test', 'owner', 'Ada', 'Owner', '', true)
	`); err != nil {
		t.Fatalf("insert agent owner: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO users
			(id, email, username, first_name, last_name, password_hash, is_active, is_agent, agent_owner_user_id)
		VALUES (9002, 'agent@example.test', 'owned-agent', 'Owned', 'Agent', '', true, true, 9001)
	`); err != nil {
		t.Fatalf("insert owned agent: %v", err)
	}
	if _, err := tdb.Exec(`
		UPDATE workspace_agent_bindings
		SET acting_user_id = 9002,
		    acting_user_kind = 'agent',
		    identity_class = 'user_owned',
		    lifecycle = 'draft'
		WHERE id = ?
	`, profile.ID); err != nil {
		t.Fatalf("convert profile to grandfathered owned identity: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO user_global_permissions (user_id, permission_id)
		SELECT ?, id FROM permissions WHERE permission_key = 'system.admin'
	`, admin.ID); err != nil {
		t.Fatalf("grant owner-attribution visibility: %v", err)
	}
	_ = permissionService.InvalidateUserCache(admin.ID)

	adminResponse := request(admin, data.WorkspaceID)
	adminResponse.AssertStatusCode(http.StatusOK)
	var adminEntries []agentCatalogEntry
	adminResponse.AssertJSONResponse(&adminEntries)
	if len(adminEntries) != 1 || adminEntries[0].OwnerName != "Ada Owner" {
		t.Fatalf("authorized owner attribution = %+v", adminEntries)
	}

	viewerResponse := request(viewer, data.WorkspaceID)
	viewerResponse.AssertStatusCode(http.StatusOK)
	if strings.Contains(viewerResponse.Body.String(), "owner_name") ||
		strings.Contains(viewerResponse.Body.String(), "Ada Owner") {
		t.Fatalf("viewer catalog leaked owner attribution: %s", viewerResponse.Body.String())
	}

	request(viewer, data.WorkspaceID+999).AssertStatusCode(http.StatusForbidden)
}
