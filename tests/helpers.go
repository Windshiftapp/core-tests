// Package tests provides integration test helpers and utilities.
package tests

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	mathrand "math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"windshift/internal/auth"
	"windshift/internal/config"
	"windshift/internal/database"
	"windshift/internal/server"
)

// testHTTPClient is a shared HTTP client with timeout for all test requests
var testHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

func waitForCondition(t *testing.T, timeout time.Duration, description string, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if condition() {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", description)
		}
	}
}

// testSessionSecret is the cookie-secret the in-process server runs with.
// Helpers that need to mint cookie-encoded session values for tests (e.g.
// portal customer sessions, inserted into the DB without going through magic
// link auth) construct a sibling session manager from it.
const testSessionSecret = "test-session-secret-for-integration-tests"

// TestServer represents a running test server instance.
//
// Two credentials are tracked because the two API surfaces accept different
// auth methods:
//   - SessionCookie authenticates against the cookie-auth surface (/api/...).
//     The stored value is the full Cookie-header string ("name=value; flags")
//     produced by *http.Cookie.String(); it is replayed verbatim in the
//     Cookie request header by MakeAuthRequest* helpers. Cookie values are
//     secure-cookie encoded so they cannot be used as X-Session-Token.
//   - BearerToken authenticates against the v1 surface (/rest/api/v1/...) via
//     the Authorization: Bearer header. Use it through MakeBearerRequest*
//     helpers, or to test bearer-token-specific behavior.
//
// CreateBearerToken populates both during setup so individual tests don't
// have to think about the split.
type TestServer struct {
	Port          int
	BaseURL       string
	APIBase       string
	DBPath        string
	DBType        string
	BearerToken   string
	SessionCookie string
	server        *server.Server // in-process server reference
}

// GetDBType returns the database type to use for integration tests.
// It reads from TEST_DB_TYPE env var, defaulting to "sqlite".
func GetDBType() string {
	if dt := os.Getenv("TEST_DB_TYPE"); dt != "" {
		return dt
	}
	return "sqlite"
}

// replacePgDBName rewrites a Postgres DSN to point at a different database.
// Supports URL-style (postgres://user:pass@host:port/dbname?params) and
// key=value (host=... dbname=...) forms.
func replacePgDBName(dsn, newDB string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		scheme := dsn
		params := ""
		if i := strings.IndexByte(dsn, '?'); i >= 0 {
			scheme, params = dsn[:i], dsn[i:]
		}
		authEnd := strings.Index(scheme, "://") + 3
		if slash := strings.LastIndexByte(scheme, '/'); slash >= authEnd {
			return scheme[:slash+1] + newDB + params
		}
		return scheme + "/" + newDB + params
	}
	parts := strings.Fields(dsn)
	found := false
	for i, p := range parts {
		if strings.HasPrefix(p, "dbname=") {
			parts[i] = "dbname=" + newDB
			found = true
		}
	}
	if !found {
		parts = append(parts, "dbname="+newDB)
	}
	return strings.Join(parts, " ")
}

// extractPgDBName returns the database name from a Postgres DSN (URL or key=value).
func extractPgDBName(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		path := dsn
		if i := strings.IndexByte(dsn, '?'); i >= 0 {
			path = dsn[:i]
		}
		authEnd := strings.Index(path, "://") + 3
		if slash := strings.LastIndexByte(path, '/'); slash >= authEnd {
			return path[slash+1:]
		}
		return ""
	}
	for _, p := range strings.Fields(dsn) {
		if strings.HasPrefix(p, "dbname=") {
			return strings.TrimPrefix(p, "dbname=")
		}
	}
	return ""
}

// StartTestServer starts a windshift server with an isolated database
// and returns a TestServer instance with cleanup function.
// This uses an in-process server for faster, more reliable tests.
func StartTestServer(t *testing.T, dbType string) (ts *TestServer, cleanup func()) {
	t.Helper()

	// Generate unique database name
	timestamp := time.Now().UnixNano()
	pid := os.Getpid()

	var dbPath string
	var pgBaseDSN string // used for postgres cleanup

	switch dbType {
	case "sqlite":
		// Use temp directory to avoid polluting project root
		tempDir := filepath.Join(os.TempDir(), "windshift-tests")
		if err := os.MkdirAll(tempDir, 0o750); err != nil {
			t.Fatalf("Failed to create test temp dir: %v", err)
		}
		dbPath = filepath.Join(tempDir, fmt.Sprintf("test_%d_%d.db", timestamp, pid))
	case "postgres":
		pgBaseDSN = os.Getenv("TEST_POSTGRES_DSN")
		if pgBaseDSN == "" {
			pgBaseDSN = "postgresql://windshift_test:windshift_test_password@localhost:15432/postgres?sslmode=disable" //nolint:gosec // G101: test-only fallback DSN for local Postgres
		}

		// Create a unique test database for isolation
		dbName := fmt.Sprintf("windshift_test_%d_%d", timestamp, pid)

		// Connect to default "postgres" DB to create the test database
		adminDB, err := sql.Open("postgres", pgBaseDSN)
		if err != nil {
			t.Fatalf("Failed to connect to PostgreSQL: %v", err)
		}
		defer adminDB.Close()

		_, err = adminDB.Exec("CREATE DATABASE " + dbName)
		if err != nil {
			t.Fatalf("Failed to create test database %s: %v", dbName, err)
		}

		// Build connection string pointing to the new test database. Handles
		// both URL-style (postgres://...) and key=value (host=... dbname=...)
		// DSN forms; CI uses the key=value form.
		dbPath = replacePgDBName(pgBaseDSN, dbName)
	default:
		t.Fatalf("Unknown database type: %s", dbType)
	}

	// Set required environment variables for testing (kept for any code path
	// that still reads env vars directly rather than through config.Load).
	_ = os.Setenv("SESSION_SECRET", "test-session-secret-for-integration-tests")

	// Create server configuration for testing
	cfg := server.Config{
		Port:           "0",                             // Use port 0 for OS-assigned free port
		DisableCSRF:    true,                            // Disable CSRF for testing
		SilentMode:     os.Getenv("TEST_VERBOSE") == "", // Suppress logs unless TEST_VERBOSE is set
		MCPEnabled:     true,                            // Mount /mcp so MCP-tool tests can hit it
		AttachmentPath: t.TempDir(),                     // Keep the cookie-auth attachment routes enabled for HTTP tests
		DB: config.DBConfig{
			MaxReadConns:  10,
			MaxWriteConns: 1,
		},
		Auth: config.AuthConfig{
			SessionSecret: testSessionSecret,
		},
	}

	switch dbType {
	case "sqlite":
		cfg.DB.SQLitePath = dbPath
	case "postgres":
		cfg.DB.PostgresConn = dbPath
	}

	// Create the in-process server
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Start the server
	if err := srv.Start(); err != nil {
		_ = srv.Shutdown(context.Background())
		t.Fatalf("Failed to start test server: %v", err)
	}

	port := srv.Port()
	baseURL := fmt.Sprintf("http://localhost:%d", port)
	apiBase := baseURL + "/api"

	ts = &TestServer{
		Port:    port,
		BaseURL: baseURL,
		APIBase: apiBase,
		DBPath:  dbPath,
		DBType:  dbType,
		server:  srv,
	}

	// Cleanup function with graceful shutdown
	cleanup = func() {
		// Ensure we always clean up database, even if server cleanup fails
		defer func() {
			switch dbType {
			case "sqlite":
				if dbPath != "" {
					// Remove all SQLite database files
					if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
						t.Logf("Warning: Failed to remove database file %s: %v", dbPath, err)
					}
					// Also remove WAL files (ignore errors if they don't exist)
					_ = os.Remove(dbPath + "-shm")
					_ = os.Remove(dbPath + "-wal")
					_ = os.Remove(dbPath + "-journal")
				}
			case "postgres":
				if pgBaseDSN != "" {
					dbName := extractPgDBName(dbPath)
					adminDB, err := sql.Open("postgres", pgBaseDSN)
					if err != nil {
						t.Logf("Warning: Failed to connect for cleanup: %v", err)
						return
					}
					defer adminDB.Close()
					// Terminate existing connections before dropping
					_, _ = adminDB.Exec(fmt.Sprintf("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()", dbName))
					if _, err := adminDB.Exec("DROP DATABASE IF EXISTS " + dbName); err != nil {
						t.Logf("Warning: Failed to drop test database %s: %v", dbName, err)
					}
				}
			}
		}()

		// Graceful shutdown with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			t.Logf("Warning: Server shutdown error: %v", err)
		}
	}

	// Register cleanup with testing framework
	t.Cleanup(cleanup)

	return ts, cleanup
}

