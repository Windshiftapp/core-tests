package tests

import (
	"fmt"
	"net/http"
	"testing"
)

type surfaceStatus struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	CategoryID    int    `json:"category_id"`
	CategoryName  string `json:"category_name"`
	CategoryColor string `json:"category_color"`
	IsDefault     bool   `json:"is_default"`
	IsCompleted   bool   `json:"is_completed"`
}

type surfaceStatusCategory struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	IsDefault   bool   `json:"is_default"`
	IsCompleted bool   `json:"is_completed"`
}

func TestStatusCatalogsConvergeAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Status surface convergence", shortKey("SSC"))

	var sessionStatuses, bearerStatuses []surfaceStatus
	decodeSessionAndBearer(t, server, "/v2/statuses", "/rest/api/v2/statuses", &sessionStatuses, &bearerStatuses)
	assertStatusSlicesEqual(t, sessionStatuses, bearerStatuses)

	var sessionCategories, bearerCategories []surfaceStatusCategory
	decodeSessionAndBearer(t, server, "/v2/status-categories", "/rest/api/v2/status-categories", &sessionCategories, &bearerCategories)
	if len(sessionCategories) != len(bearerCategories) {
		t.Fatalf("status category counts: session=%d bearer=%d", len(sessionCategories), len(bearerCategories))
	}
	for i := range sessionCategories {
		if sessionCategories[i] != bearerCategories[i] {
			t.Fatalf("status category %d: session=%+v bearer=%+v", i, sessionCategories[i], bearerCategories[i])
		}
	}

	var sessionWorkspace, bearerWorkspace []surfaceStatus
	decodeSessionAndBearer(t, server,
		fmt.Sprintf("/v2/workspaces/%d/statuses", workspaceID),
		fmt.Sprintf("/rest/api/v2/workspaces/%d/statuses", workspaceID),
		&sessionWorkspace, &bearerWorkspace)
	assertStatusSlicesEqual(t, sessionWorkspace, bearerWorkspace)
}

func decodeSessionAndBearer(t *testing.T, server *TestServer, sessionPath, bearerPath string, sessionTarget, bearerTarget any) {
	t.Helper()
	sessionResponse := MakeAuthRequest(t, server, http.MethodGet, sessionPath, nil)
	defer sessionResponse.Body.Close()
	AssertStatusCode(t, sessionResponse, http.StatusOK)
	DecodeJSON(t, sessionResponse, sessionTarget)

	bearerResponse := MakeBearerRequest(t, server, http.MethodGet, bearerPath, nil)
	defer bearerResponse.Body.Close()
	AssertStatusCode(t, bearerResponse, http.StatusOK)
	DecodeJSON(t, bearerResponse, bearerTarget)
}

func assertStatusSlicesEqual(t *testing.T, session, bearer []surfaceStatus) {
	t.Helper()
	if len(session) != len(bearer) {
		t.Fatalf("status counts: session=%d bearer=%d", len(session), len(bearer))
	}
	for i := range session {
		if session[i] != bearer[i] {
			t.Fatalf("status %d: session=%+v bearer=%+v", i, session[i], bearer[i])
		}
	}
}
