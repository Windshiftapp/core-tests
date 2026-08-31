//go:build test

package handlers

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"windshift/internal/aitools"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func TestWorkspaceAgentToolCapabilitiesRequiresAdminAndUsesCanonicalRegistry(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	data := tdb.SeedTestData(t)

	permissionService, err := services.NewPermissionService(tdb.GetDatabase(), services.PermissionCacheConfig{
		TTL:             time.Minute,
		MaxCacheSize:    16,
		WarmupOnStartup: false,
		PreWarmActive:   false,
		BatchSize:       10,
	})
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })

	handler := NewWorkspaceAgentBindingHandler(nil, nil, permissionService, nil)
	request := func(user *models.User) *testutils.ResponseRecorder {
		t.Helper()
		req := testutils.CreateJSONRequest(
			t,
			http.MethodGet,
			"/api/workspaces/1/agent-tool-capabilities",
			nil,
		)
		req.SetPathValue("workspaceId", strconv.Itoa(data.WorkspaceID))
		return testutils.ExecuteAuthenticatedRequest(t, handler.ToolCapabilities, req, user)
	}

	adminResponse := request(testutils.DefaultTestUser())
	adminResponse.AssertStatusCode(http.StatusOK)
	var got []aitools.CapabilityGroupDefinition
	adminResponse.AssertJSONResponse(&got)
	want := aitools.StandardCapabilityGroups(aitools.Default)
	if len(got) != len(want) {
		t.Fatalf("capability group count = %d, want %d", len(got), len(want))
	}
	if got[0].Key != aitools.CapabilityReadComment || !got[0].Required {
		t.Fatalf("required preset missing from response: %#v", got[0])
	}

	if _, err := tdb.Exec(`
		INSERT INTO users
			(id, email, username, first_name, last_name, password_hash, is_active)
		VALUES
			(2, 'viewer@example.test', 'viewer', 'Read', 'Only', '', true)
	`); err != nil {
		t.Fatalf("insert viewer: %v", err)
	}
	viewerResponse := request(testutils.TestUserWithID(2))
	viewerResponse.AssertStatusCode(http.StatusForbidden)
}
