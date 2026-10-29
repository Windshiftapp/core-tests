package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestTimeProjectWithoutCustomerUsesBuiltinPlaceholder covers the no-customer
// placeholder: a project created without a customer is attributed to the
// built-in organisation, time can be logged on it, and the placeholder itself
// is protected from the customer admin API.
func TestTimeProjectWithoutCustomerUsesBuiltinPlaceholder(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	createProject := MakeAuthRequest(t, server, http.MethodPost, "/time/projects", map[string]any{
		"name": "Internal work",
	})
	defer createProject.Body.Close()
	AssertStatusCode(t, createProject, http.StatusCreated)
	var project struct {
		ID                 int    `json:"id"`
		CustomerID         *int   `json:"customer_id"`
		CustomerBuiltinKey string `json:"customer_builtin_key"`
	}
	DecodeJSON(t, createProject, &project)
	if project.CustomerID == nil {
		t.Fatal("customer_id = nil, want the built-in no-customer placeholder")
	}
	if project.CustomerBuiltinKey != "no_customer" {
		t.Fatalf("customer_builtin_key = %q, want %q", project.CustomerBuiltinKey, "no_customer")
	}

	// Time can be logged on a project with no real customer, and the worklog
	// carries the placeholder so clients can localize its name.
	createWorklog := MakeAuthRequest(t, server, http.MethodPost, "/v2/time/worklogs", map[string]any{
		"project_id": project.ID, "description": "Unassigned work", "date": "2026-09-02", "duration": "30m",
	})
	defer createWorklog.Body.Close()
	AssertStatusCode(t, createWorklog, http.StatusCreated)
	var worklog struct {
		ID                 int    `json:"id"`
		CustomerID         int    `json:"customer_id"`
		CustomerBuiltinKey string `json:"customer_builtin_key"`
	}
	DecodeJSON(t, createWorklog, &worklog)
	if worklog.CustomerID != *project.CustomerID {
		t.Fatalf("worklog customer_id = %d, want %d", worklog.CustomerID, *project.CustomerID)
	}
	if worklog.CustomerBuiltinKey != "no_customer" {
		t.Fatalf("worklog customer_builtin_key = %q, want %q", worklog.CustomerBuiltinKey, "no_customer")
	}

	// The placeholder is system-managed: it cannot be renamed or deleted.
	rename := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/customer-organisations/%d", *project.CustomerID),
		map[string]any{"name": "Renamed placeholder", "active": true})
	defer rename.Body.Close()
	AssertStatusCode(t, rename, http.StatusBadRequest)

	remove := MakeAuthRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/customer-organisations/%d", *project.CustomerID), nil)
	defer remove.Body.Close()
	AssertStatusCode(t, remove, http.StatusBadRequest)
}
