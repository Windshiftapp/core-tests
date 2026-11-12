package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestDeletingCustomerOrganisationKeepsDetachedProjectWorklogs reproduces
// GitHub #403: unlinking a project from an organisation and then deleting the
// organisation must not delete the project's time worklogs. Worklogs derive
// their customer from the project (customer -> project -> worklog), so the
// organisation is not a worklog parent.
func TestDeletingCustomerOrganisationKeepsDetachedProjectWorklogs(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	createOrg := MakeAuthRequest(t, server, http.MethodPost, "/customer-organisations",
		map[string]any{"name": "Doomed customer"})
	defer createOrg.Body.Close()
	AssertStatusCode(t, createOrg, http.StatusCreated)
	var org struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, createOrg, &org)

	createProject := MakeAuthRequest(t, server, http.MethodPost, "/time/projects",
		map[string]any{"name": "Detachable project", "customer_id": org.ID})
	defer createProject.Body.Close()
	AssertStatusCode(t, createProject, http.StatusCreated)
	var project struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, createProject, &project)

	createWorklog := MakeAuthRequest(t, server, http.MethodPost, "/v2/time/worklogs",
		map[string]any{"project_id": project.ID, "description": "Billable time", "date": "2026-09-02", "duration": "1h"})
	defer createWorklog.Body.Close()
	AssertStatusCode(t, createWorklog, http.StatusCreated)
	var worklog struct {
		ID         int `json:"id"`
		CustomerID int `json:"customer_id"`
	}
	DecodeJSON(t, createWorklog, &worklog)
	if worklog.CustomerID != org.ID {
		t.Fatalf("worklog customer_id = %d, want organisation %d", worklog.CustomerID, org.ID)
	}

	// Detach the project from the organisation, then delete the organisation.
	detach := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/time/projects/%d", project.ID),
		map[string]any{"customer_id": nil})
	defer detach.Body.Close()
	AssertStatusCode(t, detach, http.StatusOK)

	removeOrg := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/customer-organisations/%d", org.ID), nil)
	defer removeOrg.Body.Close()
	AssertStatusCode(t, removeOrg, http.StatusNoContent)

	// The worklog must survive and now report the built-in no-customer
	// placeholder.
	list := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/time/worklogs?project_id=%d", project.ID), nil)
	defer list.Body.Close()
	AssertStatusCode(t, list, http.StatusOK)
	var page struct {
		Data []struct {
			ID                 int    `json:"id"`
			CustomerBuiltinKey string `json:"customer_builtin_key"`
		} `json:"data"`
	}
	DecodeJSON(t, list, &page)
	if len(page.Data) != 1 || page.Data[0].ID != worklog.ID {
		t.Fatalf("worklog list after organisation delete = %+v, want worklog %d", page.Data, worklog.ID)
	}
	if page.Data[0].CustomerBuiltinKey != "no_customer" {
		t.Fatalf("worklog customer_builtin_key = %q, want no_customer", page.Data[0].CustomerBuiltinKey)
	}
}
