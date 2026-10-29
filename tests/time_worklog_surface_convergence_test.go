package tests

import (
	"fmt"
	"net/http"
	"testing"
)

type surfaceWorklog struct {
	ID              int    `json:"id"`
	ProjectID       int    `json:"project_id"`
	Description     string `json:"description"`
	DurationMinutes int    `json:"duration_minutes"`
}

func TestTimeWorklogLifecycleConvergesAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	projectID := createTimeProjectFixture(t, server, "Worklog convergence")

	create := MakeAuthRequest(t, server, http.MethodPost, "/v2/time/worklogs", map[string]any{
		"project_id": projectID, "description": "Session worklog", "date": "2026-09-02", "duration": "75m",
	})
	defer create.Body.Close()
	AssertStatusCode(t, create, http.StatusCreated)
	var worklog surfaceWorklog
	DecodeJSON(t, create, &worklog)
	if worklog.ID == 0 || worklog.ProjectID != projectID || worklog.DurationMinutes != 75 {
		t.Fatalf("session-created worklog = %+v", worklog)
	}

	list := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/time/worklogs?project_id=%d", projectID), nil)
	defer list.Body.Close()
	AssertStatusCode(t, list, http.StatusOK)
	var page struct {
		Data []surfaceWorklog `json:"data"`
	}
	DecodeJSON(t, list, &page)
	if len(page.Data) != 1 || page.Data[0].ID != worklog.ID || page.Data[0].DurationMinutes != 75 {
		t.Fatalf("bearer worklog list = %+v", page.Data)
	}

	update := MakeBearerRequest(t, server, http.MethodPatch,
		fmt.Sprintf("/rest/api/v2/time/worklogs/%d", worklog.ID), map[string]any{"description": "Bearer description"})
	defer update.Body.Close()
	AssertStatusCode(t, update, http.StatusOK)

	get := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/time/worklogs/%d", worklog.ID), nil)
	defer get.Body.Close()
	AssertStatusCode(t, get, http.StatusOK)
	DecodeJSON(t, get, &worklog)
	if worklog.Description != "Bearer description" || worklog.DurationMinutes != 75 {
		t.Fatalf("session worklog after bearer update = %+v", worklog)
	}

	remove := MakeBearerRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/rest/api/v2/time/worklogs/%d", worklog.ID), nil)
	defer remove.Body.Close()
	AssertStatusCode(t, remove, http.StatusNoContent)

	deleted := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/time/worklogs/%d", worklog.ID), nil)
	defer deleted.Body.Close()
	AssertStatusCode(t, deleted, http.StatusNotFound)
}