// CreateBearerToken completes the full authentication flow and populates both
// auth credentials on testServer:
//   - testServer.SessionCookie: for /api/* (cookie-auth) — use MakeAuthRequest*.
//   - testServer.BearerToken:   for /rest/api/v1/* (v1) — use MakeBearerRequest*.
//
// Returns testServer.SessionCookie so callers passing the result to
// MakeAuthRequestWithToken (which targets /api/*) get the right credential.
// Tests that need the bearer token can read testServer.BearerToken directly
// or use MakeBearerRequest helpers.
//
// Tests run with DisableCSRF: true, so no CSRF headers are needed.
func CreateBearerToken(t *testing.T, testServer *TestServer) string {
	t.Helper()

	// Step 1: Complete initial setup
	setupData := map[string]interface{}{
		"admin_user": map[string]interface{}{
			"email":      "admin@test.com",
			"username":   "admin",
			"password":   "testpass123", // Plaintext; hashed server-side
			"first_name": "Test",
			"last_name":  "Admin",
		},
		"module_settings": map[string]interface{}{
			"time_tracking_enabled":   true,
			"test_management_enabled": true,
		},
	}

	setupResp := makeRequest(t, http.MethodPost, testServer.APIBase+"/setup/complete", "", setupData, nil)

	if setupResp.StatusCode != http.StatusOK && setupResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(setupResp.Body)
		t.Fatalf("Setup failed: %d - %s", setupResp.StatusCode, string(body))
	}
	setupResp.Body.Close()

	// Step 2: Login to get session cookie
	loginData := map[string]string{
		"email_or_username": "admin",
		"password":          "testpass123",
	}

	loginResp := makeRequest(t, http.MethodPost, testServer.APIBase+"/auth/login", "", loginData, nil)

	if loginResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("Login failed: %d - %s", loginResp.StatusCode, string(body))
	}

	// Extract the full session cookie ("name=value; flags") for replay.
	cookies := loginResp.Cookies()
	var sessionCookie string
	for _, cookie := range cookies {
		if cookie.Name == "session" || cookie.Name == "windshift_session" {
			sessionCookie = cookie.String()
			break
		}
	}

	if sessionCookie == "" {
		t.Fatal("No session cookie received from login")
	}
	loginResp.Body.Close()

	// Step 3: Create API bearer token (used for /rest/api/v1/* requests).
	// The full granular scope set: the legacy "admin" string is no longer a
	// valid mint input (WI-959), so spell out everything the admin flow needs.
	tokenData := map[string]interface{}{
		"name":        "Test API Token",
		"permissions": append(auth.NonAdminScopes(), auth.AdminScopes()...),
	}

	tokenResp := makeRequest(t, http.MethodPost, testServer.APIBase+"/api-tokens", "", tokenData, map[string]string{
		"Cookie": sessionCookie,
	})

	if tokenResp.StatusCode != http.StatusOK && tokenResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(tokenResp.Body)
		t.Fatalf("Token creation failed: %d - %s", tokenResp.StatusCode, string(body))
	}

	var tokenResult struct {
		Token string `json:"token"`
	}

	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenResult); err != nil {
		t.Fatalf("Failed to decode token response: %v", err)
	}
	tokenResp.Body.Close()

	if tokenResult.Token == "" {
		t.Fatal("Empty bearer token received")
	}

	testServer.BearerToken = tokenResult.Token
	testServer.SessionCookie = sessionCookie
	return testServer.SessionCookie
}

// makeRequest is a low-level helper for making HTTP requests with optional
// bearer auth. The bearerToken arg is sent verbatim as Authorization: Bearer
// when non-empty. Use this directly only for setup flows (login, setup) and
// for tests that explicitly exercise the bearer-token contract; everyday
// authenticated requests should go through MakeAuthRequest (session) or
// MakeBearerRequest (v1 bearer).
func makeRequest(t *testing.T, method, url, bearerToken string, body interface{}, headers map[string]string) *http.Response {
	t.Helper()

	var bodyReader io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("Failed to marshal request body: %v", err)
		}
		bodyReader = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := testHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	return resp
}

// makeSessionRequest is the cookie-authenticated counterpart of makeRequest.
// Used for /api/* (cookie-auth) traffic, which no longer accepts bearer
// tokens. The sessionCookie value is the full Cookie-header string emitted by
// http.Cookie.String() during login.
func makeSessionRequest(t *testing.T, method, url, sessionCookie string, body interface{}, headers map[string]string) *http.Response {
	t.Helper()

	var bodyReader io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("Failed to marshal request body: %v", err)
		}
		bodyReader = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if sessionCookie != "" {
		req.Header.Set("Cookie", sessionCookie)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := testHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	return resp
}

// MakeAuthRequest makes a session-authenticated request to /api/<endpoint>.
// Cookie-auth (the /api/* surface) only accepts session-based auth; bearer
// tokens belong on /rest/api/v1/* — use MakeBearerRequest there.
func MakeAuthRequest(t *testing.T, testServer *TestServer, method, endpoint string, body interface{}) *http.Response {
	t.Helper()

	url := testServer.APIBase + endpoint
	return makeSessionRequest(t, method, url, testServer.SessionCookie, body, nil)
}

// MakeAuthRequestRaw is the raw-body variant of MakeAuthRequest. Used by tests
// that need to send malformed JSON (the marshalled-body path of MakeAuthRequest
// would refuse).
func MakeAuthRequestRaw(t *testing.T, testServer *TestServer, method, endpoint, rawBody string) *http.Response {
	t.Helper()

	url := testServer.APIBase + endpoint

	req, err := http.NewRequest(method, url, strings.NewReader(rawBody))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if testServer.SessionCookie != "" {
		req.Header.Set("Cookie", testServer.SessionCookie)
	}

	resp, err := testHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	return resp
}

