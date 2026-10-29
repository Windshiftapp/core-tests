package jira

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"golang.org/x/time/rate"
)

type dispatchCountingTransport struct {
	count int
}

func (t *dispatchCountingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.count++
	return nil, errors.New("request unexpectedly dispatched")
}

func TestValidateReadOnlyRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		method  string
		url     string
		allowed bool
	}{
		{
			name:    "GET project metadata is allowed",
			method:  http.MethodGet,
			url:     "https://example.atlassian.net/rest/api/3/project",
			allowed: true,
		},
		{
			name:    "HEAD attachment metadata is allowed",
			method:  http.MethodHead,
			url:     "https://example.atlassian.net/secure/attachment/100/file.txt",
			allowed: true,
		},
		{
			name:    "POST JQL search is allowed",
			method:  http.MethodPost,
			url:     "https://example.atlassian.net/rest/api/3/search/jql",
			allowed: true,
		},
		{
			name:    "POST gateway JQL search is allowed",
			method:  http.MethodPost,
			url:     "https://api.atlassian.com/ex/jira/cloud-id/rest/api/3/search/jql",
			allowed: true,
		},
		{
			name:    "POST bulk issue fetch is allowed",
			method:  http.MethodPost,
			url:     "https://example.atlassian.net/rest/api/3/issue/bulkfetch",
			allowed: true,
		},
		{
			name:    "POST workflow read is allowed",
			method:  http.MethodPost,
			url:     "https://example.atlassian.net/rest/api/3/workflows",
			allowed: true,
		},
		{
			name:    "POST deprecated legacy Assets navlist is denied",
			method:  http.MethodPost,
			url:     "https://example.atlassian.net/rest/assets/1.0/object/navlist/aql",
			allowed: false,
		},
		{
			name:    "POST gateway Assets AQL search is allowed",
			method:  http.MethodPost,
			url:     "https://api.atlassian.com/ex/jira/cloud-id/jsm/assets/workspace/workspace-id/v1/object/aql",
			allowed: true,
		},
		{
			name:    "POST issue creation is denied",
			method:  http.MethodPost,
			url:     "https://example.atlassian.net/rest/api/3/issue",
			allowed: false,
		},
		{
			name:    "PUT issue update is denied",
			method:  http.MethodPut,
			url:     "https://example.atlassian.net/rest/api/3/issue/TEST-1",
			allowed: false,
		},
		{
			name:    "PATCH request is denied",
			method:  http.MethodPatch,
			url:     "https://example.atlassian.net/rest/api/3/issue/TEST-1",
			allowed: false,
		},
		{
			name:    "DELETE request is denied",
			method:  http.MethodDelete,
			url:     "https://example.atlassian.net/rest/api/3/issue/TEST-1",
			allowed: false,
		},
		{
			name:    "POST lookalike query path is denied",
			method:  http.MethodPost,
			url:     "https://example.atlassian.net/rest/api/3/search/jql/mutate",
			allowed: false,
		},
		{
			name:    "POST gateway Assets lookalike path is denied",
			method:  http.MethodPost,
			url:     "https://api.atlassian.com/ex/jira/cloud-id/jsm/assets/workspace/workspace-id/v1/object/aql/delete",
			allowed: false,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := validateReadOnlyRequest(test.method, test.url)
			if test.allowed {
				if err != nil {
					t.Fatalf("validateReadOnlyRequest() returned error: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrReadOnlyViolation) {
				t.Fatalf("validateReadOnlyRequest() error = %v, want ErrReadOnlyViolation", err)
			}
		})
	}
}

func TestJiraClientsRejectMutationsBeforeDispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(context.Context, string, string) (error, int)
	}{
		{
			name: "Cloud",
			call: func(ctx context.Context, method, requestURL string) (error, int) {
				transport := &dispatchCountingTransport{}
				client := &cloudClient{
					httpClient: &http.Client{Transport: transport},
					limiter:    rate.NewLimiter(rate.Inf, 1),
				}
				_, err := client.do(ctx, method, requestURL, map[string]any{"unsafe": true})
				return err, transport.count
			},
		},
		{
			name: "Data Center",
			call: func(ctx context.Context, method, requestURL string) (error, int) {
				transport := &dispatchCountingTransport{}
				client := &dataCenterClient{
					httpClient: &http.Client{Transport: transport},
					limiter:    rate.NewLimiter(rate.Inf, 1),
				}
				_, err := client.do(ctx, method, requestURL, map[string]any{"unsafe": true})
				return err, transport.count
			},
		},
	}
	methods := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, method := range methods {
				method := method
				t.Run(method, func(t *testing.T) {
					t.Parallel()
					err, dispatchCount := test.call(context.Background(), method, "https://jira.example.invalid/rest/api/3/issue/TEST-1")
					if !errors.Is(err, ErrReadOnlyViolation) {
						t.Fatalf("%s mutation error = %v, want ErrReadOnlyViolation", method, err)
					}
					if dispatchCount != 0 {
						t.Fatalf("%s dispatch count = %d, want 0", method, dispatchCount)
					}
				})
			}
		})
	}
}
