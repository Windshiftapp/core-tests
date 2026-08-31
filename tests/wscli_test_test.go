// wscli_test_test exercises `ws test ...` end-to-end against an isolated
// test server, hitting the v1 surface introduced by WI-68 (the WI-78
// test-half repoint).
//
// Setup uses the admin cookie session against /api/* to seed a test
// case + test set + test run, then drives the lifecycle through the
// in-process CLI bound to the bearer token. A regression that bounces
// `ws test` back to /api/* will 401 here.
package tests

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

func TestWSCLI_Test_RunLifecycle(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	// 1. Seed a test case via the cookie API (admin session). Test catalog
	//    CRUD stays cookie-only until a follow-up ticket; we rely on the
	//    cookie surface here purely to set up state for the v1 lifecycle.
	caseID := seedTestCase(t, ts, w.Alpha.ID, "WSCLI Login Test")

	// 2. Seed a test set and attach the case so a run can be started.
	setID := seedTestSet(t, ts, w.Alpha.ID, "WSCLI Smoke Suite")
	attachCaseToSet(t, ts, w.Alpha.ID, setID, caseID)

	// 3. `ws test set ls` — list sets should include the one we just made.
	out, stderr, code := runWS(t, ts, "test", "set", "ls", "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	setIDs := idsFromJSONArray(t, out)
	if !containsInt(setIDs, setID) {
		t.Fatalf("test set ls: missing set id %d, got %v", setID, setIDs)
	}

	// 4. `ws test set get <id>` returns the set.
	out, stderr, code = runWS(t, ts, "test", "set", "get", strconv.Itoa(setID), "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	if got := jsonInt(t, out, "id"); got != setID {
		t.Fatalf("test set get: id = %d, want %d", got, setID)
	}

	// 5. `ws test case ls` returns the seeded case.
	out, stderr, code = runWS(t, ts, "test", "case", "ls", "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	caseIDs := idsFromJSONArray(t, out)
	if !containsInt(caseIDs, caseID) {
		t.Fatalf("test case ls: missing case id %d, got %v", caseID, caseIDs)
	}

	// 6. `ws test case get <id>` returns the case.
	out, stderr, code = runWS(t, ts, "test", "case", "get", strconv.Itoa(caseID), "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	if got := jsonInt(t, out, "id"); got != caseID {
		t.Fatalf("test case get: id = %d, want %d", got, caseID)
	}

	// 7. `ws test run start <set>` creates a new run.
	out, stderr, code = runWS(t, ts, "test", "run", "start", strconv.Itoa(setID), "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	runID := jsonInt(t, out, "id")
	if runID == 0 {
		t.Fatalf("test run start: id is zero, body=%s", string(out))
	}

	// 8. `ws test run ls` returns the new run.
	out, stderr, code = runWS(t, ts, "test", "run", "ls", "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	runIDs := idsFromJSONArray(t, out)
	if !containsInt(runIDs, runID) {
		t.Fatalf("test run ls: missing run id %d, got %v", runID, runIDs)
	}

	// 9. `ws test run get <id>` returns the run.
	out, stderr, code = runWS(t, ts, "test", "run", "get", strconv.Itoa(runID), "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	if got := jsonInt(t, out, "id"); got != runID {
		t.Fatalf("test run get: id = %d, want %d", got, runID)
	}

	// 10. `ws test result <run-id> <case-id> passed` records a result.
	out, stderr, code = runWS(t, ts, "test", "result", strconv.Itoa(runID), strconv.Itoa(caseID), "passed", "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)

	// 11. Fetch results via the cookie API (the CLI doesn't expose a
	//     direct results-list subcommand) and verify the case is now
	//     marked passed.
	resp := MakeAuthRequest(t, ts, http.MethodGet, "/workspaces/"+strconv.Itoa(w.Alpha.ID)+"/test-runs/"+strconv.Itoa(runID)+"/results", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var results []map[string]interface{}
	DecodeJSON(t, resp, &results)
	if got := findResultStatus(results, caseID); got != "passed" {
		t.Fatalf("test result %d for case %d: status = %q, want %q", runID, caseID, got, "passed")
	}

	// 12. `ws test run end <id>` marks the run complete.
	_, stderr, code = runWS(t, ts, "test", "run", "end", strconv.Itoa(runID), "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)

	out, stderr, code = runWS(t, ts, "test", "run", "get", strconv.Itoa(runID), "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	if ended := jsonField(t, out, "ended_at"); ended == nil || ended == "" {
		t.Fatalf("test run get after end: ended_at is empty, body=%s", string(out))
	}
}

// --- test-management seed helpers (admin cookie session) ---

func seedTestCase(t *testing.T, ts *TestServer, workspaceID int, title string) int {
	t.Helper()
	body := map[string]interface{}{
		"title":    title,
		"priority": "medium",
	}
	resp := MakeAuthRequest(t, ts, http.MethodPost, "/workspaces/"+strconv.Itoa(workspaceID)+"/test-cases", body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	return ExtractIDFromResponse(t, result)
}

func seedTestSet(t *testing.T, ts *TestServer, workspaceID int, name string) int {
	t.Helper()
	body := map[string]interface{}{"name": name, "description": "WSCLI test seed"}
	resp := MakeAuthRequest(t, ts, http.MethodPost, "/workspaces/"+strconv.Itoa(workspaceID)+"/test-sets", body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	return ExtractIDFromResponse(t, result)
}

func attachCaseToSet(t *testing.T, ts *TestServer, workspaceID, setID, caseID int) {
	t.Helper()
	body := map[string]interface{}{"test_case_id": caseID}
	resp := MakeAuthRequest(t, ts, http.MethodPost,
		"/workspaces/"+strconv.Itoa(workspaceID)+"/test-sets/"+strconv.Itoa(setID)+"/test-cases", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		AssertStatusCode(t, resp, http.StatusOK) // produce a clear failure
	}
}

// --- JSON decoding helpers ---

// idsFromJSONArray reads a CLI JSON-array payload and returns the `id`
// of each element.
func idsFromJSONArray(t *testing.T, out []byte) []int {
	t.Helper()
	var arr []map[string]interface{}
	if err := json.Unmarshal(out, &arr); err != nil {
		t.Fatalf("decode array: %v\nraw=%s", err, string(out))
	}
	ids := make([]int, 0, len(arr))
	for _, el := range arr {
		if id, ok := el["id"].(float64); ok {
			ids = append(ids, int(id))
		}
	}
	return ids
}

// jsonInt reads a single field of a JSON object as an int.
func jsonInt(t *testing.T, out []byte, field string) int {
	t.Helper()
	v := jsonField(t, out, field)
	if f, ok := v.(float64); ok {
		return int(f)
	}
	t.Fatalf("field %q is not numeric: %T (%v); raw=%s", field, v, v, string(out))
	return 0
}

// jsonField reads an arbitrary field from a single-object JSON payload.
func jsonField(t *testing.T, out []byte, field string) interface{} {
	t.Helper()
	var obj map[string]interface{}
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("decode object: %v\nraw=%s", err, string(out))
	}
	return obj[field]
}

// findResultStatus locates the result for a given test case ID and
// returns its status field.
func findResultStatus(results []map[string]interface{}, testCaseID int) string {
	for _, r := range results {
		if id, ok := r["test_case_id"].(float64); ok && int(id) == testCaseID {
			if s, ok := r["status"].(string); ok {
				return s
			}
		}
	}
	return ""
}
