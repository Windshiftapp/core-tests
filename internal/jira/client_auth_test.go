package jira

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
