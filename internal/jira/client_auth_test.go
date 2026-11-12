package jira

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

func TestJiraAuthHeaderMatchesDeploymentContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  Config
		want    string
		wantErr error
	}{
		{
			name: "Cloud uses email and API token as Basic credentials",
			config: Config{
				Email:          "admin@example.com",
				APIToken:       "cloud-token",
				DeploymentType: DeploymentCloud,
			},
			want: "Basic " + base64.StdEncoding.EncodeToString([]byte("admin@example.com:cloud-token")),
		},
		{
			name: "Data Center uses PAT as Bearer without username",
			config: Config{
				APIToken:       "data-center-pat",
				DeploymentType: DeploymentDataCenter,
			},
			want: "Bearer data-center-pat",
		},
		{
			name: "Cloud still requires email",
			config: Config{
				APIToken:       "cloud-token",
				DeploymentType: DeploymentCloud,
			},
			wantErr: ErrInvalidCredentials,
		},
		{
			name: "Data Center requires PAT",
			config: Config{
				DeploymentType: DeploymentDataCenter,
			},
			wantErr: ErrInvalidCredentials,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := jiraAuthHeader(test.config)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("jiraAuthHeader() error = %v, want %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("jiraAuthHeader() = %q, want %q", got, test.want)
			}
		})
	}
}

func newCloudTestClient(server *httptest.Server) *cloudClient {
	return &cloudClient{
		baseURL:    server.URL + "/rest/api/3",
		authHeader: "Basic test-token",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}
}

// TestCloudRoutingProbeKeepsGatewayWhenMyselfLacksScope is the WI-2060
// regression: a scoped token authorized for issue reads but not read:me gets
// 403 from gateway /myself and must still route through the gateway. Only an
// outright 401 (legacy token or invalid credentials) selects the site URL, and
// a transient probe failure must not downgrade routing.
func TestCloudRoutingProbeKeepsGatewayWhenMyselfLacksScope(t *testing.T) {
	t.Parallel()

	const cloudID = "cloud-scope"
	tests := []struct {
		name           string
		myselfStatus   int
		wantViaGateway bool
	}{
		{name: "missing read:me scope keeps gateway", myselfStatus: http.StatusForbidden, wantViaGateway: true},
		{name: "rejected credential uses site", myselfStatus: http.StatusUnauthorized, wantViaGateway: false},
		{name: "transient probe keeps gateway", myselfStatus: http.StatusInternalServerError, wantViaGateway: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var sitePaths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/_edge/tenant_info":
					_, _ = fmt.Fprintf(w, `{"cloudId":%q}`, cloudID)
				case "/gateway/" + cloudID + "/rest/api/3/myself":
					w.WriteHeader(test.myselfStatus)
				case "/gateway/" + cloudID + "/rest/servicedeskapi/assets/workspace":
					w.WriteHeader(http.StatusNotFound)
				case "/rest/servicedeskapi/assets/workspace":
					sitePaths = append(sitePaths, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)

			routing := cloudRoutingProbeWithGateway(server.URL, "Basic test-token", server.Client(), server.URL+"/gateway/")
			if routing.viaGateway != test.wantViaGateway {
				t.Fatalf("viaGateway = %t, want %t", routing.viaGateway, test.wantViaGateway)
			}
			if test.wantViaGateway {
				wantPlatform := server.URL + "/gateway/" + cloudID + "/rest/api/3"
				if routing.platformBase != wantPlatform {
					t.Fatalf("platformBase = %q, want %q", routing.platformBase, wantPlatform)
				}
				if len(sitePaths) != 0 {
					t.Fatalf("gateway routing made site requests: %v", sitePaths)
				}
			}
		})
	}
}

