//go:build test

package services_test

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/database"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils/factory"
)

// failingPackProvisioner simulates a built-in pack whose schema stage imports a
// configuration set (and the workflow it creates fresh) and whose later stage
// fails. The real PackApplyService leaves those rows behind when the caller
// compensates by deleting the workspace; this fake reproduces that side effect
// through the production import service so the compensation can be asserted.
type failingPackProvisioner struct {
	db          database.Database
	configSetID int
	workflowID  *int
}

func (p *failingPackProvisioner) VerifyBuiltinPack(context.Context, string) (*services.PackApplyReport, error) {
	return &services.PackApplyReport{Status: services.PackVerifyStatusVerified}, nil
}

func (p *failingPackProvisioner) ApplyBuiltinPackToWorkspace(ctx context.Context, _ services.AuditActor, _ string, workspaceID int) (*services.PackApplyReport, error) {
	repo := repository.NewConfigurationSetRepository(p.db)
	template := &services.ConfigSetTemplate{
		SchemaVersion: services.ConfigSetTemplateSchemaVersion,
		Kind:          services.ConfigSetTemplateKind,
		Payload: services.ConfigSetTplPayload{
			StatusCategories: []services.ConfigSetTplStatusCat{{Name: "To Do"}},
			Statuses:         []services.ConfigSetTplStatus{{Name: "Rollback Open", CategoryName: "To Do"}},
			ItemTypes:        []services.ConfigSetTplItemType{{Name: "Rollback Task", HierarchyLevel: 1}},
			Workflows: []services.ConfigSetTplWorkflow{{
				Name:        "Rollback Workflow",
				Transitions: []services.ConfigSetTplWorkflowTransition{{ToStatusName: "Rollback Open"}},
			}},
			Links: services.ConfigSetTplLinks{
				WorkflowName:    "Rollback Workflow",
				ItemTypeConfigs: []services.ConfigSetTplItemTypeConfig{{ItemTypeName: "Rollback Task", WorkflowName: "Rollback Workflow"}},
			},
			ConfigurationSet: services.ConfigSetTplConfigSet{Name: "Rollback Set", DefaultItemTypeName: "Rollback Task"},
		},
	}
	configSetID, _, err := services.NewConfigSetImportService(p.db, repo).Import(ctx, template)
	if err != nil {
		return nil, err
	}
	if err := repo.AssignWorkspace(workspaceID, &configSetID); err != nil {
		return nil, err
	}
	var workflowID *int
	if err := p.db.QueryRowContext(ctx, `SELECT workflow_id FROM configuration_sets WHERE id = ?`, configSetID).Scan(&workflowID); err != nil {
		return nil, err
	}
	p.configSetID = configSetID
	p.workflowID = workflowID

	return &services.PackApplyReport{
		WorkspaceID: workspaceID,
		ConfigSetID: configSetID,
		Status:      services.PackApplyStatusFailed,
		Stages: []services.PackApplyStage{
			{Name: services.PackStageSchema, Status: services.PackStageStatusOK},
			{Name: services.PackStageContent, Status: services.PackStageStatusFailed, Detail: "seed content failed"},
		},
	}, nil
}

// TestCreateWorkspaceFromTemplatePackRollbackRemovesImportedConfiguration
// verifies WI-1533: a failed create-from-template-pack deletes the workspace
// and the configuration set its schema stage imported, including the workflow
// created fresh for that set, so the instance's global registries are not
// polluted by a provisioning that never completed.
func TestCreateWorkspaceFromTemplatePackRollbackRemovesImportedConfiguration(t *testing.T) {
	db := createWorkspaceTestDB(t)
	f := factory.NewTestFactory(db)
	userID, err := f.CreateUser(nil)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	provisioner := &failingPackProvisioner{db: db}
	svc := services.NewWorkspaceService(db)
	svc.SetPackProvisioner(provisioner)

	_, err = svc.Create(context.Background(), services.CreateWorkspaceParams{
		Name:         "Rolled Back Pack Workspace",
		Key:          "RBKPACK",
		CreatorID:    userID,
		TemplatePack: "fake-failing-pack",
	})
	if !errors.Is(err, services.ErrWorkspacePackProvisioning) {
		t.Fatalf("Create error = %v, want ErrWorkspacePackProvisioning", err)
	}
	if provisioner.configSetID == 0 {
		t.Fatal("fake provisioner did not import a configuration set")
	}

	var workspaces int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspaces WHERE name = ?`, "Rolled Back Pack Workspace").Scan(&workspaces); err != nil {
		t.Fatalf("count workspaces: %v", err)
	}
	if workspaces != 0 {
		t.Fatalf("workspaces named after the failed pack = %d, want 0", workspaces)
	}

	var configSets int
	if err := db.QueryRow(`SELECT COUNT(*) FROM configuration_sets WHERE id = ?`, provisioner.configSetID).Scan(&configSets); err != nil {
		t.Fatalf("count configuration sets: %v", err)
	}
	if configSets != 0 {
		t.Fatalf("configuration set %d survived the rollback, want removed", provisioner.configSetID)
	}

	if provisioner.workflowID == nil {
		t.Fatal("imported configuration set has no workflow to check")
	}
	var workflows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workflows WHERE id = ?`, *provisioner.workflowID).Scan(&workflows); err != nil {
		t.Fatalf("count workflows: %v", err)
	}
	if workflows != 0 {
		t.Fatalf("workflow %d created by the failed apply survived, want removed", *provisioner.workflowID)
	}

	var assignments int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspace_configuration_sets WHERE configuration_set_id = ?`, provisioner.configSetID).Scan(&assignments); err != nil {
		t.Fatalf("count assignments: %v", err)
	}
	if assignments != 0 {
		t.Fatalf("configuration-set assignments = %d, want 0", assignments)
	}
}