// MakeBearerRequest makes a bearer-token-authenticated request to a path.
// Used for /rest/api/v1/* traffic (the v1 surface accepts bearer only) and
// for tests that explicitly verify bearer-token behavior.
//
// Unlike MakeAuthRequest, the path is taken verbatim — pass the full path
// including any "/rest/api/v1" or "/api" prefix.
func MakeBearerRequest(t *testing.T, testServer *TestServer, method, path string, body interface{}) *http.Response {
	t.Helper()

	url := testServer.BaseURL + path
	return makeRequest(t, method, url, testServer.BearerToken, body, nil)
}

// AssertStatusCode checks that the response has the expected status code
func AssertStatusCode(t *testing.T, resp *http.Response, expected int) {
	t.Helper()

	if resp.StatusCode != expected {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("Expected status %d, got %d. Body: %s", expected, resp.StatusCode, string(body))
	}
}

// AssertRejected asserts that the response is a permission/authentication
// rejection. Item and workspace handlers return 404 ("Item not found",
// "Workspace not found") for permission failures by design — to avoid
// leaking the existence of resources the caller cannot see — so a generic
// "rejected" assertion is more correct than coupling to a specific 4xx
// code. 401, 403, and 404 are all accepted; 200/201/204 cause a test
// failure (the action shouldn't have succeeded).
func AssertRejected(t *testing.T, resp *http.Response) {
	t.Helper()

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return
	}
	body, _ := io.ReadAll(resp.Body)
	t.Errorf("Expected rejection (401/403/404), got %d. Body: %s", resp.StatusCode, string(body))
}

// DecodeJSON decodes a JSON response into the provided interface
func DecodeJSON(t *testing.T, resp *http.Response, v interface{}) {
	t.Helper()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(bodyBytes, v); err != nil {
		t.Fatalf("Failed to decode JSON response: %v\nResponse body: %s", err, string(bodyBytes))
	}
}

// AssertJSONField checks that a JSON response contains a field with expected value
func AssertJSONField(t *testing.T, data map[string]interface{}, field string, expected interface{}) {
	t.Helper()

	actual, ok := data[field]
	if !ok {
		t.Errorf("Field %s not found in response", field)
		return
	}

	if actual != expected {
		t.Errorf("Field %s: expected %v, got %v", field, expected, actual)
	}
}

// ExtractIDFromResponse safely extracts an ID from a JSON response
func ExtractIDFromResponse(t *testing.T, result map[string]interface{}) int {
	t.Helper()

	if id, ok := result["id"].(float64); ok {
		return int(id)
	}
	t.Fatal("ID not found in response")
	return 0
}

// CreateTestWorkspace creates a test workspace and returns its ID and key
func CreateTestWorkspace(t *testing.T, testServer *TestServer, name, key string) (workspaceID int, workspaceKey string) {
	t.Helper()

	// Generate short key if not already present
	if key == "" {
		key = shortKey("TEST")
	}

	// Stays on /api/workspaces (cookie-auth) for now — the /api handler runs
	// extra setup (default config-set association, default statuses, etc.)
	// that the v1 POST /rest/api/v1/workspaces does not. Migrating this helper
	// to v1 would require either (a) adding the same setup to v1's create
	// path or (b) doing the auxiliary setup explicitly in the test helper.
	// Both are deferred — the test suite is fully functional via cookie-auth
	// and v1 has dedicated coverage in api_token_scope_test.go.
	workspaceData := map[string]interface{}{
		"name":        name,
		"key":         key,
		"description": fmt.Sprintf("Test workspace: %s", name),
		"active":      true,
	}

	resp := MakeAuthRequest(t, testServer, http.MethodPost, "/workspaces", workspaceData)
	defer resp.Body.Close()

	AssertStatusCode(t, resp, http.StatusCreated)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)

	workspaceID = ExtractIDFromResponse(t, result)
	workspaceKey, _ = result["key"].(string)

	return workspaceID, workspaceKey
}

// CreateTestCustomField creates a custom field and returns its ID
func CreateTestCustomField(t *testing.T, testServer *TestServer, name, fieldType, options string) int {
	t.Helper()

	fieldData := map[string]interface{}{
		"name":        name,
		"field_type":  fieldType,
		"description": fmt.Sprintf("Test field: %s", name),
		"required":    false,
	}

	if options != "" {
		fieldData["options"] = options
	}

	resp := MakeAuthRequest(t, testServer, http.MethodPost, "/admin/custom-fields", fieldData)
	defer resp.Body.Close()

	AssertStatusCode(t, resp, http.StatusCreated)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)

	return ExtractIDFromResponse(t, result)
}

// CreateTestStatusCategories creates 3 standard status categories and returns their IDs
func CreateTestStatusCategories(t *testing.T, testServer *TestServer, prefix string) []int {
	t.Helper()

	timestamp := time.Now().Unix()
	categories := []map[string]interface{}{
		{
			"name":         fmt.Sprintf("%s To Do %d", prefix, timestamp),
			"color":        "#6b7280",
			"description":  "Pending items",
			"is_default":   false,
			"is_completed": false,
		},
		{
			"name":         fmt.Sprintf("%s In Progress %d", prefix, timestamp),
			"color":        "#3b82f6",
			"description":  "Active items",
			"is_default":   false,
			"is_completed": false,
		},
		{
			"name":         fmt.Sprintf("%s Done %d", prefix, timestamp),
			"color":        "#10b981",
			"description":  "Completed items",
			"is_default":   false,
			"is_completed": true,
		},
	}

	categoryIDs := make([]int, 0, len(categories))
	for _, catData := range categories {
		resp := MakeAuthRequest(t, testServer, http.MethodPost, "/status-categories", catData)

		AssertStatusCode(t, resp, http.StatusCreated)

		var result map[string]interface{}
		DecodeJSON(t, resp, &result)

		categoryIDs = append(categoryIDs, ExtractIDFromResponse(t, result))
		resp.Body.Close()
	}

	return categoryIDs
}

// CreateTestStatuses creates 6 standard statuses across 3 categories and returns their IDs
func CreateTestStatuses(t *testing.T, testServer *TestServer, prefix string, categoryIDs []int) []int {
	t.Helper()

	if len(categoryIDs) != 3 {
		t.Fatalf("CreateTestStatuses requires exactly 3 category IDs, got %d", len(categoryIDs))
	}

	timestamp := time.Now().Unix()
	statuses := []map[string]interface{}{
		{
			"name":        fmt.Sprintf("%s Open %d", prefix, timestamp),
			"description": "New items",
			"category_id": categoryIDs[0],
			"is_default":  false,
		},
		{
			"name":        fmt.Sprintf("%s To Do %d", prefix, timestamp),
			"description": "Ready to start",
			"category_id": categoryIDs[0],
			"is_default":  false,
		},
		{
			"name":        fmt.Sprintf("%s In Progress %d", prefix, timestamp),
			"description": "Being worked on",
			"category_id": categoryIDs[1],
			"is_default":  false,
		},
		{
			"name":        fmt.Sprintf("%s In Review %d", prefix, timestamp),
			"description": "Under review",
			"category_id": categoryIDs[1],
			"is_default":  false,
		},
		{
			"name":        fmt.Sprintf("%s Completed %d", prefix, timestamp),
			"description": "Finished",
			"category_id": categoryIDs[2],
			"is_default":  false,
		},
		{
			"name":        fmt.Sprintf("%s Canceled %d", prefix, timestamp),
			"description": "Canceled",
			"category_id": categoryIDs[2],
			"is_default":  false,
		},
	}

	statusIDs := make([]int, 0, len(statuses))
	for _, statusData := range statuses {
		resp := MakeAuthRequest(t, testServer, http.MethodPost, "/statuses", statusData)

		AssertStatusCode(t, resp, http.StatusCreated)

		var result map[string]interface{}
		DecodeJSON(t, resp, &result)

		statusIDs = append(statusIDs, ExtractIDFromResponse(t, result))
		resp.Body.Close()
	}

	return statusIDs
}

