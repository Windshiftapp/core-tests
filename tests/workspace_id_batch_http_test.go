package tests

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// TestV2WorkspaceIDsResolve covers GET /workspaces?ids=: one bounded read
// resolves a known workspace id set, intersected with the caller's visible
// workspaces, so callers do not issue one request per id.
func TestV2WorkspaceIDsResolve(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	visibleA, _ := CreateTestWorkspace(t, server, "ID batch alpha", shortKey("IBA"))
	visibleB, _ := CreateTestWorkspace(t, server, "ID batch beta", shortKey("IBB"))
	hidden, _ := CreateTestWorkspace(t, server, "ID batch hidden", shortKey("IBH"))

	userID, username, password := CreateTestUserWithCredentials(t, server, "id_batch_reader", "id_batch_reader@test.local")
	AssignWorkspaceRole(t, server, userID, visibleA, "Viewer")
	AssignWorkspaceRole(t, server, userID, visibleB, "Viewer")
	// Restrict the third workspace to another user: assigning a Viewer makes
	// it restricted, and id_batch_reader has no role on it.
	hiddenViewerID, _, _ := CreateTestUserWithCredentials(t, server, "id_batch_hidden_viewer", "id_batch_hidden_viewer@test.local")
	AssignWorkspaceRole(t, server, hiddenViewerID, hidden, "Viewer")
	token := createTokenWithScopesAsUser(t, server, username, password, []string{"workspaces:read"})

	resolve := func(t *testing.T, query string) map[int]bool {
		t.Helper()
		resp := MakeBearerRequestWithToken(t, server, token, http.MethodGet,
			"/rest/api/v2/workspaces?"+query, nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var page struct {
			Data []map[string]any `json:"data"`
		}
		DecodeJSON(t, resp, &page)
		got := map[int]bool{}
		for _, row := range page.Data {
			got[int(row["id"].(float64))] = true
		}
		return got
	}

	t.Run("returnsOnlyTheRequestedVisibleWorkspaces", func(t *testing.T) {
		got := resolve(t, "ids="+strconv.Itoa(visibleA)+","+strconv.Itoa(visibleB)+","+strconv.Itoa(hidden))
		if !got[visibleA] || !got[visibleB] {
			t.Fatalf("visible workspaces missing from resolve: %v", got)
		}
		if got[hidden] {
			t.Fatalf("workspace %d without a role leaked into the resolve: %v", hidden, got)
		}
		if len(got) != 2 {
			t.Fatalf("resolve returned %d workspaces, want 2: %v", len(got), got)
		}
	})

	t.Run("idsIgnoreSearchAndPageSize", func(t *testing.T) {
		got := resolve(t, "ids="+strconv.Itoa(visibleA)+"&search=no-such-workspace&page_size=1")
		if len(got) != 1 || !got[visibleA] {
			t.Fatalf("ids did not take precedence over search/page_size: %v", got)
		}
	})

	t.Run("unknownIdsAreOmittedWithoutFailingTheList", func(t *testing.T) {
		got := resolve(t, "ids="+strconv.Itoa(visibleA)+",999999")
		if len(got) != 1 || !got[visibleA] {
			t.Fatalf("an unknown id changed the result: %v", got)
		}
	})

	t.Run("moreThanTheCapIsRejected", func(t *testing.T) {
		ids := make([]string, 0, 201)
		for i := 1; i <= 201; i++ {
			ids = append(ids, strconv.Itoa(i))
		}
		resp := MakeBearerRequestWithToken(t, server, token, http.MethodGet,
			"/rest/api/v2/workspaces?ids="+strings.Join(ids, ","), nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("201 ids = %d, want 400: %s", resp.StatusCode, string(body))
		}
	})

	t.Run("invalidIdIsRejected", func(t *testing.T) {
		resp := MakeBearerRequestWithToken(t, server, token, http.MethodGet,
			"/rest/api/v2/workspaces?ids=not-an-id", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("invalid id = %d, want 400: %s", resp.StatusCode, string(body))
		}
	})
}
