package tests

import (
	"fmt"
	"net/http"
	"testing"
)

type surfaceTimeProject struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func TestTimeProjectReadsConvergeAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	projectID := createTimeProjectFixture(t, server, "Shared time project")

	bearerGet := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/time/projects/%d", projectID), nil)
	defer bearerGet.Body.Close()
	AssertStatusCode(t, bearerGet, http.StatusOK)
	var bearerProject surfaceTimeProject
	DecodeJSON(t, bearerGet, &bearerProject)
	if bearerProject.ID != projectID || bearerProject.Name != "Shared time project" || bearerProject.Status != "Active" {
		t.Fatalf("bearer project = %+v", bearerProject)
	}

	sessionGet := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/time/projects/%d", projectID), nil)
	defer sessionGet.Body.Close()
	AssertStatusCode(t, sessionGet, http.StatusOK)
	var sessionProject surfaceTimeProject
	DecodeJSON(t, sessionGet, &sessionProject)
	if sessionProject != bearerProject {
		t.Fatalf("session project = %+v, bearer project = %+v", sessionProject, bearerProject)
	}

	bearerList := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/time/projects?status=Active", nil)
	defer bearerList.Body.Close()
	AssertStatusCode(t, bearerList, http.StatusOK)
	var projects []surfaceTimeProject
	DecodeJSON(t, bearerList, &projects)
	for _, project := range projects {
		if project.ID == projectID {
			return
		}
	}
	t.Fatalf("bearer list omitted session-created project %d: %+v", projectID, projects)
}