// BindStatusesToWorkspace makes the given statuses legal create-time targets
// in the workspace. Create-time status overrides are validated against the
// workflow (ValidateCreateStatusOverride): the status must be the workflow
// initial status or one ungated hop from it, so statuses created via
// CreateTestStatuses — which belong to no workflow — are rejected with 400.
// This builds a workflow with statusIDs[0] as the initial status and a direct
// transition to every other status, whitelists all item types (a config set
// with no item_type_configs rejects every typed create), and binds the
// workspace through a dedicated configuration set.
func BindStatusesToWorkspace(t *testing.T, testServer *TestServer, workspaceID int, name string, statusIDs []int) {
	t.Helper()
	if len(statusIDs) == 0 {
		t.Fatal("BindStatusesToWorkspace requires at least one status")
	}

	wfResp := MakeAuthRequest(t, testServer, http.MethodPost, "/workflows", map[string]interface{}{
		"name":        name,
		"description": "integration-test workflow (BindStatusesToWorkspace)",
	})
	AssertStatusCode(t, wfResp, http.StatusCreated)
	var wf map[string]interface{}
	DecodeJSON(t, wfResp, &wf)
	wfResp.Body.Close()
	workflowID := ExtractIDFromResponse(t, wf)

	transitions := []map[string]interface{}{
		{"from_status_id": nil, "to_status_id": statusIDs[0]},
	}
	for _, sid := range statusIDs[1:] {
		transitions = append(transitions, map[string]interface{}{
			"from_status_id": statusIDs[0],
			"to_status_id":   sid,
		})
	}
	txResp := MakeAuthRequest(t, testServer, http.MethodPut,
		fmt.Sprintf("/workflows/%d/transitions", workflowID), transitions)
	AssertStatusCode(t, txResp, http.StatusOK)
	txResp.Body.Close()

	itResp := MakeAuthRequest(t, testServer, http.MethodGet, "/item-types", nil)
	AssertStatusCode(t, itResp, http.StatusOK)
	var itemTypes []map[string]interface{}
	DecodeJSON(t, itResp, &itemTypes)
	itResp.Body.Close()
	itemTypeConfigs := make([]map[string]interface{}, 0, len(itemTypes))
	for _, it := range itemTypes {
		itemTypeConfigs = append(itemTypeConfigs, map[string]interface{}{
			"item_type_id": ExtractIDFromResponse(t, it),
		})
	}

	csResp := MakeAuthRequest(t, testServer, http.MethodPost, "/configuration-sets", map[string]interface{}{
		"name":              name,
		"description":       "integration-test config set (BindStatusesToWorkspace)",
		"workflow_id":       workflowID,
		"workspace_ids":     []int{workspaceID},
		"item_type_configs": itemTypeConfigs,
	})
	AssertStatusCode(t, csResp, http.StatusCreated)
	csResp.Body.Close()
}

// GetDefaultConfigurationSet retrieves the default configuration set ID
func GetDefaultConfigurationSet(t *testing.T, testServer *TestServer) int {
	t.Helper()

	resp := MakeAuthRequest(t, testServer, http.MethodGet, "/configuration-sets", nil)
	defer resp.Body.Close()

	AssertStatusCode(t, resp, http.StatusOK)

	// Handle paginated response format: {"configuration_sets": [...], "pagination": {...}}
	var result struct {
		ConfigurationSets []map[string]interface{} `json:"configuration_sets"`
	}
	DecodeJSON(t, resp, &result)

	configSets := result.ConfigurationSets

	// Find the default configuration set
	for _, cs := range configSets {
		if isDefault, ok := cs["is_default"].(bool); ok && isDefault {
			return ExtractIDFromResponse(t, cs)
		}
	}

	// If no default found, use the first one
	if len(configSets) > 0 {
		return ExtractIDFromResponse(t, configSets[0])
	}

	t.Fatal("No configuration set found")
	return 0
}

// GetItemTypes retrieves all item types for a configuration set as a map of name->ID
func GetItemTypes(t *testing.T, testServer *TestServer, configSetID int) map[string]int {
	t.Helper()

	// First try with config set filter
	endpoint := fmt.Sprintf("/item-types?configuration_set_id=%d", configSetID)
	resp := MakeAuthRequest(t, testServer, http.MethodGet, endpoint, nil)
	defer resp.Body.Close()

	AssertStatusCode(t, resp, http.StatusOK)

	bodyBytes, _ := io.ReadAll(resp.Body)

	var itemTypes []map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &itemTypes); err != nil {
		t.Fatalf("Failed to decode item types: %v\nResponse: %s", err, string(bodyBytes))
	}

	// If no item types found for config set, fall back to all item types
	// This handles the case where item types aren't yet associated with configuration sets
	if len(itemTypes) == 0 {
		allResp := MakeAuthRequest(t, testServer, http.MethodGet, "/item-types", nil)
		allBodyBytes, _ := io.ReadAll(allResp.Body)
		allResp.Body.Close()
		if err := json.Unmarshal(allBodyBytes, &itemTypes); err != nil {
			t.Fatalf("Failed to decode all item types: %v\nResponse: %s", err, string(allBodyBytes))
		}
	}

	itemTypeMap := make(map[string]int)
	for _, it := range itemTypes {
		if name, ok := it["name"].(string); ok {
			if id, ok := it["id"].(float64); ok {
				itemTypeMap[name] = int(id)
			}
		}
	}

	return itemTypeMap
}

// RequireItemTypeID returns the ID of a named item type or fails the test.
// Callers creating parentless items must select a regular type explicitly:
// choosing the first entry of a map is nondeterministic and can select the
// generic Sub-task type, which requires a parent.
func RequireItemTypeID(t *testing.T, itemTypes map[string]int, name string) int {
	t.Helper()
	itemTypeID := itemTypes[name]
	if itemTypeID == 0 {
		t.Fatalf("%s item type not found", name)
	}
	return itemTypeID
}

// ============================================================================
// Key Generation Helpers
// ============================================================================

// shortKey generates a short workspace key (max 10 chars) with a prefix and random suffix.
func shortKey(prefix string) string {
	// Ensure we have room for at least 4 random digits
	maxPrefixLen := 6
	if len(prefix) > maxPrefixLen {
		prefix = prefix[:maxPrefixLen]
	}
	return fmt.Sprintf("%s%d", prefix, mathrand.Intn(10000)) //nolint:gosec // G404: test helper, crypto random not needed
}

// ============================================================================
// Permission Testing Helpers
// ============================================================================

