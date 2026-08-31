package services

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/agentstudio"
	"windshift/internal/models"
	"windshift/internal/repository"
)

type privateTestDispatcher struct {
	binding *models.WorkspaceAgentBinding
	prompt  string
}

func (d *privateTestDispatcher) StartItemRun(context.Context, *models.WorkspaceAgentBinding, int, int, int, *models.RunTrigger) error {
	return nil
}

func (d *privateTestDispatcher) CancelForBinding(context.Context, int) error {
	return nil
}

func (d *privateTestDispatcher) RunPrivateTest(_ context.Context, binding *models.WorkspaceAgentBinding, _ int, _ int, prompt string) (*StandardPrivateTestResult, error) {
	d.binding = binding
	d.prompt = prompt
	return &StandardPrivateTestResult{Answer: "Read-only response", Iterations: 2, ToolCalls: 1}, nil
}

func TestBindingService_CreateStudioProfileKeepsOpenWorkspaceOpen(t *testing.T) {
	st := newBindingTestStack(t, false)
	llmConnectionID := validTestLLMConnectionID

	profile, err := st.BS.CreateStudioProfile(context.Background(), CreateStudioProfileRequest{
		WorkspaceID:     1,
		CreatedByUserID: st.AdminID,
		TemplateKey:     "workspace_guide",
		Name:            "Workspace Guide",
		Handle:          "workspace-guide",
		Purpose:         "Help members find the right workspace context.",
		LLMConnectionID: &llmConnectionID,
	})
	if err != nil {
		t.Fatalf("create Studio profile: %v", err)
	}
	if profile.ProfileType != models.AgentProfileStandard {
		t.Fatalf("profile type = %q, want standard", profile.ProfileType)
	}
	if profile.Lifecycle != models.AgentLifecycleDraft {
		t.Fatalf("lifecycle = %q, want draft", profile.Lifecycle)
	}
	if profile.IdentityClass != models.AgentIdentityWorkspaceManaged {
		t.Fatalf("identity class = %q, want workspace_managed", profile.IdentityClass)
	}
	if profile.ActingUserID == st.AgentID || profile.ActingUserID == st.SvcUserID {
		t.Fatalf("workspace-managed profile reused an existing identity: %d", profile.ActingUserID)
	}
	if profile.Instructions == "" {
		t.Fatal("template instructions were not copied into the Draft")
	}

	var username string
	var ownerID *int
	if err := st.DB.QueryRow(`
		SELECT username, agent_owner_user_id FROM users WHERE id = ?
	`, profile.ActingUserID).Scan(&username, &ownerID); err != nil {
		t.Fatalf("load acting identity: %v", err)
	}
	if username != "workspace-guide" {
		t.Fatalf("username = %q, want workspace-guide", username)
	}
	if ownerID != nil {
		t.Fatalf("workspace-managed identity owner = %v, want nil", ownerID)
	}
	var roleCount int
	if err := st.DB.QueryRow(`
		SELECT COUNT(*) FROM user_workspace_roles
		WHERE user_id = ? AND workspace_id = ?
	`, profile.ActingUserID, profile.WorkspaceID).Scan(&roleCount); err != nil {
		t.Fatalf("count explicit roles: %v", err)
	}
	if roleCount != 0 {
		t.Fatalf("explicit role count = %d, want 0 for an open workspace", roleCount)
	}
	canEdit, err := st.BS.permissions.HasWorkspacePermission(profile.ActingUserID, profile.WorkspaceID, models.PermissionItemEdit)
	if err != nil {
		t.Fatalf("check inherited Editor access: %v", err)
	}
	if !canEdit {
		t.Fatal("workspace-managed agent did not inherit Editor access from Everyone")
	}

	activated, validation, err := st.BS.ActivateStudioProfile(context.Background(), 1, profile.ID)
	if err != nil {
		t.Fatalf("activate profile: %v (validation=%+v)", err, validation)
	}
	if !validation.Ready || activated.Lifecycle != models.AgentLifecycleReady {
		t.Fatalf("activation = profile=%+v validation=%+v", activated, validation)
	}
}

