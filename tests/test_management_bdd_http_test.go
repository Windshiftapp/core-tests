//go:build test

package tests

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// gherkinAuthRequest posts or sends a raw body with a custom content type
// (text/x-gherkin, application/merge-patch+json) on the v2 surface.
func gherkinAuthRequest(t *testing.T, ts *TestServer, method, endpoint, contentType, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, ts.APIBase+endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	if ts.SessionCookie != "" {
		req.Header.Set("Cookie", ts.SessionCookie)
	}
	resp, err := testHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

const bddOutlineFeature = `Feature: Login
  Scenario Outline: valid credentials
    Given the <role> role
    Examples:
      | role |
      | admin |
`

// seedBDDCaseWithRun creates a BDD outline case, a set containing it, and a
// run over the set. It returns the workspace, run, and case IDs.
func seedBDDCaseWithRun(t *testing.T, ts *TestServer, name string) (workspaceID, runID, caseID int) {
	t.Helper()
	workspaceID, _ = CreateTestWorkspace(t, ts, name, shortKey("BDD"))
	createBody := map[string]interface{}{"format": "bdd", "gherkin": bddOutlineFeature}
	resp := MakeAuthRequest(t, ts, http.MethodPost, fmt.Sprintf("/v2/workspaces/%d/test-cases", workspaceID), createBody)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, resp, &created)
	caseID = ExtractIDFromResponse(t, created)

	setID := seedTestSet(t, ts, workspaceID, name+" set")
	attachCaseToSet(t, ts, workspaceID, setID, caseID)

	runResp := MakeAuthRequest(t, ts, http.MethodPost, fmt.Sprintf("/v2/workspaces/%d/test-runs", workspaceID),
		map[string]interface{}{"name": name + " run", "plan_id": setID})
	defer runResp.Body.Close()
	AssertStatusCode(t, runResp, http.StatusCreated)
	var run map[string]interface{}
	DecodeJSON(t, runResp, &run)
	runID = ExtractIDFromResponse(t, run)
	return workspaceID, runID, caseID
}

func TestBDDValidateFeatureAppliesCaseRules(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "BDD validate", shortKey("BDV"))

	tests := []struct {
		name       string
		feature    string
		wantValid  bool
		wantErrors []string
	}{
		{
			name:      "single named scenario is valid",
			feature:   bddOutlineFeature,
			wantValid: true,
		},
		{
			name:       "multiple scenarios fail the single-scenario rule",
			feature:    "Feature: Login\nScenario: one\nGiven a\nScenario: two\nGiven b\n",
			wantValid:  false,
			wantErrors: []string{"exactly one Scenario"},
		},
		{
			name:       "unnamed scenario fails the name rule",
			feature:    "Feature: Login\nScenario:\nGiven a\n",
			wantValid:  false,
			wantErrors: []string{"the scenario needs a name"},
		},
		{
			name:       "parse errors are reported",
			feature:    "Feature: Login\nScenario: broken\nAnd cannot be first\n",
			wantValid:  false,
			wantErrors: []string{"cannot be the first step"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := gherkinAuthRequest(t, server, http.MethodPost,
				fmt.Sprintf("/v2/workspaces/%d/test-cases/validate-feature", workspaceID), "text/x-gherkin", tt.feature)
			defer resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusOK)
			var result struct {
				Valid  bool `json:"valid"`
				Errors []struct {
					Message string `json:"message"`
				} `json:"errors"`
			}
			DecodeJSON(t, resp, &result)
			if result.Valid != tt.wantValid {
				t.Fatalf("valid = %v with errors %v, want %v", result.Valid, result.Errors, tt.wantValid)
			}
			joined := make([]string, 0, len(result.Errors))
			for _, e := range result.Errors {
				joined = append(joined, e.Message)
			}
			all := strings.Join(joined, "\n")
			for _, want := range tt.wantErrors {
				if !strings.Contains(all, want) {
					t.Fatalf("errors = %q, want text %q", all, want)
				}
			}
		})
	}
}

func TestBDDImportFeatureStoresEditablePerCaseSource(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "BDD import", shortKey("BDI"))

	source := "Feature: Login\n  Scenario: valid credentials\n    Given the login page\n  Scenario Outline: role based access\n    Given the <role> role\n    Examples:\n      | role |\n      | admin |\n"
	resp := gherkinAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/v2/workspaces/%d/test-cases/import-feature?all=true", workspaceID), "text/x-gherkin", source)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var imported struct {
		Cases []struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		} `json:"cases"`
	}
	DecodeJSON(t, resp, &imported)
	if len(imported.Cases) != 2 {
		t.Fatalf("imported %d cases, want 2", len(imported.Cases))
	}

	for _, tc := range imported.Cases {
		detailResp := MakeAuthRequest(t, server, http.MethodGet,
			fmt.Sprintf("/v2/workspaces/%d/test-cases/%d", workspaceID, tc.ID), nil)
		var detail map[string]interface{}
		DecodeJSON(t, detailResp, &detail)
		detailResp.Body.Close()
		bdd, _ := detail["bdd"].(map[string]interface{})
		if bdd == nil {
			t.Fatalf("case %d detail carries no bdd payload", tc.ID)
		}
		gherkin, _ := bdd["gherkin"].(string)
		if gherkin == source {
			t.Fatalf("case %d stored the full feature file as its source", tc.ID)
		}
		if !strings.Contains(gherkin, tc.Title) {
			t.Fatalf("case %d stored source %q lost its scenario %q", tc.ID, gherkin, tc.Title)
		}

		// The stored source must pass the single-scenario rule: editing the
		// case right after import works without manual trimming. Plain
		// scenarios take a trailing step; outlines insert one before Examples.
		var edited string
		if strings.Contains(gherkin, "    Examples:") {
			edited = strings.Replace(gherkin, "    Examples:", "    And an extra step\n    Examples:", 1)
		} else {
			edited = gherkin + "\n    And an extra step\n"
		}
		if edited == gherkin {
			t.Fatalf("no edit could be applied to the stored source of case %d:\n%s", tc.ID, gherkin)
		}
		patchResp := gherkinAuthRequest(t, server, http.MethodPatch,
			fmt.Sprintf("/v2/workspaces/%d/test-cases/%d", workspaceID, tc.ID),
			"application/merge-patch+json", `{"gherkin":`+jsonString(edited)+`}`)
		defer patchResp.Body.Close()
		if patchResp.StatusCode != http.StatusOK {
			body := new(strings.Builder)
			_, _ = io.Copy(body, patchResp.Body)
			t.Fatalf("editing imported case %d returned %d, want 200: %s", tc.ID, patchResp.StatusCode, body.String())
		}
	}
}