// CreateTestUserWithCredentials creates a user via the API and returns userID, username, and password.
// Requires admin token to be set on the server.
func CreateTestUserWithCredentials(t *testing.T, testServer *TestServer, username, email string) (userID int, uname, password string) {
	t.Helper()

	password = "testpass123"

	// is_active in the body is intentionally ignored by the user-create handler
	// (internal/handlers/users.go:282 always inserts is_active=false). We must
	// follow up with the explicit activate endpoint before the user can log in.
	userData := map[string]interface{}{
		"email":      email,
		"username":   username,
		"first_name": "Test",
		"last_name":  "User",
		"password":   password,
	}

	resp := MakeAuthRequest(t, testServer, http.MethodPost, "/users", userData)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to create user %s: %d - %s", username, resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)

	userID = ExtractIDFromResponse(t, result)

	activateResp := MakeAuthRequest(t, testServer, http.MethodPost, fmt.Sprintf("/users/%d/activate", userID), nil)
	defer activateResp.Body.Close()
	if activateResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(activateResp.Body)
		t.Fatalf("Failed to activate user %s (id=%d): %d - %s", username, userID, activateResp.StatusCode, string(body))
	}

	return userID, username, password
}

// CreateBearerTokenForUser logs in as the specified user and returns the full
// session cookie string ("name=value; flags") usable with
// MakeAuthRequestWithToken (which targets /api/*). The flow also mints an API
// bearer token for the user — discarded here because the existing call sites
// use the result against the cookie-auth surface; if a future test needs a
// per-user bearer token, add a CreateUserCredentials helper that returns both.
// Tests run with DisableCSRF: true.
func CreateBearerTokenForUser(t *testing.T, testServer *TestServer, username, password string) string {
	t.Helper()
	sessionCookie, _ := CreateAuthCredentialsForUser(t, testServer, username, password)
	return sessionCookie
}

// CreateAuthCredentialsForUser logs in as a user and returns credentials for
// both the cookie and bearer API surfaces.
func CreateAuthCredentialsForUser(t *testing.T, testServer *TestServer, username, password string) (string, string) {
	t.Helper()

	// Login to get session cookie
	loginData := map[string]string{
		"email_or_username": username,
		"password":          password,
	}

	loginResp := makeRequest(t, http.MethodPost, testServer.APIBase+"/auth/login", "", loginData, nil)

	if loginResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("Login failed for user %s: %d - %s", username, loginResp.StatusCode, string(body))
	}

	// Extract full session cookie for replay.
	cookies := loginResp.Cookies()
	var sessionCookie string
	for _, cookie := range cookies {
		if cookie.Name == "session" || cookie.Name == "windshift_session" {
			sessionCookie = cookie.String()
			break
		}
	}

	if sessionCookie == "" {
		t.Fatalf("No session cookie received for user %s", username)
	}
	loginResp.Body.Close()

	// Mint a bearer token under this user. Non-admin users cannot request admin
	// scopes at creation time.
	tokenData := map[string]interface{}{
		"name":        fmt.Sprintf("Test Token for %s", username),
		"permissions": auth.NonAdminScopes(),
	}

	tokenResp := makeRequest(t, http.MethodPost, testServer.APIBase+"/api-tokens", "", tokenData, map[string]string{
		"Cookie": sessionCookie,
	})

	if tokenResp.StatusCode != http.StatusOK && tokenResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(tokenResp.Body)
		t.Fatalf("Token creation failed for user %s: %d - %s", username, tokenResp.StatusCode, string(body))
	}
	var tokenResult struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenResult); err != nil {
		t.Fatalf("Failed to decode token response for user %s: %v", username, err)
	}
	tokenResp.Body.Close()
	if tokenResult.Token == "" {
		t.Fatalf("Empty bearer token received for user %s", username)
	}

	return sessionCookie, tokenResult.Token
}

// MakeAuthRequestWithToken makes a session-authenticated request to /api/*
// using a caller-supplied session cookie. Pair with CreateBearerTokenForUser,
// which returns a full Cookie-header string for the named user.
//
// (Name kept for callsite stability across ~30 tests; "Token" here is a
// session cookie, not a bearer.)
func MakeAuthRequestWithToken(t *testing.T, testServer *TestServer, token, method, endpoint string, body interface{}) *http.Response {
	t.Helper()

	url := testServer.APIBase + endpoint
	return makeSessionRequest(t, method, url, token, body, nil)
}

// MakeBearerRequestWithToken is the bearer counterpart of
// MakeAuthRequestWithToken — caller-supplied bearer token, full path.
func MakeBearerRequestWithToken(t *testing.T, testServer *TestServer, token, method, path string, body interface{}) *http.Response {
	t.Helper()

	url := testServer.BaseURL + path
	return makeRequest(t, method, url, token, body, nil)
}

// GetWorkspaceRoles retrieves all workspace roles and returns a map of name -> ID.
func GetWorkspaceRoles(t *testing.T, testServer *TestServer) map[string]int {
	t.Helper()

	resp := MakeAuthRequest(t, testServer, http.MethodGet, "/workspace-roles", nil)
	defer resp.Body.Close()

	AssertStatusCode(t, resp, http.StatusOK)

	var roles []map[string]interface{}
	DecodeJSON(t, resp, &roles)

	roleMap := make(map[string]int)
	for _, role := range roles {
		if name, ok := role["name"].(string); ok {
			if id, ok := role["id"].(float64); ok {
				roleMap[name] = int(id)
			}
		}
	}

	return roleMap
}

// GetPermissions retrieves all permissions and returns a map of permission_key -> ID.
// Note: This requires system admin permissions.
func GetPermissions(t *testing.T, testServer *TestServer) map[string]int {
	t.Helper()

	resp := MakeAuthRequest(t, testServer, http.MethodGet, "/permissions", nil)
	defer resp.Body.Close()

	AssertStatusCode(t, resp, http.StatusOK)

	var permissions []map[string]interface{}
	DecodeJSON(t, resp, &permissions)

	permMap := make(map[string]int)
	for _, perm := range permissions {
		if key, ok := perm["permission_key"].(string); ok {
			if id, ok := perm["id"].(float64); ok {
				permMap[key] = int(id)
			}
		}
	}

	return permMap
}

// AssignWorkspaceRole assigns a role to a user in a workspace.
// roleName should be "Viewer", "Editor", or "Administrator".
func AssignWorkspaceRole(t *testing.T, testServer *TestServer, userID, workspaceID int, roleName string) {
	t.Helper()

	// Get role ID from name
	roles := GetWorkspaceRoles(t, testServer)
	roleID, ok := roles[roleName]
	if !ok {
		t.Fatalf("Role %s not found. Available roles: %v", roleName, roles)
	}

	assignData := map[string]interface{}{
		"user_id":      userID,
		"workspace_id": workspaceID,
		"role_id":      roleID,
	}

	resp := MakeAuthRequest(t, testServer, http.MethodPost, "/workspace-roles/assign", assignData)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to assign role %s to user %d in workspace %d: %d - %s",
			roleName, userID, workspaceID, resp.StatusCode, string(body))
	}
}

// RevokeWorkspaceRole removes a user's role assignment in a workspace.
func RevokeWorkspaceRole(t *testing.T, testServer *TestServer, userID, workspaceID, roleID int) {
	t.Helper()

	endpoint := fmt.Sprintf("/users/%d/workspaces/%d/roles/%d", userID, workspaceID, roleID)
	resp := MakeAuthRequest(t, testServer, http.MethodDelete, endpoint, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to revoke role %d from user %d in workspace %d: %d - %s",
			roleID, userID, workspaceID, resp.StatusCode, string(body))
	}
}

