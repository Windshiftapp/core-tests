//go:build test

package services

import (
	"reflect"
	"testing"

	"windshift/internal/models"
)

func TestBoardMilestoneSwimlanesRoundTripAndMerge(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, true)
	service := NewCollectionApplicationService(tdb.GetDatabase(), permissions)
	actor := AuditActor{UserID: seed.UserID}
	scope := BoardConfigurationScope{WorkspaceID: &seed.WorkspaceID}

	// A fresh configuration without an explicit preference stores no milestone
	// swimlane settings; the board falls back to showing completed lanes.
	created, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{})
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
	if config.ViewSettings != nil && config.ViewSettings.MilestoneSwimlanes != nil {
		t.Fatalf("default milestone swimlanes = %+v, want nil", config.ViewSettings.MilestoneSwimlanes)
	}

	// An explicit preference round-trips through the board-options JSON.
	want := &models.MilestoneSwimlaneConfig{
		GlobalMilestoneIDs:    []int{3, 7},
		ShowCompleted:         false,
		ShowOpenFromCompleted: true,
	}
	if _, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{
		ViewSettings: &models.ViewSettings{MilestoneSwimlanes: want},
	}); err != nil {
		t.Fatalf("set preference: %v", err)
	}
	config, err = service.GetBoardConfiguration(seed.UserID, scope)
	if err != nil {
		t.Fatalf("get after set: %v", err)
	}
	if config.ViewSettings == nil || !reflect.DeepEqual(config.ViewSettings.MilestoneSwimlanes, want) {
		t.Fatalf("milestone swimlanes = %+v, want %+v", config.ViewSettings, want)
	}

	// A later update that does not carry the field (for example the board
	// settings page) must keep the stored preference instead of resetting it.
	if _, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{}); err != nil {
		t.Fatalf("update without milestone swimlanes: %v", err)
	}
	config, err = service.GetBoardConfiguration(seed.UserID, scope)
	if err != nil {
		t.Fatalf("get after merge: %v", err)
	}
	if config.ViewSettings == nil || !reflect.DeepEqual(config.ViewSettings.MilestoneSwimlanes, want) {
		t.Fatalf("milestone swimlanes after merge = %+v, want %+v", config.ViewSettings, want)
	}

	// An explicit update replaces the stored preference.
	replacement := &models.MilestoneSwimlaneConfig{
		GlobalMilestoneIDs:    []int{11},
		ShowCompleted:         true,
		ShowOpenFromCompleted: true,
	}
	if _, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{
		ViewSettings: &models.ViewSettings{MilestoneSwimlanes: replacement},
	}); err != nil {
		t.Fatalf("replace preference: %v", err)
	}
	config, err = service.GetBoardConfiguration(seed.UserID, scope)
	if err != nil {
		t.Fatalf("get after replace: %v", err)
	}
	if config.ViewSettings == nil || !reflect.DeepEqual(config.ViewSettings.MilestoneSwimlanes, replacement) {
		t.Fatalf("milestone swimlanes after replace = %+v, want %+v", config.ViewSettings, replacement)
	}
}

func TestCollectionBoardInheritsWorkspaceMilestoneSwimlanes(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, true)
	service := NewCollectionApplicationService(tdb.GetDatabase(), permissions)
	actor := AuditActor{UserID: seed.UserID}

	collection, err := service.Create(actor, models.Collection{
		Name: "Milestone lanes", WorkspaceID: &seed.WorkspaceID, QLQuery: "",
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	workspaceScope := BoardConfigurationScope{WorkspaceID: &seed.WorkspaceID}
	collectionScope := BoardConfigurationScope{CollectionID: &collection.ID}

	workspacePreference := &models.MilestoneSwimlaneConfig{
		GlobalMilestoneIDs:    []int{5},
		ShowCompleted:         false,
		ShowOpenFromCompleted: true,
	}
	if _, err := service.PutBoardConfiguration(actor, workspaceScope, models.BoardConfigurationRequest{
		ViewSettings: &models.ViewSettings{MilestoneSwimlanes: workspacePreference},
	}); err != nil {
		t.Fatalf("workspace preference: %v", err)
	}

	// The collection has no board row yet, so it inherits the workspace default
	// without losing its own effective enabled-views resolution.
	config, err := service.GetBoardConfiguration(seed.UserID, collectionScope)
	if err != nil {
		t.Fatalf("get collection: %v", err)
	}
	if config.ViewSettings == nil || !reflect.DeepEqual(config.ViewSettings.MilestoneSwimlanes, workspacePreference) {
		t.Fatalf("inherited milestone swimlanes = %+v, want %+v", config.ViewSettings, workspacePreference)
	}

	// An explicit collection preference wins over the workspace default.
	collectionPreference := &models.MilestoneSwimlaneConfig{
		GlobalMilestoneIDs:    []int{9},
		ShowCompleted:         true,
		ShowOpenFromCompleted: false,
	}
	if _, err := service.PutBoardConfiguration(actor, collectionScope, models.BoardConfigurationRequest{
		ViewSettings: &models.ViewSettings{MilestoneSwimlanes: collectionPreference},
	}); err != nil {
		t.Fatalf("collection preference: %v", err)
	}
	config, err = service.GetBoardConfiguration(seed.UserID, collectionScope)
	if err != nil {
		t.Fatalf("get collection after set: %v", err)
	}
	if config.ViewSettings == nil || !reflect.DeepEqual(config.ViewSettings.MilestoneSwimlanes, collectionPreference) {
		t.Fatalf("collection milestone swimlanes = %+v, want %+v", config.ViewSettings, collectionPreference)
	}
}