// TestCloudTestConnectionRequiresAuthenticatedEvidence is the WI-2064/WI-1153
// regression: anonymous /serverInfo must not validate credentials. Only a 401
// from /myself (authenticated endpoint) rejects the token; a 403 means the
// token authenticated but lacks read:me and must still connect.
func TestCloudTestConnectionRequiresAuthenticatedEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		myselfStatus    int
		myselfBody      string
		serverInfoBody  string
		wantErr         error
		wantDisplayName string
		wantURL         string
	}{
		{
			name:           "anonymous serverInfo cannot validate a rejected token",
			myselfStatus:   http.StatusUnauthorized,
			myselfBody:     `{"errorMessages":["Client must be authenticated"]}`,
			serverInfoBody: `{"baseUrl":"https://example.atlassian.net","serverTitle":"Example"}`,
			wantErr:        ErrInvalidCredentials,
		},
		{
			name:            "scoped token without read:me still connects",
			myselfStatus:    http.StatusForbidden,
			myselfBody:      `{"errorMessages":["scope does not match"]}`,
			serverInfoBody:  `{"baseUrl":"https://example.atlassian.net","serverTitle":"Example"}`,
			wantDisplayName: "Example",
			wantURL:         "https://example.atlassian.net",
		},
		{
			name:            "authenticated identity enriches the display name",
			myselfStatus:    http.StatusOK,
			myselfBody:      `{"displayName":"Jane Operator"}`,
			serverInfoBody:  `{"baseUrl":"https://example.atlassian.net","serverTitle":"Example"}`,
			wantDisplayName: "Jane Operator",
			wantURL:         "https://example.atlassian.net",
		},
		{
			name:            "fallback URL strips the REST prefix",
			myselfStatus:    http.StatusOK,
			myselfBody:      `{"displayName":"Jane Operator"}`,
			serverInfoBody:  `{"serverTitle":"Example"}`,
			wantDisplayName: "Jane Operator",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/rest/api/3/myself":
					w.WriteHeader(test.myselfStatus)
					_, _ = w.Write([]byte(test.myselfBody))
				case "/rest/api/3/serverInfo":
					_, _ = w.Write([]byte(test.serverInfoBody))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)

			client := newCloudTestClient(server)
			info, err := client.TestConnection(context.Background())
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("TestConnection() error = %v, want %v", err, test.wantErr)
				}
				if info != nil {
					t.Fatalf("TestConnection() info = %#v, want nil", info)
				}
				if len(paths) != 1 || paths[0] != "/rest/api/3/myself" {
					t.Fatalf("request paths = %v, want only /rest/api/3/myself", paths)
				}
				return
			}
			if err != nil {
				t.Fatalf("TestConnection() error = %v", err)
			}
			if info.DisplayName != test.wantDisplayName {
				t.Fatalf("DisplayName = %q, want %q", info.DisplayName, test.wantDisplayName)
			}
			if test.wantURL != "" && info.URL != test.wantURL {
				t.Fatalf("URL = %q, want %q", info.URL, test.wantURL)
			}
			if test.wantURL == "" && info.URL != server.URL {
				t.Fatalf("URL = %q, want stripped fallback %q", info.URL, server.URL)
			}
		})
	}
}

func TestDataCenterClientUsesPATBearerAuthenticationWithoutUsername(t *testing.T) {
	t.Parallel()

	var paths []string
	var authHeaders []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/2/serverInfo":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"baseUrl":     server.URL,
				"serverTitle": "Example Jira",
			})
		case "/rest/api/2/myself":
			_ = json.NewEncoder(w).Encode(map[string]string{"displayName": "PAT User"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(Config{
		InstanceURL:    "https://jira.example.invalid",
		APIToken:       "data-center-pat",
		DeploymentType: DeploymentDataCenter,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	dcClient, ok := client.(*dataCenterClient)
	if !ok {
		t.Fatalf("NewClient() type = %T, want *dataCenterClient", client)
	}
	dcClient.baseURL = server.URL + "/rest/api/2"
	dcClient.httpClient = server.Client()
	dcClient.limiter = rate.NewLimiter(rate.Inf, 1)

	info, err := dcClient.TestConnection(context.Background())
	if err != nil {
		t.Fatalf("TestConnection() error = %v", err)
	}
	if info.DisplayName != "PAT User" {
		t.Fatalf("DisplayName = %q, want PAT User", info.DisplayName)
	}
	if len(paths) != 2 || paths[0] != "/rest/api/2/myself" || paths[1] != "/rest/api/2/serverInfo" {
		t.Fatalf("request paths = %v, want myself then serverInfo", paths)
	}
	for index, authHeader := range authHeaders {
		if authHeader != "Bearer data-center-pat" {
			t.Fatalf("request %d Authorization = %q, want Bearer data-center-pat", index+1, authHeader)
		}
	}
}

func TestDataCenterConnectionRejectsInvalidPATWhenServerInfoIsPublic(t *testing.T) {
	t.Parallel()

	var paths []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/2/myself":
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"errorMessages": "invalid personal access token"})
		case "/rest/api/2/serverInfo":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"baseUrl":     server.URL,
				"serverTitle": "Example Jira",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(Config{
		InstanceURL:    "https://jira.example.invalid",
		APIToken:       "invalid-data-center-pat",
		DeploymentType: DeploymentDataCenter,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	dcClient, ok := client.(*dataCenterClient)
	if !ok {
		t.Fatalf("NewClient() type = %T, want *dataCenterClient", client)
	}
	dcClient.baseURL = server.URL + "/rest/api/2"
	dcClient.httpClient = server.Client()
	dcClient.limiter = rate.NewLimiter(rate.Inf, 1)

	info, err := dcClient.TestConnection(context.Background())
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("TestConnection() error = %v, want ErrInvalidCredentials", err)
	}
	if info != nil {
		t.Fatalf("TestConnection() info = %#v, want nil", info)
	}
	if len(paths) != 1 || paths[0] != "/rest/api/2/myself" {
		t.Fatalf("request paths = %v, want only myself", paths)
	}
}