// GrantGlobalPermission grants a global permission to a user.
func GrantGlobalPermission(t *testing.T, testServer *TestServer, userID int, permissionKey string) {
	t.Helper()

	// Get permission ID from key
	permissions := GetPermissions(t, testServer)
	permissionID, ok := permissions[permissionKey]
	if !ok {
		t.Fatalf("Permission %s not found. Available permissions: %v", permissionKey, permissions)
	}

	grantData := map[string]interface{}{
		"user_id":       userID,
		"permission_id": permissionID,
	}

	resp := MakeAuthRequest(t, testServer, http.MethodPost, "/permissions/global/grant", grantData)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to grant permission %s to user %d: %d - %s",
			permissionKey, userID, resp.StatusCode, string(body))
	}
}

// RevokeGlobalPermission removes a global permission from a user.
func RevokeGlobalPermission(t *testing.T, testServer *TestServer, userID, permissionID int) {
	t.Helper()

	endpoint := fmt.Sprintf("/users/%d/permissions/global/%d", userID, permissionID)
	resp := MakeAuthRequest(t, testServer, http.MethodDelete, endpoint, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to revoke permission %d from user %d: %d - %s",
			permissionID, userID, resp.StatusCode, string(body))
	}
}

// LockDownWorkspace restricts a workspace so that only explicitly assigned
// users have access. It does this by assigning the Viewer role to the admin
// user (the bearer-token holder), which triggers the "has explicit Viewer
// assignments" condition and blocks implicit everyone access.
func LockDownWorkspace(t *testing.T, testServer *TestServer, workspaceID int) {
	t.Helper()

	// Get the admin user's ID via GET /users (bearer-token compatible)
	resp := MakeAuthRequest(t, testServer, http.MethodGet, "/users", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var users []map[string]interface{}
	DecodeJSON(t, resp, &users)
	if len(users) == 0 {
		t.Fatal("No users found")
	}

	// Find admin user (username "admin" from setup)
	var adminID int
	for _, u := range users {
		if u["username"] == "admin" {
			adminID = int(u["id"].(float64))
			break
		}
	}
	if adminID == 0 {
		// Fallback to first user
		adminID = int(users[0]["id"].(float64))
	}

	AssignWorkspaceRole(t, testServer, adminID, workspaceID, "Viewer")
}

// CreateTestItem creates a work item in a workspace and returns its ID.
func CreateTestItem(t *testing.T, testServer *TestServer, workspaceID int, title string) int {
	t.Helper()

	// Get default configuration set and item type
	configSetID := GetDefaultConfigurationSet(t, testServer)
	itemTypes := GetItemTypes(t, testServer, configSetID)

	// Use a regular type explicitly. Map iteration is nondeterministic and can
	// otherwise select the generic Sub-task type, which requires a parent.
	itemTypeID := itemTypes["Task"]

	if itemTypeID == 0 {
		t.Fatal("No item types found")
	}

	itemData := map[string]interface{}{
		"title":        title,
		"workspace_id": workspaceID,
		"item_type_id": itemTypeID,
	}

	resp := MakeAuthRequest(t, testServer, http.MethodPost, "/items", itemData)
	defer resp.Body.Close()

	AssertStatusCode(t, resp, http.StatusCreated)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)

	return ExtractIDFromResponse(t, result)
}

// CreateTestItemWithToken creates a work item using a specific bearer token.
func CreateTestItemWithToken(t *testing.T, testServer *TestServer, token string, workspaceID int, title string) (resp *http.Response, itemID int) {
	t.Helper()

	// Get default configuration set and item type (using admin token)
	configSetID := GetDefaultConfigurationSet(t, testServer)
	itemTypes := GetItemTypes(t, testServer, configSetID)

	// Use a regular type explicitly. Map iteration is nondeterministic and can
	// otherwise select the generic Sub-task type, which requires a parent.
	itemTypeID := itemTypes["Task"]

	if itemTypeID == 0 {
		t.Fatal("No item types found")
	}

	itemData := map[string]interface{}{
		"title":        title,
		"workspace_id": workspaceID,
		"item_type_id": itemTypeID,
	}

	resp = MakeAuthRequestWithToken(t, testServer, token, http.MethodPost, "/items", itemData)

	if resp.StatusCode == http.StatusCreated {
		var result map[string]interface{}
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		_ = json.Unmarshal(bodyBytes, &result)
		if id, ok := result["id"].(float64); ok {
			itemID = int(id)
		}
		// Recreate response for caller to check
		resp = &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(bytes.NewReader(bodyBytes)),
		}
		return resp, itemID
	}

	return resp, 0
}

// EmailChannelConfig contains configuration for creating an email channel
type EmailChannelConfig struct {
	Name              string
	WorkspaceID       int
	ItemTypeID        int
	EmailProviderID   int
	IMAPHost          string
	IMAPPort          int
	Username          string
	Password          string
	Encryption        string // "ssl", "tls", "starttls", "none"
	DefaultPriorityID *int
}

// CreateEmailProvider creates an email provider for testing
func CreateEmailProvider(t *testing.T, testServer *TestServer, name, providerType string) int {
	t.Helper()

	// Generate a slug from the name
	slug := strings.ToLower(strings.ReplaceAll(name, " ", "-"))

	data := map[string]interface{}{
		"name":       name,
		"slug":       slug,
		"type":       providerType,
		"is_enabled": true,
	}
	if providerType == "generic" {
		// Generic providers own the server-level connection settings. Channel
		// fixtures may still override credentials, but provider validation now
		// requires a complete, TLS-capable IMAP endpoint at creation time.
		data["imap_host"] = "localhost"
		data["imap_port"] = 993
		data["imap_encryption"] = "ssl"
	}

	resp := MakeAuthRequest(t, testServer, http.MethodPost, "/admin/email-providers", data)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to create email provider: %d - %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to parse email provider response: %v", err)
	}

	if id, ok := result["id"].(float64); ok {
		return int(id)
	}
	t.Fatal("No ID returned for email provider")
	return 0
}

// CreateInboundEmailChannel creates an inbound email channel for testing
func CreateInboundEmailChannel(t *testing.T, testServer *TestServer, config EmailChannelConfig) int {
	t.Helper()

	encryption := config.Encryption
	if encryption == "" {
		encryption = "none" // Plain for testing with mock server
	}

	channelConfig := map[string]interface{}{
		"email_provider_id":  config.EmailProviderID,
		"email_workspace_id": config.WorkspaceID,
		"email_item_type_id": config.ItemTypeID,
		"email_auth_method":  "basic",
		"email_mailbox":      "INBOX",
		"email_mark_as_read": true,
		// IMAP settings for generic provider
		"imap_host":       config.IMAPHost,
		"imap_port":       config.IMAPPort,
		"imap_username":   config.Username,
		"imap_password":   config.Password,
		"imap_encryption": encryption,
	}

	if config.DefaultPriorityID != nil {
		channelConfig["email_default_priority_id"] = *config.DefaultPriorityID
	}

	// Marshal the config to JSON string since Channel.Config is a string
	configJSON, err := json.Marshal(channelConfig)
	if err != nil {
		t.Fatalf("Failed to marshal channel config: %v", err)
	}

	data := map[string]interface{}{
		"name":        config.Name,
		"type":        "email",
		"direction":   "inbound",
		"description": "Test inbound email channel",
		"status":      "enabled",
		"config":      string(configJSON),
	}

	resp := MakeAuthRequest(t, testServer, http.MethodPost, "/channels", data)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to create email channel: %d - %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to parse email channel response: %v", err)
	}

	if id, ok := result["id"].(float64); ok {
		t.Logf("Created email channel ID: %d", int(id))
		return int(id)
	}
	t.Fatal("No ID returned for email channel")
	return 0
}