func TestBindingService_CreateStudioProfileGrantsEditorInRestrictedWorkspace(t *testing.T) {
	tests := []struct {
		name        string
		restriction string
		role        string
	}{
		{name: "user Viewer assignment", restriction: "user", role: models.RoleViewer},
		{name: "group Editor assignment", restriction: "group", role: models.RoleEditor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newBindingTestStack(t, false)
			switch tt.restriction {
			case "user":
				if _, err := st.DB.Exec(`
					INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_by)
					SELECT ?, 1, id, ? FROM workspace_roles WHERE name = ?
				`, st.AgentID, st.AdminID, tt.role); err != nil {
					t.Fatalf("restrict workspace through user: %v", err)
				}
			case "group":
				var groupID int
				if err := st.DB.QueryRow(`INSERT INTO groups (name, is_active) VALUES ('agent-editors', true) RETURNING id`).Scan(&groupID); err != nil {
					t.Fatalf("create restriction group: %v", err)
				}
				if _, err := st.DB.Exec(`
					INSERT INTO group_workspace_roles (group_id, workspace_id, role_id, granted_by)
					SELECT ?, 1, id, ? FROM workspace_roles WHERE name = ?
				`, groupID, st.AdminID, tt.role); err != nil {
					t.Fatalf("restrict workspace through group: %v", err)
				}
			}
			llmConnectionID := validTestLLMConnectionID

			profile, err := st.BS.CreateStudioProfile(context.Background(), CreateStudioProfileRequest{
				WorkspaceID:     1,
				CreatedByUserID: st.AdminID,
				TemplateKey:     "software_engineer",
				Name:            "Software Engineer",
				Handle:          "software-engineer",
				LLMConnectionID: &llmConnectionID,
			})
			if err != nil {
				t.Fatalf("create Studio profile: %v", err)
			}

			var roleName string
			if err := st.DB.QueryRow(`
				SELECT wr.name
				FROM user_workspace_roles uwr
				JOIN workspace_roles wr ON wr.id = uwr.role_id
				WHERE uwr.user_id = ? AND uwr.workspace_id = ?
			`, profile.ActingUserID, profile.WorkspaceID).Scan(&roleName); err != nil {
				t.Fatalf("load explicit role: %v", err)
			}
			if roleName != models.RoleEditor {
				t.Fatalf("explicit role = %q, want %q", roleName, models.RoleEditor)
			}
		})
	}
}

func TestBindingService_CreateStudioProfileUsesEligibleCentralizedFallbackOnly(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	if _, err := st.DB.Exec(`
		UPDATE system_settings SET value = 'false' WHERE key = 'workspace_managed_agents'
	`); err != nil {
		t.Fatalf("disable workspace-managed identities: %v", err)
	}
	security := repository.NewAgentSecurityRepository(st.DB)
	if err := security.SetAllowCentralizedServiceUsers(ctx, true); err != nil {
		t.Fatalf("enable centralized identities: %v", err)
	}
	workspaceID := 1
	if err := security.AddAllowlistEntry(ctx, st.SvcUserID, &workspaceID, &st.AdminID, "Agent Studio fallback"); err != nil {
		t.Fatalf("allowlist centralized identity: %v", err)
	}
	llmConnectionID := validTestLLMConnectionID

	profile, err := st.BS.CreateStudioProfile(ctx, CreateStudioProfileRequest{
		WorkspaceID:     workspaceID,
		CreatedByUserID: st.AdminID,
		TemplateKey:     "blank",
		ActingUserID:    st.SvcUserID,
		LLMConnectionID: &llmConnectionID,
	})
	if err != nil {
		t.Fatalf("create centralized profile: %v", err)
	}
	if profile.ActingUserID != st.SvcUserID || profile.IdentityClass != models.AgentIdentityCentralized {
		t.Fatalf("centralized identity contract = %+v", profile)
	}

	_, err = st.BS.CreateStudioProfile(ctx, CreateStudioProfileRequest{
		WorkspaceID:     workspaceID,
		CreatedByUserID: st.AdminID,
		TemplateKey:     "blank",
		ActingUserID:    st.AgentID,
		LLMConnectionID: &llmConnectionID,
	})
	if !errors.Is(err, ErrAgentProfileCentralizedRequired) {
		t.Fatalf("user-owned fallback error = %v, want ErrAgentProfileCentralizedRequired", err)
	}
}

