//go:build test

package handlers

import (
	"net/http"
	"testing"

	"windshift/internal/logger"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestSecuritySettingsHandler_WorkspaceManagedAgentsDefaultsOn(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := NewSecuritySettingsHandler(
		repository.NewSystemSettingRepository(tdb.GetDatabase()),
		logger.NewAuditor(tdb.GetDatabase()),
		false,
	)
	req := testutils.CreateJSONRequest(t, http.MethodGet, "/api/admin/security-settings", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetSecuritySettings, req, nil)

	rr.AssertStatusCode(http.StatusOK)
	var got SecuritySettings
	rr.AssertJSONResponse(&got)
	if !got.WorkspaceManagedAgents {
		t.Fatal("Workspace-Managed Agents must default to enabled")
	}
	if got.AllowExternalImages || handler.ExternalImagesAllowed() {
		t.Fatal("external images must default to disabled")
	}
}

func TestSecuritySettingsHandler_ExternalImagesPersistAndUpdateRuntimePolicy(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	repo := repository.NewSystemSettingRepository(tdb.GetDatabase())
	handler := NewSecuritySettingsHandler(repo, logger.NewAuditor(tdb.GetDatabase()), false)
	update := SecuritySettings{
		CalendarFeedEnabled:    true,
		AllowExternalImages:    true,
		APIKeyCreationPolicy:   "all_users",
		APIKeyAllowedGroupIDs:  []int{},
		MaxAgentsPerUser:       5,
		WorkspaceManagedAgents: true,
	}
	req := testutils.CreateJSONRequest(t, http.MethodPut, "/api/admin/security-settings", update)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateSecuritySettings, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	if !handler.ExternalImagesAllowed() {
		t.Fatal("runtime policy was not enabled after update")
	}
	value, ok, err := repo.GetValue("allow_external_images")
	if err != nil {
		t.Fatalf("load external-image setting: %v", err)
	}
	if !ok || value != "true" {
		t.Fatalf("stored external-image setting = %q, %v; want true, true", value, ok)
	}

	restarted := NewSecuritySettingsHandler(repo, logger.NewAuditor(tdb.GetDatabase()), false)
	if !restarted.ExternalImagesAllowed() {
		t.Fatal("runtime policy did not load the persisted setting")
	}
}

func TestSecuritySettingsHandler_WorkspaceManagedAgentsSharesModuleSetting(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := NewSecuritySettingsHandler(
		repository.NewSystemSettingRepository(tdb.GetDatabase()),
		logger.NewAuditor(tdb.GetDatabase()),
		false,
	)
	update := SecuritySettings{
		CalendarFeedEnabled:    true,
		APIKeyCreationPolicy:   "all_users",
		APIKeyAllowedGroupIDs:  []int{},
		MaxAgentsPerUser:       5,
		WorkspaceManagedAgents: false,
	}
	req := testutils.CreateJSONRequest(t, http.MethodPut, "/api/admin/security-settings", update)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateSecuritySettings, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	var stored string
	if err := tdb.QueryRow(
		"SELECT value FROM system_settings WHERE key = 'workspace_managed_agents'",
	).Scan(&stored); err != nil {
		t.Fatalf("query workspace-managed setting: %v", err)
	}
	if stored != "false" {
		t.Fatalf("expected shared setting false, got %q", stored)
	}

	setup := NewSetupHandler(tdb.GetDatabase(), nil, nil)
	moduleSettings, err := setup.ModuleSettings()
	if err != nil {
		t.Fatalf("load module settings: %v", err)
	}
	if moduleSettings.WorkspaceManagedAgents {
		t.Fatal("Setup/Modules must read the value written by Admin Security")
	}
}
