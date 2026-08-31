//go:build test

package services

import (
	"database/sql"
	"strings"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestSCMActionRepositoryFiltersApplyToEveryTrigger(t *testing.T) {
	service := &ActionService{}
	for _, trigger := range []models.ActionTriggerType{
		models.ActionTriggerSCMTagCreated,
		models.ActionTriggerSCMReleaseBranchCreated,
		models.ActionTriggerSCMPRLinked,
		models.ActionTriggerSCMPRMerged,
	} {
		t.Run(string(trigger), func(t *testing.T) {
			action := &models.Action{
				TriggerType: trigger,
				TriggerConfig: `{
					"workspace_repository_id": 17,
					"repository_full_name": "Acme/API"
				}`,
			}
			event := &models.ActionEvent{
				EventType: trigger,
				NewValues: map[string]any{
					"repo.workspace_repository_id": 17,
					"repo.full_name":               "acme/api",
				},
			}
			if !service.matchesTrigger(action, event) {
				t.Fatal("matching repository was rejected")
			}
			event.NewValues["repo.workspace_repository_id"] = 18
			if service.matchesTrigger(action, event) {
				t.Fatal("different workspace repository matched")
			}
			event.NewValues["repo.workspace_repository_id"] = 17
			event.NewValues["repo.full_name"] = "acme/other"
			if service.matchesTrigger(action, event) {
				t.Fatal("different repository name matched")
			}
		})
	}
}

func TestSCMActionTriggersRequireExplicitActor(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	repo := repository.NewActionRepository(db)
	service := NewActionService(db, DefaultActionServiceConfig(), nil)
	t.Cleanup(service.Stop)

	for _, trigger := range []models.ActionTriggerType{
		models.ActionTriggerSCMTagCreated,
		models.ActionTriggerSCMReleaseBranchCreated,
		models.ActionTriggerSCMPRLinked,
		models.ActionTriggerSCMPRMerged,
	} {
		t.Run(string(trigger), func(t *testing.T) {
			actionID := createDurableTestAction(t, repo, data.WorkspaceID, string(trigger), trigger)
			action := &models.Action{
				ID: actionID, WorkspaceID: data.WorkspaceID, Name: string(trigger),
				IsEnabled: true, TriggerType: trigger,
			}
			err := service.executeActionForEvent(action, &models.ActionEvent{
				EventType: trigger, WorkspaceID: data.WorkspaceID,
				NewValues: map[string]any{"repo.workspace_repository_id": 17},
			}, nil, "")
			if err == nil || !strings.Contains(err.Error(), "requires an actor_user_id override") {
				t.Fatalf("executeActionForEvent() error = %v, want actor override rejection", err)
			}

			var status, message string
			var triggerUserID, effectiveActorUserID sql.NullInt64
			if err := db.QueryRow(`
				SELECT status, error_message, trigger_user_id, effective_actor_user_id
				FROM action_execution_logs
				WHERE action_id = ?
			`, actionID).Scan(&status, &message, &triggerUserID, &effectiveActorUserID); err != nil {
				t.Fatalf("load execution audit: %v", err)
			}
			if status != string(models.ActionStatusFailed) ||
				message != "SCM trigger requires an actor_user_id override because the sync loop has no authenticated user" {
				t.Fatalf("execution audit = %s/%q", status, message)
			}
			if triggerUserID.Valid || effectiveActorUserID.Valid {
				t.Fatalf("anonymous SCM audit users = trigger:%v effective:%v, want NULL/NULL", triggerUserID, effectiveActorUserID)
			}
		})
	}
}
