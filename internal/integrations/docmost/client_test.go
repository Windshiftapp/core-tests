//go:build test

package docmost

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/utils"
)

func allowLocal(t *testing.T) {
	t.Helper()
	previous := utils.AllowLocalConnections()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(previous) })
}

func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "https", input: "https://wiki.example.com", want: "https://wiki.example.com"},
		{name: "http private host", input: "http://localhost:3200", want: "http://localhost:3200"},
		{name: "trailing slash trimmed", input: "https://wiki.example.com/", want: "https://wiki.example.com"},
		{name: "whitespace trimmed", input: "  https://wiki.example.com  ", want: "https://wiki.example.com"},
		{name: "empty", input: "", wantErr: true},
		{name: "wrong scheme", input: "ftp://wiki.example.com", wantErr: true},
		{name: "no host", input: "https://", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeBaseURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				if !errors.Is(err, ErrInvalidURL) {
					t.Fatalf("error = %v, want ErrInvalidURL", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSearchParsesEnvelope(t *testing.T) {
	allowLocal(t)
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_, _ = w.Write([]byte(`{"data":{"items":[{"id":"p1","slugId":"AbC","title":"Runbook","highlight":"the <b>deploy</b> steps","rank":0.9,"space":{"name":"Eng","slug":"eng"}}]}}`))
	}))
	t.Cleanup(srv.Close)

	results, err := NewClient().Search(context.Background(), srv.URL, "dcm_token", "deploy", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if gotAuth != "Bearer dcm_token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"query":"deploy"`) {
		t.Fatalf("request body = %q", gotBody)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	want := SearchResult{ID: "p1", SlugID: "AbC", Title: "Runbook", Highlight: "the <b>deploy</b> steps", Rank: 0.9, SpaceName: "Eng", SpaceSlug: "eng"}
	if results[0] != want {
		t.Fatalf("result = %+v, want %+v", results[0], want)
	}
	if url := PageURL(srv.URL, "eng", "AbC"); url != srv.URL+"/s/eng/p/AbC" {
		t.Fatalf("page URL = %q", url)
	}
}

func TestSearchMapsUnauthorized(t *testing.T) {
	allowLocal(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
	}))
	t.Cleanup(srv.Close)

	if _, err := NewClient().Search(context.Background(), srv.URL, "bad", "q", 10); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("search error = %v, want ErrUnauthorized", err)
	}
}

func TestSearchCapsResponse(t *testing.T) {
	allowLocal(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"items":[]}}`))
		_, _ = w.Write([]byte(strings.Repeat(" ", maxResponseBytes+1)))
	}))
	t.Cleanup(srv.Close)

	if _, err := NewClient().Search(context.Background(), srv.URL, "token", "q", 10); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("search error = %v, want ErrUnavailable", err)
	}
}

func TestRegisterClient(t *testing.T) {
	allowLocal(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oauth/register" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"client-abc"}`))
	}))
	t.Cleanup(srv.Close)

	clientID, err := NewClient().RegisterClient(context.Background(), srv.URL, "Windshift", "http://windshift.test/cb")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if clientID != "client-abc" {
		t.Fatalf("client id = %q", clientID)
	}
}

func TestRegisterClientOAuthDisabled(t *testing.T) {
	allowLocal(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	if _, err := NewClient().RegisterClient(context.Background(), srv.URL, "Windshift", "http://windshift.test/cb"); !errors.Is(err, ErrOAuthDisabled) {
		t.Fatalf("register error = %v, want ErrOAuthDisabled", err)
	}
}

func TestExchangeCode(t *testing.T) {
	allowLocal(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oauth/token" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		if r.PostFormValue("grant_type") != "authorization_code" || r.PostFormValue("code_verifier") != "verifier" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"acc","refresh_token":"ref","token_type":"Bearer","scope":"read write","expires_in":3600}`))
	}))
	t.Cleanup(srv.Close)

	token, err := NewClient().ExchangeCode(context.Background(), srv.URL, "client-abc", "http://windshift.test/cb", "code", "verifier")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if token.AccessToken != "acc" || token.RefreshToken != "ref" || token.ExpiresAt.IsZero() {
		t.Fatalf("token = %+v", token)
	}
}

func TestExchangeCodeRejected(t *testing.T) {
	allowLocal(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	t.Cleanup(srv.Close)

	if _, err := NewClient().ExchangeCode(context.Background(), srv.URL, "client-abc", "http://windshift.test/cb", "bad", "verifier"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("exchange error = %v, want ErrUnauthorized", err)
	}
}