func TestBindingService_CreateStudioProfileRollsBackIdentityWhenDefaultRoleIsUnavailable(t *testing.T) {
	st := newBindingTestStack(t, false)
	if _, err := st.DB.Exec(`DELETE FROM workspace_roles WHERE name = ?`, models.RoleEditor); err != nil {
		t.Fatalf("remove Editor role: %v", err)
	}
	llmConnectionID := validTestLLMConnectionID

	_, err := st.BS.CreateStudioProfile(context.Background(), CreateStudioProfileRequest{
		WorkspaceID:     1,
		CreatedByUserID: st.AdminID,
		TemplateKey:     "blank",
		Name:            "Rollback Agent",
		Handle:          "rollback-agent",
		LLMConnectionID: &llmConnectionID,
	})
	if err == nil {
		t.Fatal("create succeeded without the default role")
	}
	var users int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE username = 'rollback-agent'`).Scan(&users); err != nil {
		t.Fatalf("count rolled-back users: %v", err)
	}
	var profiles int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM workspace_agent_bindings WHERE purpose = '' AND acting_user_id NOT IN (?, ?)`, st.AgentID, st.SvcUserID).Scan(&profiles); err != nil {
		t.Fatalf("count rolled-back profiles: %v", err)
	}
	if users != 0 || profiles != 0 {
		t.Fatalf("transaction leaked rows: users=%d profiles=%d", users, profiles)
	}
}

func TestBindingService_ActivationReportsMissingSelectedCapabilityPermission(t *testing.T) {
	st := newBindingTestStack(t, false)
	if _, err := st.DB.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_by)
		SELECT ?, 1, id, ? FROM workspace_roles WHERE name = ?
	`, st.AgentID, st.AdminID, models.RoleViewer); err != nil {
		t.Fatalf("restrict workspace: %v", err)
	}
	llmConnectionID := validTestLLMConnectionID
	profile, err := st.BS.CreateStudioProfile(context.Background(), CreateStudioProfileRequest{
		WorkspaceID:      1,
		CreatedByUserID:  st.AdminID,
		TemplateKey:      "work_item_triage",
		Name:             "Triage Agent",
		Handle:           "triage-agent",
		CapabilityGroups: []string{string(agentstudio.CapabilityIssueManagement)},
		LLMConnectionID:  &llmConnectionID,
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	if _, err := st.DB.Exec(`DELETE FROM user_workspace_roles WHERE user_id = ? AND workspace_id = 1`, profile.ActingUserID); err != nil {
		t.Fatalf("revoke agent Editor role: %v", err)
	}

	validation, err := st.BS.ValidateStudioProfile(context.Background(), 1, profile.ID)
	if err != nil {
		t.Fatalf("validate profile: %v", err)
	}
	if validation.Ready {
		t.Fatalf("identity without Editor access unexpectedly passed issue-management readiness: %+v", validation)
	}
	if !validationHasDependency(validation, models.PermissionItemCreate) ||
		!validationHasDependency(validation, models.PermissionItemEdit) {
		t.Fatalf("missing exact permission errors: %+v", validation.Errors)
	}
	if _, _, err := st.BS.ActivateStudioProfile(context.Background(), 1, profile.ID); !errors.Is(err, ErrAgentProfileValidationFailed) {
		t.Fatalf("activation error = %v, want ErrAgentProfileValidationFailed", err)
	}

	if _, err := st.DB.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_by)
		SELECT ?, 1, id, ? FROM workspace_roles WHERE name = ?
	`, profile.ActingUserID, st.AdminID, models.RoleEditor); err != nil {
		t.Fatalf("grant Editor role: %v", err)
	}
	if err := st.BS.permissions.InvalidateUserCache(profile.ActingUserID); err != nil {
		t.Fatalf("invalidate permission cache: %v", err)
	}
	activated, validation, err := st.BS.ActivateStudioProfile(context.Background(), 1, profile.ID)
	if err != nil {
		t.Fatalf("activate after permission recovery: %v (%+v)", err, validation)
	}
	if activated.Lifecycle != models.AgentLifecycleReady {
		t.Fatalf("lifecycle = %q, want ready", activated.Lifecycle)
	}
}