// TriggerEmailProcessing triggers immediate email processing for a channel
func TriggerEmailProcessing(t *testing.T, testServer *TestServer, channelID int) {
	t.Helper()

	endpoint := fmt.Sprintf("/channels/%d/process-emails", channelID)
	resp := MakeAuthRequest(t, testServer, http.MethodPost, endpoint, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Logf("Email processing trigger response: %d - %s", resp.StatusCode, string(body))
		// Don't fail - the channel might not process any emails and that's OK for some tests
	} else {
		t.Log("Email processing triggered successfully")
	}
}

// GetItemsByWorkspace returns items in a workspace
func GetItemsByWorkspace(t *testing.T, testServer *TestServer, workspaceID int) []map[string]interface{} {
	t.Helper()

	endpoint := fmt.Sprintf("/items?workspace_id=%d", workspaceID)
	resp := MakeAuthRequest(t, testServer, http.MethodGet, endpoint, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to get items: %d - %s", resp.StatusCode, string(body))
	}

	var result struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to parse items response: %v", err)
	}

	return result.Items
}

// AssociateWorkspaceWithConfigSet associates a workspace with a configuration set
func AssociateWorkspaceWithConfigSet(t *testing.T, testServer *TestServer, workspaceID, configSetID int) {
	t.Helper()

	// First, get the current configuration set
	getEndpoint := fmt.Sprintf("/configuration-sets/%d", configSetID)
	getResp := MakeAuthRequest(t, testServer, http.MethodGet, getEndpoint, nil)
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(getResp.Body)
		t.Fatalf("Failed to get config set %d: %d - %s", configSetID, getResp.StatusCode, string(body))
	}

	var configSet map[string]interface{}
	if err := json.NewDecoder(getResp.Body).Decode(&configSet); err != nil {
		t.Fatalf("Failed to decode config set: %v", err)
	}

	// Add workspace to WorkspaceIDs if not already present
	workspaceIDs := []int{}
	if ids, ok := configSet["workspace_ids"].([]interface{}); ok {
		for _, id := range ids {
			if idFloat, ok := id.(float64); ok {
				workspaceIDs = append(workspaceIDs, int(idFloat))
			}
		}
	}

	// Check if already associated
	for _, id := range workspaceIDs {
		if id == workspaceID {
			t.Logf("Workspace %d already associated with configuration set %d", workspaceID, configSetID)
			return
		}
	}
	workspaceIDs = append(workspaceIDs, workspaceID)
	configSet["workspace_ids"] = workspaceIDs

	// Update the configuration set with skip_migration_check
	updateEndpoint := fmt.Sprintf("/configuration-sets/%d?skip_migration_check=true", configSetID)
	updateResp := MakeAuthRequest(t, testServer, http.MethodPut, updateEndpoint, configSet)
	defer updateResp.Body.Close()

	if updateResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(updateResp.Body)
		t.Logf("Failed to associate workspace with config set: %d - %s", updateResp.StatusCode, string(body))
	} else {
		t.Logf("Associated workspace %d with configuration set %d", workspaceID, configSetID)
	}
}

// AddCustomFieldsToCreateScreens makes custom fields available to request
// types by adding them to each selected item type's effective create screen.
func AddCustomFieldsToCreateScreens(t *testing.T, testServer *TestServer, workspaceID int, itemTypeIDs, customFieldIDs []int) {
	t.Helper()

	screenIDs := map[int]struct{}{}
	fallbackScreenID := 0
	for _, itemTypeID := range itemTypeIDs {
		var configSetID int
		var screenID sql.NullInt64
		err := testServer.DB().QueryRow(`
			SELECT wcs.configuration_set_id, csit.create_screen_id
			FROM workspace_configuration_sets wcs
			JOIN configuration_set_item_types csit ON csit.configuration_set_id = wcs.configuration_set_id
			WHERE wcs.workspace_id = ? AND csit.item_type_id = ?
		`, workspaceID, itemTypeID).Scan(&configSetID, &screenID)
		if err != nil {
			t.Fatalf("resolve create screen for workspace %d item type %d: %v", workspaceID, itemTypeID, err)
		}
		if !screenID.Valid {
			if fallbackScreenID == 0 {
				createResp := MakeAuthRequest(t, testServer, http.MethodPost, "/screens", map[string]interface{}{
					"name":        fmt.Sprintf("Portal create screen %d", time.Now().UnixNano()),
					"description": "Create screen used by the portal integration fixture",
				})
				AssertStatusCode(t, createResp, http.StatusCreated)
				var created map[string]interface{}
				DecodeJSON(t, createResp, &created)
				createResp.Body.Close()
				fallbackScreenID = ExtractIDFromResponse(t, created)
			}
			if _, err := testServer.DB().Exec(`
				UPDATE configuration_set_item_types
				SET create_screen_id = ?
				WHERE configuration_set_id = ? AND item_type_id = ?
			`, fallbackScreenID, configSetID, itemTypeID); err != nil {
				t.Fatalf("assign create screen %d to configuration set %d item type %d: %v", fallbackScreenID, configSetID, itemTypeID, err)
			}
			screenID = sql.NullInt64{Int64: int64(fallbackScreenID), Valid: true}
		}
		screenIDs[int(screenID.Int64)] = struct{}{}
	}

	for screenID := range screenIDs {
		getResp := MakeAuthRequest(t, testServer, http.MethodGet, fmt.Sprintf("/screens/%d/fields", screenID), nil)
		AssertStatusCode(t, getResp, http.StatusOK)
		var fields []map[string]interface{}
		DecodeJSON(t, getResp, &fields)
		getResp.Body.Close()

		present := make(map[string]bool, len(fields))
		for _, field := range fields {
			identifier, _ := field["field_identifier"].(string)
			present[identifier] = true
		}
		for _, customFieldID := range customFieldIDs {
			identifier := fmt.Sprintf("%d", customFieldID)
			if present[identifier] {
				continue
			}
			fields = append(fields, map[string]interface{}{
				"field_type":       "custom",
				"field_identifier": identifier,
				"display_order":    len(fields),
				"is_required":      false,
				"field_width":      "full",
			})
		}

		putResp := MakeAuthRequest(t, testServer, http.MethodPut, fmt.Sprintf("/screens/%d/fields", screenID), fields)
		AssertStatusCode(t, putResp, http.StatusOK)
		putResp.Body.Close()
	}
}

// DB returns the underlying database for direct DB operations in tests
func (ts *TestServer) DB() database.Database {
	return ts.server.DB()
}

