package handlers

import (
	"path/filepath"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

func TestActionHandlerResolvesCapabilitiesByType(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "v1-actions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	repo := repository.NewActionRepository(db)
	capability := &models.ActionCapability{
		Name:                   "docker",
		CapabilityType:         models.CapabilityDockerEnvironment,
		Config:                 `{"image":"alpine:latest"}`,
		IsEnabled:              true,
		AppliesToAllWorkspaces: true,
	}
	if _, err := repo.CreateCapabilityWithWorkspaces(capability, nil); err != nil {
		t.Fatalf("CreateCapabilityWithWorkspaces: %v", err)
	}
	handler := &ActionHandler{repo: repo}

	if !handler.HasCapabilityOfType(123, capability.ID, models.CapabilityDockerEnvironment) {
		t.Fatal("matching capability type was rejected")
	}
	if handler.HasCapabilityOfType(123, capability.ID, models.CapabilityHTTPClient) {
		t.Fatal("docker capability was accepted as an HTTP capability")
	}
}
