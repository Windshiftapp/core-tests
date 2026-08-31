package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestMCPAndRESTV1_TestExecutionAuthorizationContract(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Test execution auth", shortKey("TEA"))
	LockDownWorkspace(t, server, workspaceID)
	testCaseID := seedTestCase(t, server, workspaceID, "Protected execution case")
	setID := seedTestSet(t, server, workspaceID, "Protected execution set")
	attachCaseToSet(t, server, workspaceID, setID, testCaseID)

	createRun := func(t *testing.T, name string) (int, int) {
		t.Helper()
		response := MakeAuthRequest(t, server, http.MethodPost,
			fmt.Sprintf("/workspaces/%d/test-runs", workspaceID),
			map[string]interface{}{"name": name, "set_id": setID})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusCreated)
		var run map[string]interface{}
		DecodeJSON(t, response, &run)
		runID := ExtractIDFromResponse(t, run)

		resultsResponse := MakeBearerRequest(t, server, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/workspaces/%d/test-runs/%d/results", workspaceID, runID), nil)
		defer resultsResponse.Body.Close()
		AssertStatusCode(t, resultsResponse, http.StatusOK)
		var results []map[string]interface{}
		DecodeJSON(t, resultsResponse, &results)
		if len(results) != 1 {
			t.Fatalf("run results = %v, want one result", results)
		}
		return runID, intField(results[0], "id")
	}

	viewerID, viewerUsername, viewerPassword := CreateTestUserWithCredentials(t, server, "execution_viewer", "execution_viewer@test.com")
	AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")
	testerID, testerUsername, testerPassword := CreateTestUserWithCredentials(t, server, "execution_tester", "execution_tester@test.com")
	AssignWorkspaceRole(t, server, testerID, workspaceID, "Tester")

	t.Run("missing write scope is denied by both token gates", func(t *testing.T) {
		runID, resultID := createRun(t, "Missing scope run")
		token := createTokenWithScopesAsUser(t, server, testerUsername, testerPassword,
			[]string{"mcp:access", "tests:read"})

		response := MakeBearerRequestWithToken(t, server, token, http.MethodPut,
			fmt.Sprintf("/rest/api/v1/workspaces/%d/test-runs/%d/results/%d", workspaceID, runID, resultID),
			map[string]interface{}{"status": "passed"})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusForbidden)

		body, err := callMCPForContract(dialMCPWithToken(t, server, token), "record_test_result", map[string]interface{}{
			"workspace_id": workspaceID, "run_id": runID, "test_case_id": testCaseID, "status": "passed",
		})
		if err == nil && !strings.Contains(body, "tests:write") {
			t.Fatalf("MCP missing-scope denial = %q, want tests:write", body)
		}
		if err != nil && !strings.Contains(err.Error(), "403") && !strings.Contains(err.Error(), "Forbidden") && !strings.Contains(err.Error(), "tests:write") && !strings.Contains(err.Error(), "scope") {
			t.Fatalf("MCP missing-scope error = %v", err)
		}
	})

	t.Run("viewer execution is existence-masked on both surfaces", func(t *testing.T) {
		runID, resultID := createRun(t, "Viewer denied run")
		token := createTokenWithScopesAsUser(t, server, viewerUsername, viewerPassword,
			[]string{"mcp:access", "tests:read", "tests:write"})

		response := MakeBearerRequestWithToken(t, server, token, http.MethodPut,
			fmt.Sprintf("/rest/api/v1/workspaces/%d/test-runs/%d/results/%d", workspaceID, runID, resultID),
			map[string]interface{}{"status": "passed"})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusNotFound)

		body, err := callMCPForContract(dialMCPWithToken(t, server, token), "record_test_result", map[string]interface{}{
			"workspace_id": workspaceID, "run_id": runID, "test_case_id": testCaseID, "status": "passed",
		})
		if err != nil {
			t.Fatalf("MCP viewer execution denial: %v", err)
		}
		if !strings.Contains(body, "test run not found") {
			t.Fatalf("MCP viewer execution denial = %q, want test run not found", body)
		}

		assertTestRunResultStatus(t, server, workspaceID, runID, resultID, "not_run")
	})

	t.Run("tester mutation succeeds with the same permission on both surfaces", func(t *testing.T) {
		runID, resultID := createRun(t, "Tester execution run")
		token := createTokenWithScopesAsUser(t, server, testerUsername, testerPassword,
			[]string{"mcp:access", "tests:read", "tests:write"})

		response := MakeBearerRequestWithToken(t, server, token, http.MethodPut,
			fmt.Sprintf("/rest/api/v1/workspaces/%d/test-runs/%d/results/%d", workspaceID, runID, resultID),
			map[string]interface{}{"status": "passed", "actual_result": "REST result"})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
		assertTestRunResultStatus(t, server, workspaceID, runID, resultID, "passed")

		body, err := callMCPForContract(dialMCPWithToken(t, server, token), "record_test_result", map[string]interface{}{
			"workspace_id": workspaceID, "run_id": runID, "test_case_id": testCaseID,
			"status": "failed", "actual_result": "MCP result",
		})
		if err != nil {
			t.Fatalf("MCP tester execution: %v", err)
		}
		if !strings.Contains(body, `"success":true`) || !strings.Contains(body, `"status":"failed"`) {
			t.Fatalf("MCP tester execution response = %q", body)
		}
		assertTestRunResultStatus(t, server, workspaceID, runID, resultID, "failed")
	})
}

func assertTestRunResultStatus(t *testing.T, server *TestServer, workspaceID, runID, resultID int, want string) {
	t.Helper()
	response := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/test-runs/%d/results", workspaceID, runID), nil)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)
	var results []map[string]interface{}
	DecodeJSON(t, response, &results)
	for _, result := range results {
		if intField(result, "id") == resultID {
			if got, _ := result["status"].(string); got != want {
				t.Fatalf("result status = %q, want %q", got, want)
			}
			return
		}
	}
	t.Fatalf("result %d not found in %v", resultID, results)
}
