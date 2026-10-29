//go:build test

package services

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"windshift/internal/models"
)

func boardViewSettingsOverride(views ...string) *models.ViewSettings {
	return &models.ViewSettings{EnabledViews: &views}
}

func boardViewSettingsReset() *models.ViewSettings {
	var nilViews []string
	return &models.ViewSettings{EnabledViews: &nilViews}
}

func assertEnabledViews(t *testing.T, settings *models.ViewSettings, want []string) {
	t.Helper()
	if settings == nil || settings.EnabledViews == nil {
		t.Fatalf("view settings = %+v, want enabled views %v", settings, want)
	}
	if !reflect.DeepEqual(*settings.EnabledViews, want) {
		t.Fatalf("enabled views = %v, want %v", *settings.EnabledViews, want)
	}
}

func TestPutBoardConfigurationValidatesEnabledViews(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, true)
	service := NewCollectionApplicationService(tdb.GetDatabase(), permissions)
	actor := AuditActor{UserID: seed.UserID}
	scope := BoardConfigurationScope{WorkspaceID: &seed.WorkspaceID}

	tests := []struct {
		name    string
		views   []string
		wantErr string
	}{
		{name: "unknown view id rejected", views: []string{"kanban"}, wantErr: `unknown view "kanban" in enabled_views`},
		{name: "duplicate view ids rejected", views: []string{"board", "board"}, wantErr: `duplicate view "board" in enabled_views`},
		{name: "empty set rejected", views: []string{}, wantErr: "enabled_views must contain at least one view"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{
				ViewSettings: boardViewSettingsOverride(tt.views...),
			})
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("PutBoardConfiguration error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestBoardViewSettingsRoundTripAndMerge(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, true)
	service := NewCollectionApplicationService(tdb.GetDatabase(), permissions)
	actor := AuditActor{UserID: seed.UserID}
	scope := BoardConfigurationScope{WorkspaceID: &seed.WorkspaceID}

	created, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{
		ViewSettings: boardViewSettingsOverride("board", "list"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	config, err := service.GetBoardConfiguration(seed.UserID, scope)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if config.ID != created.ID {
		t.Fatalf("config id = %d, want %d", config.ID, created.ID)
	}
	if config.ViewSettingsInherited {
		t.Fatal("workspace scope must never report inherited settings")
	}
	assertEnabledViews(t, config.ViewSettings, []string{"board", "list"})

	// An update without view settings keeps the stored override.
	if _, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{}); err != nil {
		t.Fatalf("update without view settings: %v", err)
	}
	config, err = service.GetBoardConfiguration(seed.UserID, scope)
	if err != nil {
		t.Fatalf("get after merge: %v", err)
	}
	assertEnabledViews(t, config.ViewSettings, []string{"board", "list"})

	// An explicit null resets the override to the default.
	reset := boardViewSettingsReset()
	if _, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{ViewSettings: reset}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	config, err = service.GetBoardConfiguration(seed.UserID, scope)
	if err != nil {
		t.Fatalf("get after reset: %v", err)
	}
	if config.ViewSettings == nil || config.ViewSettings.EnabledViews == nil || len(*config.ViewSettings.EnabledViews) != 0 {
		t.Fatalf("view settings after reset = %+v, want explicit null", config.ViewSettings)
	}

	// Deleting the configuration falls back to the default with no settings.
	if err := service.DeleteBoardConfiguration(actor, scope); err != nil {
		t.Fatalf("delete: %v", err)
	}
	config, err = service.GetBoardConfiguration(seed.UserID, scope)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if config.ID != 0 || config.ViewSettings != nil {
		t.Fatalf("default board after delete = (%+v), want synthetic config without settings", config)
	}
}

func TestCollectionScopeInheritsWorkspaceViewSettings(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, true)
	service := NewCollectionApplicationService(tdb.GetDatabase(), permissions)
	actor := AuditActor{UserID: seed.UserID}

	collection, err := service.Create(actor, models.Collection{
		Name: "View settings", WorkspaceID: &seed.WorkspaceID, QLQuery: "",
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	collectionScope := BoardConfigurationScope{CollectionID: &collection.ID}
	workspaceScope := BoardConfigurationScope{WorkspaceID: &seed.WorkspaceID}

	// No rows anywhere: every view is enabled and marked inherited.
	config, err := service.GetBoardConfiguration(seed.UserID, collectionScope)
	if err != nil {
		t.Fatalf("get without rows: %v", err)
	}
	if config.ID != 0 || config.CollectionID == nil {
		t.Fatalf("synthetic collection config = %+v, want id 0 with collection scope", config)
	}
	if !config.ViewSettingsInherited {
		t.Fatal("settings without rows must report inherited")
	}
	assertEnabledViews(t, config.ViewSettings, models.BoardViewIDs)

	// A workspace override is inherited by the collection.
	if _, err := service.PutBoardConfiguration(actor, workspaceScope, models.BoardConfigurationRequest{
		ViewSettings: boardViewSettingsOverride("board", "list"),
	}); err != nil {
		t.Fatalf("workspace override: %v", err)
	}
	config, err = service.GetBoardConfiguration(seed.UserID, collectionScope)
	if err != nil {
		t.Fatalf("get after workspace override: %v", err)
	}
	if !config.ViewSettingsInherited {
		t.Fatal("workspace override must stay inherited on the collection")
	}
	assertEnabledViews(t, config.ViewSettings, []string{"board", "list"})

	// A collection row overrides the workspace set.
	if _, err := service.PutBoardConfiguration(actor, collectionScope, models.BoardConfigurationRequest{
		ViewSettings: boardViewSettingsOverride("board"),
	}); err != nil {
		t.Fatalf("collection override: %v", err)
	}
	config, err = service.GetBoardConfiguration(seed.UserID, collectionScope)
	if err != nil {
		t.Fatalf("get after collection override: %v", err)
	}
	if config.ViewSettingsInherited {
		t.Fatal("explicit collection override must not report inherited")
	}
	assertEnabledViews(t, config.ViewSettings, []string{"board"})

	// Resetting the collection override inherits the workspace set again.
	reset := boardViewSettingsReset()
	if _, err := service.PutBoardConfiguration(actor, collectionScope, models.BoardConfigurationRequest{ViewSettings: reset}); err != nil {
		t.Fatalf("collection reset: %v", err)
	}
	config, err = service.GetBoardConfiguration(seed.UserID, collectionScope)
	if err != nil {
		t.Fatalf("get after collection reset: %v", err)
	}
	if !config.ViewSettingsInherited {
		t.Fatal("reset collection must inherit again")
	}
	assertEnabledViews(t, config.ViewSettings, []string{"board", "list"})

	// The bootstrap carries the same effective settings.
	bootstrap, err := service.GetBoardConfigurationBootstrap(t.Context(), seed.UserID, collectionScope, nil)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if bootstrap.BoardConfiguration == nil {
		t.Fatal("bootstrap board configuration = nil, want effective settings")
	}
	assertEnabledViews(t, bootstrap.BoardConfiguration.ViewSettings, []string{"board", "list"})
	if !bootstrap.BoardConfiguration.ViewSettingsInherited {
		t.Fatal("bootstrap settings must report inherited")
	}
}

func TestWorkspaceDefaultViewConflictOnBoardConfiguration(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, true)
	service := NewCollectionApplicationService(tdb.GetDatabase(), permissions)
	workspaces := NewWorkspaceService(tdb.GetDatabase())
	actor := AuditActor{UserID: seed.UserID}
	scope := BoardConfigurationScope{WorkspaceID: &seed.WorkspaceID}

	defaultView := "board"
	if _, err := workspaces.Update(UpdateWorkspaceParams{ID: seed.WorkspaceID, DefaultView: &defaultView}); err != nil {
		t.Fatalf("set default view: %v", err)
	}

	_, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{
		ViewSettings: boardViewSettingsOverride("list"),
	})
	want := fmt.Sprintf("workspace default view %q must stay enabled; change the workspace default view first", defaultView)
	if err == nil || err.Error() != want {
		t.Fatalf("conflict error = %v, want %q", err, want)
	}

	created, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{
		ViewSettings: boardViewSettingsOverride("board", "list"),
	})
	if err != nil {
		t.Fatalf("override including default view: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("override including default view must persist")
	}
}

func TestWorkspaceUpdateRejectsDisabledDefaultView(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, true)
	service := NewCollectionApplicationService(tdb.GetDatabase(), permissions)
	workspaces := NewWorkspaceService(tdb.GetDatabase())
	actor := AuditActor{UserID: seed.UserID}
	scope := BoardConfigurationScope{WorkspaceID: &seed.WorkspaceID}

	if _, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{
		ViewSettings: boardViewSettingsOverride("board", "list"),
	}); err != nil {
		t.Fatalf("workspace override: %v", err)
	}

	disabled := "map"
	if _, err := workspaces.Update(UpdateWorkspaceParams{ID: seed.WorkspaceID, DefaultView: &disabled}); !errors.Is(err, ErrWorkspaceMutationInvalid) {
		t.Fatalf("disabled default view error = %v, want ErrWorkspaceMutationInvalid", err)
	}

	enabled := "board"
	if _, err := workspaces.Update(UpdateWorkspaceParams{ID: seed.WorkspaceID, DefaultView: &enabled}); err != nil {
		t.Fatalf("enabled default view: %v", err)
	}

	// Unknown legacy values are not part of the toggle set and stay allowed.
	legacy := "kanban"
	if _, err := workspaces.Update(UpdateWorkspaceParams{ID: seed.WorkspaceID, DefaultView: &legacy}); err != nil {
		t.Fatalf("legacy default view: %v", err)
	}
}