// CreatePortalCustomerWithSession creates a portal customer via the admin API
// and inserts a session directly into the database. Returns customerID and raw session token.
func CreatePortalCustomerWithSession(t *testing.T, testServer *TestServer, channelID int, name, email string) (customerID int, sessionCookie string) {
	t.Helper()

	// Create portal customer via admin API
	customerData := map[string]interface{}{
		"name":  name,
		"email": email,
	}

	resp := MakeAuthRequest(t, testServer, http.MethodPost, "/portal-customers", customerData)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to create portal customer %s: %d - %s", name, resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	customerID = ExtractIDFromResponse(t, result)

	// Generate a random session token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		t.Fatalf("Failed to generate session token: %v", err)
	}
	rawToken := fmt.Sprintf("%x", tokenBytes)

	// Insert session directly into the database. channel_id binds the session
	// to a specific portal channel — verifyPortalSessionBinding in
	// internal/handlers/portal.go rejects requests whose resolved channel
	// doesn't match. ip_address is left empty so the IP-binding check in
	// ValidatePortalSession (added with the WI-179 hardening) treats the
	// row as a legacy session and skips the bind check — the test inserts
	// the row out-of-band, so it can't predict which loopback address
	// (127.0.0.1 vs ::1) the in-process server will see for each request.
	db := testServer.DB()
	_, err := db.ExecWrite(
		`INSERT INTO portal_customer_sessions (portal_customer_id, session_token, channel_id, expires_at, ip_address, user_agent, is_active, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		customerID, rawToken, channelID, time.Now().Add(7*24*time.Hour), "", "test-agent", true, time.Now(),
	)
	if err != nil {
		t.Fatalf("Failed to insert portal session: %v", err)
	}

	// The cookie-auth surface no longer accepts session tokens via
	// Authorization: Bearer — that header is reserved for crw_* API tokens on
	// /rest/api/v1/*. Mint a real portal session cookie by replaying the
	// raw token through a sibling PortalSessionManager (same secret as the
	// in-process server) and returning the resulting "name=value" Cookie-
	// header pair for MakePortalRequest to replay.
	return customerID, encodePortalSessionCookie(t, rawToken)
}

// encodePortalSessionCookie builds the Cookie request header value
// ("windshift_portal_session=<encoded>") for a raw portal session token by
// replaying the server's SetPortalSessionCookie against a recorder. Constructs
// a stand-in PortalSessionManager (nil DB is fine — encoding doesn't touch it)
// keyed to the same SessionSecret StartTestServer feeds the in-process server.
func encodePortalSessionCookie(t *testing.T, rawToken string) string {
	t.Helper()

	psm := auth.NewPortalSessionManager(nil, false, false, nil, testSessionSecret, "strict")
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := psm.SetPortalSessionCookie(recorder, req, rawToken); err != nil {
		t.Fatalf("Failed to encode portal session cookie: %v", err)
	}
	setCookie := recorder.Header().Get("Set-Cookie")
	if setCookie == "" {
		t.Fatal("SetPortalSessionCookie produced no Set-Cookie header")
	}
	if i := strings.IndexByte(setCookie, ';'); i >= 0 {
		return setCookie[:i]
	}
	return setCookie
}

// MakeUnauthenticatedRequest makes a request with no authentication
func MakeUnauthenticatedRequest(t *testing.T, testServer *TestServer, method, endpoint string, body interface{}) *http.Response {
	t.Helper()

	url := testServer.APIBase + endpoint
	return makeRequest(t, method, url, "", body, nil)
}

// MakePortalRequest makes a request authenticated by a portal customer session.
// portalSessionCookie is the "name=value" Cookie-header pair returned by
// CreatePortalCustomerWithSession. The portal API is on the cookie-auth surface
// and does not accept Authorization: Bearer — that header is reserved for
// crw_* API tokens on /rest/api/v1/*.
func MakePortalRequest(t *testing.T, testServer *TestServer, portalSessionCookie, method, endpoint string, body interface{}) *http.Response {
	t.Helper()

	url := testServer.APIBase + endpoint
	return makeSessionRequest(t, method, url, portalSessionCookie, body, nil)
}

// SetupPortalChannel creates a portal channel with a slug and a request type.
// Returns the portal slug and the underlying channel ID. The channel ID is
// needed by CreatePortalCustomerWithSession to bind sessions to the channel
// (see verifyPortalSessionBinding in internal/handlers/portal.go).
func SetupPortalChannel(t *testing.T, testServer *TestServer, workspaceID int) (portalSlug string, channelID int) {
	t.Helper()

	timestamp := time.Now().UnixNano()
	portalSlug = fmt.Sprintf("test-portal-%d", timestamp)

	// Create the channel
	channelData := map[string]interface{}{
		"name":        fmt.Sprintf("Test Portal %d", timestamp),
		"type":        "portal",
		"direction":   "inbound",
		"description": "Portal for boundary testing",
		"status":      "enabled",
	}

	resp := MakeAuthRequest(t, testServer, http.MethodPost, "/channels", channelData)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	channelID = ExtractIDFromResponse(t, result)

	// Configure the portal with slug and workspace
	updateData := map[string]interface{}{
		"config": map[string]interface{}{
			"portal_slug":          portalSlug,
			"portal_enabled":       true,
			"portal_title":         "Test Portal",
			"portal_description":   "Test portal for boundary tests",
			"portal_workspace_ids": []int{workspaceID},
		},
	}

	resp2 := MakeAuthRequest(t, testServer, http.MethodPut, fmt.Sprintf("/channels/%d/config", channelID), updateData)
	defer resp2.Body.Close()
	AssertStatusCode(t, resp2, http.StatusOK)

	toggleResp := MakeAuthRequest(t, testServer, http.MethodPut, fmt.Sprintf("/channels/%d/toggle", channelID), nil)
	defer toggleResp.Body.Close()
	AssertStatusCode(t, toggleResp, http.StatusOK)

	// Create a request type for submissions
	configSetID := GetDefaultConfigurationSet(t, testServer)
	itemTypes := GetItemTypes(t, testServer, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

	requestTypeData := map[string]interface{}{
		"name":         "General Request",
		"description":  "General request type",
		"item_type_id": itemTypeID,
		"icon":         "Circle",
		"color":        "#666666",
		"is_active":    true,
	}

	resp3 := MakeAuthRequest(t, testServer, http.MethodPost, fmt.Sprintf("/channels/%d/request-types", channelID), requestTypeData)
	defer resp3.Body.Close()
	AssertStatusCode(t, resp3, http.StatusCreated)

	return portalSlug, channelID
}

// SubmitPortalRequest submits a request through the portal for a specific portal customer.
// Requires portal authentication token from CreatePortalCustomerWithSession.
// Returns the created item ID.
func SubmitPortalRequest(t *testing.T, testServer *TestServer, portalSlug, portalToken, title string) int {
	t.Helper()

	submissionData := map[string]interface{}{
		"title":       title,
		"description": "Test portal submission",
	}

	endpoint := fmt.Sprintf("/portal/%s/submit", portalSlug)
	submitResp := MakePortalRequest(t, testServer, portalToken, http.MethodPost, endpoint, submissionData)
	defer submitResp.Body.Close()

	AssertStatusCode(t, submitResp, http.StatusCreated)

	var result map[string]interface{}
	DecodeJSON(t, submitResp, &result)

	if itemID, ok := result["item_id"].(float64); ok {
		return int(itemID)
	}
	t.Fatal("No item_id in portal submission response")
	return 0
}

// GetItemComments returns comments for an item
func GetItemComments(t *testing.T, testServer *TestServer, itemID int) []map[string]interface{} {
	t.Helper()

	endpoint := fmt.Sprintf("/items/%d/comments", itemID)
	resp := MakeAuthRequest(t, testServer, http.MethodGet, endpoint, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to get comments: %d - %s", resp.StatusCode, string(body))
	}

	var result struct {
		Comments []map[string]interface{} `json:"comments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to parse comments response: %v", err)
	}

	return result.Comments
}
