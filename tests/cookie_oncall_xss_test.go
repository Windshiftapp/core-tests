// cookie_oncall_xss_test pins the WI-185 slice-11 sweep on the oncall
// handler family. Schedule + Layer + Override + Policy carry user-
// facing Name + Description / Reason / IANA TZ identifier; the swap
// flow + SetRules + SetLayerMembers carry only IDs / status enums and
// are deliberately not covered here. End-to-end testing exercises
// CreateSchedule + CreatePolicy via the existing /teams admin POST.
package tests

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// createTestTeam seeds a team and returns its id.
func createTestTeam(t *testing.T, ts *TestServer, name string) int {
	t.Helper()
	resp := MakeAuthRequest(t, ts, http.MethodPost, "/teams",
		map[string]interface{}{"name": name})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create team: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	return ExtractIDFromResponse(t, got)
}

func TestCookieAuth_OnCallScheduleXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	teamID := createTestTeam(t, ts, "OnCall XSS Team")

	resp := MakeAuthRequest(t, ts, http.MethodPost,
		fmt.Sprintf("/teams/%d/on-call/schedules", teamID),
		map[string]interface{}{
			"name":        "<script>alert(1)</script>PrimaryRotation",
			"description": "24/7<img src=x onerror=evil()>oncall",
			"timezone":    "Europe/Amsterdam<script>x</script>",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create schedule: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if name, _ := got["name"].(string); strings.Contains(name, "<script") || name != "PrimaryRotation" {
		t.Fatalf("schedule name unsanitized: %q", name)
	}
	if desc, _ := got["description"].(string); strings.Contains(desc, "<img") || strings.Contains(desc, "onerror") {
		t.Fatalf("schedule description unsanitized: %q", desc)
	}
	if tz, _ := got["timezone"].(string); strings.Contains(tz, "<script") || tz != "Europe/Amsterdam" {
		t.Fatalf("schedule timezone unsanitized: %q", tz)
	}
}

func TestCookieAuth_OnCallPolicyXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	teamID := createTestTeam(t, ts, "Policy XSS Team")

	resp := MakeAuthRequest(t, ts, http.MethodPost,
		fmt.Sprintf("/teams/%d/on-call/escalation-policies", teamID),
		map[string]interface{}{
			"name":         "<script>alert(1)</script>SevOne",
			"description":  "Page<img src=x onerror=evil()>everyone",
			"repeat_count": 3,
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create policy: %d %s", resp.StatusCode, string(b))
	}
	// CreatePolicy responds with {id: int} only — sanitize lands at
	// decode and the name passed into the audit log + repository is
	// already scrubbed. Verify via subsequent GET.
	var created map[string]interface{}
	DecodeJSON(t, resp, &created)
	policyID, ok := created["id"].(float64)
	if !ok {
		t.Fatalf("create policy response missing id: %v", created)
	}
	getResp := MakeAuthRequest(t, ts, http.MethodGet,
		fmt.Sprintf("/on-call/escalation-policies/%d", int(policyID)), nil)
	defer getResp.Body.Close()
	AssertStatusCode(t, getResp, http.StatusOK)
	var got map[string]interface{}
	DecodeJSON(t, getResp, &got)
	if name, _ := got["name"].(string); strings.Contains(name, "<script") || name != "SevOne" {
		t.Fatalf("policy name unsanitized: %q", name)
	}
	if desc, _ := got["description"].(string); strings.Contains(desc, "<img") || strings.Contains(desc, "onerror") {
		t.Fatalf("policy description unsanitized: %q", desc)
	}
}
