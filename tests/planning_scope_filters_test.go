package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// The planning list endpoints keep global and workspace rows apart unless the
// caller explicitly asks for both. Unscoped lists return global plus
// accessible-workspace rows and narrow to global-only with is_global=true;
// workspace lists return the workspace's own rows and extend with
// include_global=true. Duplicated or cross-scope rows break the management
// views and the item-assignment pickers, so the exact membership is the
// contract under test.

func createPlanningScopeFixture(t *testing.T, server *TestServer, token string, workspaceID int, suffix string) (globalIteration, localIteration, globalMilestone, localMilestone string) {
	t.Helper()

	globalIteration = "Global iteration " + suffix
	localIteration = "Local iteration " + suffix
	globalMilestone = "Global milestone " + suffix
	localMilestone = "Local milestone " + suffix

	iterationBody := func(name string) map[string]any {
		return map[string]any{
			"name": name, "start_date": "2026-09-01", "end_date": "2026-09-14", "status": "planned",
		}
	}
	milestoneBody := func(name string) map[string]any {
		return map[string]any{"name": name, "status": "planning"}
	}

	for _, create := range []struct {
		path string
		body map[string]any
	}{
		{"/v2/iterations", iterationBody(globalIteration)},
		{"/v2/milestones", milestoneBody(globalMilestone)},
	} {
		resp := MakeAuthRequestWithToken(t, server, token, http.MethodPost, create.path, create.body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
	}

	for _, create := range []struct {
		path string
		body map[string]any
	}{
		{fmt.Sprintf("/v2/workspaces/%d/iterations", workspaceID), iterationBody(localIteration)},
		{fmt.Sprintf("/v2/workspaces/%d/milestones", workspaceID), milestoneBody(localMilestone)},
	} {
		resp := MakeAuthRequestWithToken(t, server, token, http.MethodPost, create.path, create.body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
	}

	return globalIteration, localIteration, globalMilestone, localMilestone
}

func planningListNames(t *testing.T, server *TestServer, token, path string) map[string]bool {
	t.Helper()
	resp := MakeAuthRequestWithToken(t, server, token, http.MethodGet, path, nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var page struct {
		Data []struct {
			Name     string `json:"name"`
			IsGlobal bool   `json:"is_global"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &page)
	names := make(map[string]bool, len(page.Data))
	for _, row := range page.Data {
		names[row.Name] = true
	}
	return names
}

func TestPlanningUnscopedIsGlobalFilterReturnsOnlyGlobalRows(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	token := CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Planning is_global filter", shortKey("PGF"))
	suffix := shortKey("f")

	globalIteration, localIteration, globalMilestone, localMilestone := createPlanningScopeFixture(
		t, server, token, workspaceID, suffix,
	)

	iterations := planningListNames(t, server, token, "/v2/iterations?is_global=true")
	if !iterations[globalIteration] {
		t.Fatalf("is_global iteration list omitted the global iteration: %v", iterations)
	}
	if iterations[localIteration] {
		t.Fatalf("is_global iteration list leaked workspace iteration: %v", iterations)
	}

	milestones := planningListNames(t, server, token, "/v2/milestones?is_global=true")
	if !milestones[globalMilestone] {
		t.Fatalf("is_global milestone list omitted the global milestone: %v", milestones)
	}
	if milestones[localMilestone] {
		t.Fatalf("is_global milestone list leaked workspace milestone: %v", milestones)
	}
}

func TestPlanningWorkspaceListScopeFilters(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	token := CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Planning scope primary", shortKey("PSP"))
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "Planning scope other", shortKey("PSO"))
	suffix := shortKey("s")

	globalIteration, localIteration, globalMilestone, localMilestone := createPlanningScopeFixture(
		t, server, token, workspaceID, suffix,
	)

	// The admin can see both workspaces, so a correct include_global response
	// must scope the global rows to the requested workspace only.
	otherIterationBody := map[string]any{
		"name": "Other workspace iteration " + suffix, "start_date": "2026-09-01",
		"end_date": "2026-09-14", "status": "planned",
	}
	otherIteration := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/v2/workspaces/%d/iterations", otherWorkspaceID), otherIterationBody)
	otherIteration.Body.Close()
	AssertStatusCode(t, otherIteration, http.StatusCreated)
	otherMilestoneBody := map[string]any{"name": "Other workspace milestone " + suffix, "status": "planning"}
	otherMilestone := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/v2/workspaces/%d/milestones", otherWorkspaceID), otherMilestoneBody)
	otherMilestone.Body.Close()
	AssertStatusCode(t, otherMilestone, http.StatusCreated)

	t.Run("default lists only the workspace's own rows", func(t *testing.T) {
		iterations := planningListNames(t, server, token, fmt.Sprintf("/v2/workspaces/%d/iterations", workspaceID))
		if !iterations[localIteration] {
			t.Fatalf("workspace iteration list omitted the local iteration: %v", iterations)
		}
		if iterations[globalIteration] || iterations["Other workspace iteration "+suffix] {
			t.Fatalf("default workspace iteration list leaked non-local rows: %v", iterations)
		}

		milestones := planningListNames(t, server, token, fmt.Sprintf("/v2/workspaces/%d/milestones", workspaceID))
		if !milestones[localMilestone] {
			t.Fatalf("workspace milestone list omitted the local milestone: %v", milestones)
		}
		if milestones[globalMilestone] || milestones["Other workspace milestone "+suffix] {
			t.Fatalf("default workspace milestone list leaked non-local rows: %v", milestones)
		}
	})

	t.Run("include_global adds global rows without other workspaces", func(t *testing.T) {
		iterations := planningListNames(t, server, token,
			fmt.Sprintf("/v2/workspaces/%d/iterations?include_global=true", workspaceID))
		if !iterations[localIteration] || !iterations[globalIteration] {
			t.Fatalf("include_global iteration list missed local or global rows: %v", iterations)
		}
		if iterations["Other workspace iteration "+suffix] {
			t.Fatalf("include_global iteration list leaked another workspace's rows: %v", iterations)
		}

		milestones := planningListNames(t, server, token,
			fmt.Sprintf("/v2/workspaces/%d/milestones?include_global=true", workspaceID))
		if !milestones[localMilestone] || !milestones[globalMilestone] {
			t.Fatalf("include_global milestone list missed local or global rows: %v", milestones)
		}
		if milestones["Other workspace milestone "+suffix] {
			t.Fatalf("include_global milestone list leaked another workspace's rows: %v", milestones)
		}
	})

	t.Run("rejects non-boolean scope flags", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet,
			fmt.Sprintf("/v2/workspaces/%d/iterations?include_global=maybe", workspaceID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)

		globalResp := MakeAuthRequest(t, server, http.MethodGet, "/v2/iterations?is_global=maybe", nil)
		defer globalResp.Body.Close()
		AssertStatusCode(t, globalResp, http.StatusBadRequest)
	})
}

func TestPlanningListsHideInaccessibleWorkspaceRows(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)
	hiddenWorkspaceID, _ := CreateTestWorkspace(t, server, "Planning hidden workspace", shortKey("PHW"))
	visibleWorkspaceID, _ := CreateTestWorkspace(t, server, "Planning visible workspace", shortKey("PVW"))
	suffix := shortKey("h")

	// Without an explicit assignment a workspace is open to every user, so
	// gate it before treating its rows as inaccessible.
	LockDownWorkspace(t, server, hiddenWorkspaceID)

	// The hidden workspace's fixture rows (local iteration and milestone) must
	// never surface for a caller without access to that workspace.
	_, hiddenIteration, _, hiddenMilestone := createPlanningScopeFixture(
		t, server, adminToken, hiddenWorkspaceID, suffix,
	)

	_, _, _, visibleMilestone := createPlanningScopeFixture(
		t, server, adminToken, visibleWorkspaceID, suffix+"-v",
	)

	viewerID, viewerUsername, viewerPassword := CreateTestUserWithCredentials(
		t, server, shortKey("plv"), shortKey("plv")+"@example.test",
	)
	AssignWorkspaceRole(t, server, viewerID, visibleWorkspaceID, "Viewer")
	viewerToken := CreateBearerTokenForUser(t, server, viewerUsername, viewerPassword)

	unscopedIterations := planningListNames(t, server, viewerToken, "/v2/iterations")
	if unscopedIterations[hiddenIteration] {
		t.Fatalf("unscoped iteration list leaked inaccessible workspace rows: %v", unscopedIterations)
	}
	unscopedMilestones := planningListNames(t, server, viewerToken, "/v2/milestones")
	if unscopedMilestones[hiddenMilestone] {
		t.Fatalf("unscoped milestone list leaked inaccessible workspace rows: %v", unscopedMilestones)
	}

	includeGlobalIterations := planningListNames(t, server, viewerToken,
		fmt.Sprintf("/v2/workspaces/%d/iterations?include_global=true", visibleWorkspaceID))
	if includeGlobalIterations[hiddenIteration] {
		t.Fatalf("include_global iteration list leaked inaccessible workspace rows: %v", includeGlobalIterations)
	}
	includeGlobalMilestones := planningListNames(t, server, viewerToken,
		fmt.Sprintf("/v2/workspaces/%d/milestones?include_global=true", visibleWorkspaceID))
	if includeGlobalMilestones[hiddenMilestone] {
		t.Fatalf("include_global milestone list leaked inaccessible workspace rows: %v", includeGlobalMilestones)
	}
	if !includeGlobalMilestones[visibleMilestone] {
		t.Fatalf("include_global milestone list omitted the viewer's own workspace milestone: %v", includeGlobalMilestones)
	}
}
