//go:build test

package services

import (
	"errors"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// newKnowledgeBaseValidationFixture wires a real permission service and the
// config-update service under test. The seeded user administers workspace 1
// only; a second workspace stands in for a knowledge-base source the actor
// cannot administer.
func newKnowledgeBaseValidationFixture(t *testing.T, grantAdmin bool) (*ChannelConfigUpdateService, int, int, int) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	var otherWorkspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, active) VALUES ('Unmanaged KB workspace', 'UKBW', true) RETURNING id
	`).Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("create unmanaged workspace: %v", err)
	}

	if grantAdmin {
		if _, err := tdb.ExecWrite(`
			INSERT INTO user_global_permissions (user_id, permission_id)
			SELECT ?, id FROM permissions WHERE permission_key = 'system.admin'
		`, data.UserID); err != nil {
			t.Fatalf("grant system administrator: %v", err)
		}
	}

	permissions, err := NewPermissionService(tdb.GetDatabase(), DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	service := NewChannelConfigUpdateService(nil, permissions)
	service.SetKnowledgeBasePageValidator(func(workspaceID, pageID int) error { return nil })
	return service, data.UserID, data.WorkspaceID, otherWorkspaceID
}

func knowledgeBaseConfig(workspaces []int, sources ...models.KnowledgeBasePageSource) *models.ChannelConfig {
	return &models.ChannelConfig{
		PortalWorkspaceIDs:      workspaces,
		KnowledgeBasePageSources: sources,
	}
}

func assertKnowledgeBaseForbidden(t *testing.T, err error) {
	t.Helper()
	var configErr *ChannelConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("error = %v, want *ChannelConfigError", err)
	}
	if configErr.Kind != ChannelConfigWorkspaceForbidden {
		t.Fatalf("error kind = %v, want ChannelConfigWorkspaceForbidden", configErr.Kind)
	}
}

// WI-1680: editing unrelated portal settings must not re-authorize knowledge
// base sources that already exist unchanged, while adding or changing a source
// still requires workspace administration.
func TestValidateKnowledgeBasePageSources_OnlyAuthorizesNewOrChangedSources(t *testing.T) {
	service, actorID, managedWorkspace, unmanagedWorkspace := newKnowledgeBaseValidationFixture(t, false)
	rootA, rootB := 10, 11
	workspaces := []int{managedWorkspace, unmanagedWorkspace}

	t.Run("unchanged source on an unadministered workspace is allowed", func(t *testing.T) {
		source := models.KnowledgeBasePageSource{WorkspaceID: unmanagedWorkspace, RootPageID: &rootA}
		stored := knowledgeBaseConfig(workspaces, source)
		config := knowledgeBaseConfig(workspaces, source)

		if err := service.validateKnowledgeBasePageSources(actorID, stored, config); err != nil {
			t.Fatalf("unchanged source rejected: %v", err)
		}
	})

	t.Run("unchanged source is order-independent", func(t *testing.T) {
		first := models.KnowledgeBasePageSource{WorkspaceID: unmanagedWorkspace, RootPageID: &rootA}
		second := models.KnowledgeBasePageSource{WorkspaceID: managedWorkspace}
		stored := knowledgeBaseConfig(workspaces, first, second)
		config := knowledgeBaseConfig(workspaces, second, first)

		if err := service.validateKnowledgeBasePageSources(actorID, stored, config); err != nil {
			t.Fatalf("reordered sources rejected: %v", err)
		}
	})

	t.Run("new source on an unadministered workspace is forbidden", func(t *testing.T) {
		stored := knowledgeBaseConfig(workspaces)
		config := knowledgeBaseConfig(workspaces, models.KnowledgeBasePageSource{WorkspaceID: unmanagedWorkspace})

		assertKnowledgeBaseForbidden(t, service.validateKnowledgeBasePageSources(actorID, stored, config))
	})

	t.Run("changed root page is forbidden", func(t *testing.T) {
		stored := knowledgeBaseConfig(workspaces, models.KnowledgeBasePageSource{WorkspaceID: unmanagedWorkspace, RootPageID: &rootA})
		config := knowledgeBaseConfig(workspaces, models.KnowledgeBasePageSource{WorkspaceID: unmanagedWorkspace, RootPageID: &rootB})

		assertKnowledgeBaseForbidden(t, service.validateKnowledgeBasePageSources(actorID, stored, config))
	})

	t.Run("source on an administered workspace is allowed", func(t *testing.T) {
		stored := knowledgeBaseConfig(workspaces)
		config := knowledgeBaseConfig(workspaces, models.KnowledgeBasePageSource{WorkspaceID: managedWorkspace})

		if err := service.validateKnowledgeBasePageSources(actorID, stored, config); err != nil {
			t.Fatalf("managed source rejected: %v", err)
		}
	})
}

func TestValidateKnowledgeBasePageSources_SystemAdminBypassesWorkspaceAdmin(t *testing.T) {
	service, actorID, managedWorkspace, unmanagedWorkspace := newKnowledgeBaseValidationFixture(t, true)
	workspaces := []int{managedWorkspace, unmanagedWorkspace}

	stored := knowledgeBaseConfig(workspaces)
	config := knowledgeBaseConfig(workspaces, models.KnowledgeBasePageSource{WorkspaceID: unmanagedWorkspace})

	if err := service.validateKnowledgeBasePageSources(actorID, stored, config); err != nil {
		t.Fatalf("system admin source rejected: %v", err)
	}
}