func TestBDDExampleResultPatchKeepsUntouchedFields(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)
	workspaceID, runID, caseID := seedBDDCaseWithRun(t, server, "BDD patch")
	examplePath := fmt.Sprintf("/v2/workspaces/%d/test-runs/%d/test-cases/%d/examples/0", workspaceID, runID, caseID)

	// A notes-only patch keeps the example's status.
	notesResp := gherkinAuthRequest(t, server, http.MethodPatch, examplePath,
		"application/merge-patch+json", `{"notes":"console error on submit"}`)
	defer notesResp.Body.Close()
	AssertStatusCode(t, notesResp, http.StatusOK)
	var patched map[string]interface{}
	DecodeJSON(t, notesResp, &patched)
	if patched["status"] != "not_run" {
		t.Fatalf("notes-only patch changed status to %v, want not_run", patched["status"])
	}
	if patched["notes"] != "console error on submit" {
		t.Fatalf("notes = %v, want the submitted note", patched["notes"])
	}

	// A status-only patch keeps the notes.
	statusResp := gherkinAuthRequest(t, server, http.MethodPatch, examplePath,
		"application/merge-patch+json", `{"status":"failed"}`)
	defer statusResp.Body.Close()
	AssertStatusCode(t, statusResp, http.StatusOK)
	DecodeJSON(t, statusResp, &patched)
	if patched["status"] != "failed" || patched["notes"] != "console error on submit" {
		t.Fatalf("status-only patch = %+v, want failed status with notes preserved", patched)
	}

	// An invalid status is rejected without touching the row.
	invalidResp := gherkinAuthRequest(t, server, http.MethodPatch, examplePath,
		"application/merge-patch+json", `{"status":"exploded"}`)
	defer invalidResp.Body.Close()
	AssertStatusCode(t, invalidResp, http.StatusBadRequest)

	// The run detail reports the case format alongside each result and lists
	// the per-example results.
	detailResp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/workspaces/%d/test-runs/%d/detail", workspaceID, runID), nil)
	var detail struct {
		Results []struct {
			TestCaseID     int    `json:"test_case_id"`
			Status         string `json:"status"`
			TestCaseFormat string `json:"test_case_format"`
		} `json:"results"`
		ExampleResults []struct {
			TestCaseID   int    `json:"test_case_id"`
			ExampleIndex int    `json:"example_index"`
			Status       string `json:"status"`
			Notes        string `json:"notes"`
		} `json:"example_results"`
	}
	DecodeJSON(t, detailResp, &detail)
	detailResp.Body.Close()
	var result *struct {
		TestCaseID     int    `json:"test_case_id"`
		Status         string `json:"status"`
		TestCaseFormat string `json:"test_case_format"`
	}
	for i := range detail.Results {
		if detail.Results[i].TestCaseID == caseID {
			result = &detail.Results[i]
		}
	}
	if result == nil {
		t.Fatalf("run detail has no result for case %d: %+v", caseID, detail.Results)
	}
	if result.TestCaseFormat != "bdd" {
		t.Fatalf("test_case_format = %q, want bdd", result.TestCaseFormat)
	}
	if len(detail.ExampleResults) != 1 || detail.ExampleResults[0].Status != "failed" || detail.ExampleResults[0].Notes != "console error on submit" {
		t.Fatalf("example results = %+v, want the persisted merge-patched row", detail.ExampleResults)
	}
}

func TestBDDExampleResultPatchRejectsForeignWorkspaceAccess(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)
	_, runID, caseID := seedBDDCaseWithRun(t, server, "BDD scoped")
	foreignWorkspace, _ := CreateTestWorkspace(t, server, "BDD foreign", shortKey("BDF"))

	resp := gherkinAuthRequest(t, server, http.MethodPatch,
		fmt.Sprintf("/v2/workspaces/%d/test-runs/%d/test-cases/%d/examples/0", foreignWorkspace, runID, caseID),
		"application/merge-patch+json", `{"status":"passed"}`)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusNotFound)
}

// jsonString encodes a Go string as a JSON string literal.
func jsonString(value string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\n':
			builder.WriteString(`\n`)
		case '\t':
			builder.WriteString(`\t`)
		case '\r':
			builder.WriteString(`\r`)
		default:
			builder.WriteRune(r)
		}
	}
	builder.WriteByte('"')
	return builder.String()
}