func TestBindingService_CodingDraftReportsRepositoryAndRunnerDependencies(t *testing.T) {
	st := newBindingTestStack(t, false)
	llmConnectionID := validTestLLMConnectionID
	profile, err := st.BS.CreateStudioProfile(context.Background(), CreateStudioProfileRequest{
		WorkspaceID:     1,
		CreatedByUserID: st.AdminID,
		TemplateKey:     "software_engineer",
		Name:            "Software Engineer",
		Handle:          "software-engineer",
		LLMConnectionID: &llmConnectionID,
	})
	if err != nil {
		t.Fatalf("create Coding Draft: %v", err)
	}
	validation, err := st.BS.ValidateStudioProfile(context.Background(), 1, profile.ID)
	if err != nil {
		t.Fatalf("validate Coding Draft: %v", err)
	}
	if validation.Ready ||
		!validationHasCode(validation, "repository_required") ||
		!validationHasCode(validation, "runner_authorization_required") {
		t.Fatalf("Coding dependency errors = %+v", validation.Errors)
	}

	// Restrict Viewer access after creation. The agent had inherited Editor
	// through Everyone, so it now has no edit access and health must report it.
	if _, err := st.DB.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_by)
		SELECT ?, 1, id, ? FROM workspace_roles WHERE name = ?
	`, st.AgentID, st.AdminID, models.RoleViewer); err != nil {
		t.Fatalf("restrict workspace after agent creation: %v", err)
	}
	if err := st.BS.permissions.InvalidateUserCache(profile.ActingUserID); err != nil {
		t.Fatalf("invalidate agent permissions: %v", err)
	}
	validation, err = st.BS.ValidateStudioProfile(context.Background(), 1, profile.ID)
	if err != nil {
		t.Fatalf("validate Coding health after Editor removal: %v", err)
	}
	if !validationHasDependency(validation, models.PermissionItemEdit) {
		t.Fatalf("Coding health omitted missing Editor access: %+v", validation.Errors)
	}
}

func TestBindingService_CreateStudioProfileRejectsDisabledTestsCapabilityAndDuplicateHandle(t *testing.T) {
	st := newBindingTestStack(t, false)
	llmConnectionID := validTestLLMConnectionID
	if _, err := st.DB.Exec(`
		UPDATE system_settings SET value = 'false' WHERE key = 'test_management_enabled'
	`); err != nil {
		t.Fatalf("disable Test Management: %v", err)
	}
	_, err := st.BS.CreateStudioProfile(context.Background(), CreateStudioProfileRequest{
		WorkspaceID:      1,
		CreatedByUserID:  st.AdminID,
		TemplateKey:      "qa_test_engineer",
		Name:             "QA Agent",
		Handle:           "qa-agent",
		CapabilityGroups: []string{string(agentstudio.CapabilityTests)},
		LLMConnectionID:  &llmConnectionID,
	})
	if !errors.Is(err, ErrAgentProfileTestManagement) {
		t.Fatalf("Tests capability error = %v, want ErrAgentProfileTestManagement", err)
	}
	if _, err := st.DB.Exec(`
		UPDATE system_settings SET value = 'true' WHERE key = 'test_management_enabled'
	`); err != nil {
		t.Fatalf("enable Test Management: %v", err)
	}
	first, err := st.BS.CreateStudioProfile(context.Background(), CreateStudioProfileRequest{
		WorkspaceID:     1,
		CreatedByUserID: st.AdminID,
		TemplateKey:     "blank",
		Name:            "Stable Identity",
		Handle:          "stable-identity",
		LLMConnectionID: &llmConnectionID,
	})
	if err != nil {
		t.Fatalf("create first identity: %v", err)
	}
	_, err = st.BS.CreateStudioProfile(context.Background(), CreateStudioProfileRequest{
		WorkspaceID:     1,
		CreatedByUserID: st.AdminID,
		TemplateKey:     "blank",
		Name:            "Duplicate Identity",
		Handle:          "stable-identity",
		LLMConnectionID: &llmConnectionID,
	})
	if !errors.Is(err, ErrAgentProfileHandleTaken) {
		t.Fatalf("duplicate handle error = %v, want ErrAgentProfileHandleTaken", err)
	}
	var bindings int
	if err := st.DB.QueryRow(`
		SELECT COUNT(*) FROM workspace_agent_bindings WHERE acting_user_id = ?
	`, first.ActingUserID).Scan(&bindings); err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	if bindings != 1 {
		t.Fatalf("binding count for stable identity = %d, want 1", bindings)
	}
}

func TestBindingService_StandardProfileNeverFallsThroughToCodingRunner(t *testing.T) {
	st := newBindingTestStack(t, true)
	st.BS.standardRuns = nil
	binding := &models.WorkspaceAgentBinding{
		ID:          99,
		WorkspaceID: 1,
		ProfileType: models.AgentProfileStandard,
		Lifecycle:   models.AgentLifecycleReady,
	}
	err := st.BS.startRunForBinding(
		context.Background(),
		binding,
		1,
		1,
		st.AdminID,
		&models.RunTrigger{Kind: "assignee"},
	)
	if !errors.Is(err, ErrStandardAgentRuntimeUnavailable) {
		t.Fatalf("dispatch error = %v, want ErrStandardAgentRuntimeUnavailable", err)
	}
}

func TestBindingService_RunPrivateProfileTestUsesStandardRuntimeWithoutReadinessMutation(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	dispatcher := &privateTestDispatcher{}
	st.BS.standardRuns = dispatcher
	llmConnectionID := validTestLLMConnectionID
	bindingID, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		ActingUserKind:  ActingIdentityKindAgent,
		ProfileType:     models.AgentProfileStandard,
		Lifecycle:       models.AgentLifecycleDraft,
		ProfileVersion:  6,
		LLMConnectionID: &llmConnectionID,
		CreatedByUserID: st.AdminID,
	})
	if err != nil {
		t.Fatalf("seed Standard Draft: %v", err)
	}

	result, err := st.BS.RunPrivateProfileTest(ctx, 1, bindingID, st.AdminID, "Inspect only.")
	if err != nil {
		t.Fatalf("run private Standard test: %v", err)
	}
	if result.Mode != "standard" || result.Status != models.AgentRunStatusSucceeded ||
		result.Answer != "Read-only response" || result.Iterations != 2 || result.ToolCalls != 1 {
		t.Fatalf("private result = %+v", result)
	}
	if dispatcher.binding == nil || dispatcher.binding.ID != bindingID || dispatcher.prompt != "Inspect only." {
		t.Fatalf("dispatcher call = binding %+v prompt %q", dispatcher.binding, dispatcher.prompt)
	}
	reloaded, err := st.Bindings.Get(ctx, bindingID)
	if err != nil {
		t.Fatalf("reload profile: %v", err)
	}
	if reloaded.Lifecycle != models.AgentLifecycleDraft || reloaded.ProfileVersion != 6 {
		t.Fatalf("private test mutated definition: lifecycle=%q version=%d", reloaded.Lifecycle, reloaded.ProfileVersion)
	}
}

func validationHasDependency(result *ProfileValidationResult, dependency string) bool {
	for _, validationError := range result.Errors {
		if validationError.Dependency == dependency {
			return true
		}
	}
	return false
}

func validationHasCode(result *ProfileValidationResult, code string) bool {
	for _, validationError := range result.Errors {
		if validationError.Code == code {
			return true
		}
	}
	return false
}
