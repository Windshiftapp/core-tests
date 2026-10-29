package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestGlobalPlanningListsIncludeVisibleWorkspaceItems(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Global planning list", shortKey("GPL"))

	milestone := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/v2/workspaces/%d/milestones", workspaceID), map[string]any{
			"name": "Visible local milestone", "status": "planning",
		})
	milestone.Body.Close()
	AssertStatusCode(t, milestone, http.StatusCreated)
	iteration := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/v2/workspaces/%d/iterations", workspaceID), map[string]any{
			"name": "Visible local iteration", "start_date": "2026-09-01", "end_date": "2026-09-14", "status": "planned",
		})
	iteration.Body.Close()
	AssertStatusCode(t, iteration, http.StatusCreated)

	milestones := MakeAuthRequest(t, server, http.MethodGet, "/v2/milestones", nil)
	defer milestones.Body.Close()
	AssertStatusCode(t, milestones, http.StatusOK)
	var milestonePage struct {
		Data []map[string]any `json:"data"`
	}
	DecodeJSON(t, milestones, &milestonePage)
	if !containsPlanningName(milestonePage.Data, "Visible local milestone") {
		t.Fatalf("global milestone list omitted visible workspace milestone: %#v", milestonePage.Data)
	}

	iterations := MakeAuthRequest(t, server, http.MethodGet, "/v2/iterations", nil)
	defer iterations.Body.Close()
	AssertStatusCode(t, iterations, http.StatusOK)
	var iterationPage struct {
		Data []map[string]any `json:"data"`
	}
	DecodeJSON(t, iterations, &iterationPage)
	if !containsPlanningName(iterationPage.Data, "Visible local iteration") {
		t.Fatalf("global iteration list omitted visible workspace iteration: %#v", iterationPage.Data)
	}
}

func containsPlanningName(items []map[string]any, name string) bool {
	for _, item := range items {
		if item["name"] == name {
			return true
		}
	}
	return false
}

func TestMilestoneLifecycleConvergesAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Milestone convergence", shortKey("MSC"))

	created := MakeBearerRequest(t, server, http.MethodPost,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/milestones", workspaceID), map[string]any{
			"name": "Shared milestone", "description": "created by bearer", "status": "planning",
		})
	defer created.Body.Close()
	AssertStatusCode(t, created, http.StatusCreated)
	var milestone map[string]any
	DecodeJSON(t, created, &milestone)
	milestoneID := ExtractIDFromResponse(t, milestone)

	sessionRead := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/milestones/%d", milestoneID), nil)
	defer sessionRead.Body.Close()
	AssertStatusCode(t, sessionRead, http.StatusOK)
	var sessionMilestone map[string]any
	DecodeJSON(t, sessionRead, &sessionMilestone)
	AssertJSONField(t, sessionMilestone, "name", "Shared milestone")

	sessionUpdate := MakeAuthRequest(t, server, http.MethodPatch,
		fmt.Sprintf("/v2/milestones/%d", milestoneID), map[string]any{
			"name": "Updated milestone", "description": "updated by session", "status": "in-progress",
		})
	defer sessionUpdate.Body.Close()
	AssertStatusCode(t, sessionUpdate, http.StatusOK)

	bearerRead := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/milestones/%d", milestoneID), nil)
	defer bearerRead.Body.Close()
	AssertStatusCode(t, bearerRead, http.StatusOK)
	var bearerMilestone map[string]any
	DecodeJSON(t, bearerRead, &bearerMilestone)
	AssertJSONField(t, bearerMilestone, "name", "Updated milestone")
	AssertJSONField(t, bearerMilestone, "status", "in-progress")

	deleted := MakeBearerRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/rest/api/v2/milestones/%d", milestoneID), nil)
	defer deleted.Body.Close()
	AssertStatusCode(t, deleted, http.StatusNoContent)

	missing := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/milestones/%d", milestoneID), nil)
	defer missing.Body.Close()
	AssertStatusCode(t, missing, http.StatusNotFound)
}

func TestIterationLifecycleConvergesAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Iteration convergence", shortKey("ISC"))

	created := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/v2/workspaces/%d/iterations", workspaceID), map[string]any{
		"name":        "Shared iteration",
		"description": "created by session",
		"start_date":  "2026-09-01",
		"end_date":    "2026-09-14",
		"status":      "planned",
	})
	defer created.Body.Close()
	AssertStatusCode(t, created, http.StatusCreated)
	var iteration map[string]any
	DecodeJSON(t, created, &iteration)
	iterationID := ExtractIDFromResponse(t, iteration)

	bearerRead := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/iterations/%d", iterationID), nil)
	defer bearerRead.Body.Close()
	AssertStatusCode(t, bearerRead, http.StatusOK)
	var bearerIteration map[string]any
	DecodeJSON(t, bearerRead, &bearerIteration)
	AssertJSONField(t, bearerIteration, "name", "Shared iteration")

	bearerUpdate := MakeBearerRequest(t, server, http.MethodPatch,
		fmt.Sprintf("/rest/api/v2/iterations/%d", iterationID), map[string]any{
			"name": "Updated iteration", "description": "updated by bearer",
		})
	defer bearerUpdate.Body.Close()
	AssertStatusCode(t, bearerUpdate, http.StatusOK)

	sessionRead := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/iterations/%d", iterationID), nil)
	defer sessionRead.Body.Close()
	AssertStatusCode(t, sessionRead, http.StatusOK)
	var sessionIteration map[string]any
	DecodeJSON(t, sessionRead, &sessionIteration)
	AssertJSONField(t, sessionIteration, "name", "Updated iteration")

	deleted := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/v2/iterations/%d", iterationID), nil)
	defer deleted.Body.Close()
	AssertStatusCode(t, deleted, http.StatusNoContent)

	missing := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/iterations/%d", iterationID), nil)
	defer missing.Body.Close()
	AssertStatusCode(t, missing, http.StatusNotFound)
}
