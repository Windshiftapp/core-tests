package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// Owners lose read and edit access when their private-project membership is revoked.
func TestWorklogOwnerReadRequiresProjectViewPermission(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	projectID := createTimeProjectFixture(t, server, "Worklog read gate")

	// Another principal manages the project, so management is restricted and
	// the viewer below can never satisfy the edit gate's manager branch.
	managerID, _, _ := CreateTestUserWithCredentials(t, server, "worklog-manager", "worklog-manager@example.com")
	assignManager := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/v2/time/projects/%d/managers", projectID),
		map[string]any{"principal_type": "user", "principal_id": managerID})
	defer assignManager.Body.Close()
	AssertStatusCode(t, assignManager, http.StatusCreated)

	// Membership grants access to the viewer's own worklog.
	viewerID, username, password := CreateTestUserWithCredentials(t, server, "worklog-viewer", "worklog-viewer@example.com")
	viewer := CreateBearerTokenForUser(t, server, username, password)
	assignViewer := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/v2/time/projects/%d/members", projectID),
		map[string]any{"principal_type": "user", "principal_id": viewerID})
	defer assignViewer.Body.Close()
	AssertStatusCode(t, assignViewer, http.StatusCreated)

	create := MakeAuthRequestWithToken(t, server, viewer, http.MethodPost, "/v2/time/worklogs", map[string]any{
		"project_id": projectID, "description": "Team visible entry", "date": "2026-09-02", "duration": "30m",
	})
	defer create.Body.Close()
	AssertStatusCode(t, create, http.StatusCreated)
	var created struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, create, &created)

	list := MakeAuthRequestWithToken(t, server, viewer, http.MethodGet,
		fmt.Sprintf("/v2/time/worklogs?project_id=%d", projectID), nil)
	defer list.Body.Close()
	AssertStatusCode(t, list, http.StatusOK)
	var page struct {
		Data []struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, list, &page)
	if len(page.Data) != 1 || page.Data[0].ID != created.ID {
		t.Fatalf("viewer worklog list = %+v, want worklog %d", page.Data, created.ID)
	}

	get := MakeAuthRequestWithToken(t, server, viewer, http.MethodGet,
		fmt.Sprintf("/v2/time/worklogs/%d", created.ID), nil)
	defer get.Body.Close()
	AssertStatusCode(t, get, http.StatusOK)
	var fetched struct {
		ID          int    `json:"id"`
		Description string `json:"description"`
	}
	DecodeJSON(t, get, &fetched)
	if fetched.ID != created.ID || fetched.Description != "Team visible entry" {
		t.Fatalf("viewer worklog read = %+v", fetched)
	}

	patch := MakeAuthRequestWithToken(t, server, viewer, http.MethodPatch,
		fmt.Sprintf("/v2/time/worklogs/%d", created.ID), map[string]any{"description": "Owner updated entry"})
	defer patch.Body.Close()
	AssertStatusCode(t, patch, http.StatusOK)

	// Replace the viewer with another member so the project keeps member
	// restrictions, then remove the viewer's membership: view is lost and
	// both the list and the direct read must deny again.
	memberID, _, _ := CreateTestUserWithCredentials(t, server, "worklog-member", "worklog-member@example.com")
	assignMember := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/v2/time/projects/%d/members", projectID),
		map[string]any{"principal_type": "user", "principal_id": memberID})
	defer assignMember.Body.Close()
	AssertStatusCode(t, assignMember, http.StatusCreated)

	members := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/time/projects/%d/members", projectID), nil)
	defer members.Body.Close()
	AssertStatusCode(t, members, http.StatusOK)
	var memberList struct {
		Data []struct {
			ID          int `json:"id"`
			PrincipalID int `json:"principal_id"`
		} `json:"data"`
	}
	DecodeJSON(t, members, &memberList)
	viewerAssignment := 0
	for _, assignment := range memberList.Data {
		if assignment.PrincipalID == viewerID {
			viewerAssignment = assignment.ID
		}
	}
	if viewerAssignment == 0 {
		t.Fatalf("viewer membership assignment not found in %+v", memberList.Data)
	}

	removeViewer := MakeAuthRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/v2/time/projects/%d/members/%d", projectID, viewerAssignment), nil)
	defer removeViewer.Body.Close()
	AssertStatusCode(t, removeViewer, http.StatusNoContent)

	deniedList := MakeAuthRequestWithToken(t, server, viewer, http.MethodGet,
		fmt.Sprintf("/v2/time/worklogs?project_id=%d", projectID), nil)
	defer deniedList.Body.Close()
	AssertStatusCode(t, deniedList, http.StatusOK)
	var deniedPage struct {
		Data []struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, deniedList, &deniedPage)
	if len(deniedPage.Data) != 0 {
		t.Fatalf("restricted viewer worklog list = %+v, want empty", deniedPage.Data)
	}

	deniedGet := MakeAuthRequestWithToken(t, server, viewer, http.MethodGet,
		fmt.Sprintf("/v2/time/worklogs/%d", created.ID), nil)
	defer deniedGet.Body.Close()
	AssertStatusCode(t, deniedGet, http.StatusNotFound)
}