func TestPutBoardConfigurationValidatesCompletedVisibility(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, true)
	service := NewCollectionApplicationService(tdb.GetDatabase(), permissions)
	actor := AuditActor{UserID: seed.UserID}
	scope := BoardConfigurationScope{WorkspaceID: &seed.WorkspaceID}

	unknown := "fortnight"
	_, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{
		ViewSettings: &models.ViewSettings{CompletedVisibility: &unknown},
	})
	if err == nil || err.Error() != `unknown completed_visibility "fortnight"` {
		t.Fatalf("PutBoardConfiguration error = %v, want unknown completed_visibility", err)
	}

	for _, value := range models.CompletedVisibilityValues {
		v := value
		t.Run("accepts "+value, func(t *testing.T) {
			_, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{
				ViewSettings: &models.ViewSettings{CompletedVisibility: &v},
			})
			if err != nil {
				t.Fatalf("PutBoardConfiguration(%q): %v", value, err)
			}
		})
	}
}

func TestCollectionScopeInheritsCompletedVisibility(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, true)
	service := NewCollectionApplicationService(tdb.GetDatabase(), permissions)
	actor := AuditActor{UserID: seed.UserID}

	collection, err := service.Create(actor, models.Collection{
		Name: "Completed visibility", WorkspaceID: &seed.WorkspaceID, QLQuery: "",
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	collectionScope := BoardConfigurationScope{CollectionID: &collection.ID}
	workspaceScope := BoardConfigurationScope{WorkspaceID: &seed.WorkspaceID}

	workspaceVisibility := "last_week"
	if _, err := service.PutBoardConfiguration(actor, workspaceScope, models.BoardConfigurationRequest{
		ViewSettings: &models.ViewSettings{CompletedVisibility: &workspaceVisibility},
	}); err != nil {
		t.Fatalf("workspace override: %v", err)
	}

	config, err := service.GetBoardConfiguration(seed.UserID, collectionScope)
	if err != nil {
		t.Fatalf("get collection: %v", err)
	}
	if config.ViewSettings == nil || config.ViewSettings.CompletedVisibility == nil ||
		*config.ViewSettings.CompletedVisibility != "last_week" {
		t.Fatalf("inherited completed visibility = %+v, want last_week", config.ViewSettings)
	}

	// A collection override wins over the workspace default.
	collectionVisibility := "none"
	if _, err := service.PutBoardConfiguration(actor, collectionScope, models.BoardConfigurationRequest{
		ViewSettings: &models.ViewSettings{CompletedVisibility: &collectionVisibility},
	}); err != nil {
		t.Fatalf("collection override: %v", err)
	}
	config, err = service.GetBoardConfiguration(seed.UserID, collectionScope)
	if err != nil {
		t.Fatalf("get collection after override: %v", err)
	}
	if config.ViewSettings == nil || config.ViewSettings.CompletedVisibility == nil ||
		*config.ViewSettings.CompletedVisibility != "none" {
		t.Fatalf("collection completed visibility = %+v, want none", config.ViewSettings)
	}
}
