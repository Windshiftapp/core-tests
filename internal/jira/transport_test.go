package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/time/rate"
)

func testJiraTransport(server *httptest.Server) jiraTransport {
	return &cloudClient{
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}
}

func TestJiraErrorFromResponseMapsStatusContract(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{"unauthorized keeps upstream message", http.StatusUnauthorized, `{"errorMessages":["bad token"]}`, ErrInvalidCredentials},
		{"forbidden", http.StatusForbidden, "nope", ErrForbidden},
		{"forbidden rate-limit wording", http.StatusForbidden, "rate limit exceeded", ErrRateLimited},
		{"not found", http.StatusNotFound, "issue does not exist", ErrNotFound},
		{"too many requests", http.StatusTooManyRequests, "", ErrRateLimited},
		{"server error", http.StatusInternalServerError, "boom", ErrAPIError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tc.status,
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}
			err := jiraErrorFromResponse(resp)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("jiraErrorFromResponse(%d) = %v, want %v", tc.status, err, tc.wantErr)
			}
			if tc.wantErr != ErrRateLimited && tc.body != "" && !strings.Contains(err.Error(), tc.body) {
				t.Fatalf("error %q should preserve the upstream body %q", err, tc.body)
			}
		})
	}
}

func TestJiraRequestJSONDecodesAndMapsErrors(t *testing.T) {
	t.Run("malformed JSON surfaces a decode error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{not json"))
		}))
		defer server.Close()

		var target struct {
			Value string `json:"value"`
		}
		err := jiraGetJSON(context.Background(), testJiraTransport(server), server.URL+"/x", &target)
		if err == nil || !strings.Contains(err.Error(), "invalid character") {
			t.Fatalf("expected JSON decode error, got %v", err)
		}
	})

	t.Run("non-200 maps through the shared error contract", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("missing"))
		}))
		defer server.Close()

		var target struct{}
		err := jiraGetJSON(context.Background(), testJiraTransport(server), server.URL+"/x", &target)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("cancelled context aborts the request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var target struct{}
		err := jiraGetJSON(ctx, testJiraTransport(server), server.URL+"/x", &target)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}

func TestJiraServiceDeskValuesAccumulatesAllPages(t *testing.T) {
	var seenStart []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenStart = append(seenStart, r.URL.Query().Get("start"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("start") {
		case "0":
			_ = json.NewEncoder(w).Encode(JiraServiceDeskPage[JiraServiceDesk]{
				IsLastPage: false,
				Values:     []JiraServiceDesk{{ID: "1"}, {ID: "2"}},
			})
		case "2":
			_ = json.NewEncoder(w).Encode(JiraServiceDeskPage[JiraServiceDesk]{
				IsLastPage: true,
				Values:     []JiraServiceDesk{{ID: "3"}},
			})
		default:
			t.Fatalf("unexpected start: %s", r.URL.Query().Get("start"))
		}
	}))
	defer server.Close()

	client := &dataCenterClient{
		serviceDeskURL: server.URL + "/rest/servicedeskapi",
		httpClient:     server.Client(),
		limiter:        rate.NewLimiter(rate.Inf, 1),
	}
	desks, err := client.ListServiceDesks(context.Background())
	if err != nil {
		t.Fatalf("ListServiceDesks returned error: %v", err)
	}
	if fmt.Sprint(seenStart) != "[0 2]" {
		t.Fatalf("start sequence = %v, want [0 2]", seenStart)
	}
	if len(desks) != 3 || desks[0].ID != "1" || desks[1].ID != "2" || desks[2].ID != "3" {
		t.Fatalf("unexpected desks: %#v", desks)
	}
}

func TestJiraServiceDeskValuesStopsOnEmptyPage(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		// Jira occasionally omits isLastPage while returning no rows; the loop
		// must terminate rather than re-request the same offset forever.
		_ = json.NewEncoder(w).Encode(JiraServiceDeskPage[JiraServiceDesk]{Values: []JiraServiceDesk{}})
	}))
	defer server.Close()

	client := &dataCenterClient{
		serviceDeskURL: server.URL + "/rest/servicedeskapi",
		httpClient:     server.Client(),
		limiter:        rate.NewLimiter(rate.Inf, 1),
	}
	desks, err := client.ListServiceDesks(context.Background())
	if err != nil {
		t.Fatalf("ListServiceDesks returned error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly one request, got %d", calls)
	}
	if len(desks) != 0 {
		t.Fatalf("expected no desks, got %#v", desks)
	}
}

func TestJiraAccumulateStartAtValuesBoundaries(t *testing.T) {
	t.Run("advances past the server-echoed offset and stops at total", func(t *testing.T) {
		var seenStartAt []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seenStartAt = append(seenStartAt, r.URL.Query().Get("startAt"))
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Query().Get("startAt") {
			case "0":
				_ = json.NewEncoder(w).Encode(jiraStartAtPage[int]{StartAt: 0, MaxResults: 2, Total: 5, Values: []int{1, 2}})
			case "2":
				// Echo a later offset than requested; the client must follow
				// the server's startAt rather than its own arithmetic.
				_ = json.NewEncoder(w).Encode(jiraStartAtPage[int]{StartAt: 3, MaxResults: 2, Total: 5, Values: []int{4}})
			case "4":
				_ = json.NewEncoder(w).Encode(jiraStartAtPage[int]{StartAt: 4, MaxResults: 2, Total: 5, IsLast: true, Values: []int{5}})
			default:
				t.Fatalf("unexpected startAt: %s", r.URL.Query().Get("startAt"))
			}
		}))
		defer server.Close()

		agg, err := jiraAccumulateStartAtValues[int](
			context.Background(), testJiraTransport(server), 2,
			func(startAt int) string { return fmt.Sprintf("%s/x?startAt=%d", server.URL, startAt) },
		)
		if err != nil {
			t.Fatalf("accumulate returned error: %v", err)
		}
		if fmt.Sprint(seenStartAt) != "[0 2 4]" {
			t.Fatalf("startAt sequence = %v, want [0 2 4]", seenStartAt)
		}
		if fmt.Sprint(agg.Values) != "[1 2 4 5]" || agg.Total != 5 {
			t.Fatalf("unexpected aggregate: %#v", agg)
		}
	})

	t.Run("stops when the aggregate reaches the server total", func(t *testing.T) {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Content-Type", "application/json")
			// isLast is false but the first page already satisfies total.
			_ = json.NewEncoder(w).Encode(jiraStartAtPage[int]{StartAt: 0, MaxResults: 2, Total: 2, Values: []int{1, 2}})
		}))
		defer server.Close()

		agg, err := jiraAccumulateStartAtValues[int](
			context.Background(), testJiraTransport(server), 2,
			func(startAt int) string { return fmt.Sprintf("%s/x?startAt=%d", server.URL, startAt) },
		)
		if err != nil {
			t.Fatalf("accumulate returned error: %v", err)
		}
		if calls != 1 {
			t.Fatalf("expected one request, got %d", calls)
		}
		if fmt.Sprint(agg.Values) != "[1 2]" {
			t.Fatalf("unexpected aggregate: %#v", agg)
		}
	})

	t.Run("mid-pagination error aborts with the mapped error", func(t *testing.T) {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Content-Type", "application/json")
			if calls == 1 {
				_ = json.NewEncoder(w).Encode(jiraStartAtPage[int]{StartAt: 0, MaxResults: 1, Total: 9, Values: []int{1}})
				return
			}
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("denied"))
		}))
		defer server.Close()

		_, err := jiraAccumulateStartAtValues[int](
			context.Background(), testJiraTransport(server), 1,
			func(startAt int) string { return fmt.Sprintf("%s/x?startAt=%d", server.URL, startAt) },
		)
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})
}

func TestJiraDownloadAttachmentContract(t *testing.T) {
	t.Run("streams the body and content type on success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Fatalf("missing Authorization header: %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png-bytes"))
		}))
		defer server.Close()

		client := &cloudClient{
			authHeader: "Bearer token",
			httpClient: server.Client(),
			limiter:    rate.NewLimiter(rate.Inf, 1),
		}
		body, contentType, err := client.DownloadAttachment(context.Background(), server.URL+"/attachment/1")
		if err != nil {
			t.Fatalf("DownloadAttachment returned error: %v", err)
		}
		defer func() { _ = body.Close() }()
		if contentType != "image/png" {
			t.Fatalf("content type = %q, want image/png", contentType)
		}
		data, err := io.ReadAll(body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(data) != "png-bytes" {
			t.Fatalf("body = %q, want png-bytes", data)
		}
	})

	t.Run("non-200 maps through the shared error contract", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("gone"))
		}))
		defer server.Close()

		client := &dataCenterClient{
			authHeader: "Bearer token",
			httpClient: server.Client(),
			limiter:    rate.NewLimiter(rate.Inf, 1),
		}
		_, _, err := client.DownloadAttachment(context.Background(), server.URL+"/attachment/1")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("cancelled context aborts the download", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer server.Close()

		client := &cloudClient{
			authHeader: "Bearer token",
			httpClient: server.Client(),
			limiter:    rate.NewLimiter(rate.Inf, 1),
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := client.DownloadAttachment(ctx, server.URL+"/attachment/1")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}

func TestJiraTestConnectionRejectsMalformedJSON(t *testing.T) {
	t.Run("cloud", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{not json"))
		}))
		defer server.Close()

		client := &cloudClient{
			baseURL:    server.URL + "/rest/api/3",
			httpClient: server.Client(),
			limiter:    rate.NewLimiter(rate.Inf, 1),
		}
		_, err := client.TestConnection(context.Background())
		if err == nil || !strings.Contains(err.Error(), "decode serverInfo") {
			t.Fatalf("expected serverInfo decode error, got %v", err)
		}
	})

	t.Run("data center", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{not json"))
		}))
		defer server.Close()

		client := &dataCenterClient{
			baseURL:    server.URL + "/rest/api/2",
			httpClient: server.Client(),
			limiter:    rate.NewLimiter(rate.Inf, 1),
		}
		_, err := client.TestConnection(context.Background())
		if err == nil || !strings.Contains(err.Error(), "decode myself") {
			t.Fatalf("expected myself decode error, got %v", err)
		}
	})
}

func TestJiraErrorFromResponseBoundsErrorBodyReads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		// 8 MiB error page: the client must not buffer it whole.
		chunk := make([]byte, 1<<20)
		for i := range chunk {
			chunk[i] = 'x'
		}
		for i := 0; i < 8; i++ {
			_, _ = w.Write(chunk)
		}
	}))
	defer server.Close()

	var target struct{}
	err := jiraGetJSON(context.Background(), testJiraTransport(server), server.URL+"/x", &target)
	if !errors.Is(err, ErrAPIError) {
		t.Fatalf("expected ErrAPIError, got %v", err)
	}
	// summarizeJiraErrorBody caps at 16K runes plus a truncation marker; a
	// multi-megabyte body must collapse to that bound.
	if len(err.Error()) > 20*1024 {
		t.Fatalf("error message not bounded: %d bytes", len(err.Error()))
	}
}

func TestCloudClientGetProjectFieldsUsesSharedErrorAndPaginationContract(t *testing.T) {
	t.Run("denials map to sentinel errors", func(t *testing.T) {
		for status, want := range map[int]error{
			http.StatusUnauthorized: ErrInvalidCredentials,
			http.StatusForbidden:    ErrForbidden,
			http.StatusNotFound:     ErrNotFound,
		} {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("denied by jira"))
			}))
			client := &cloudClient{
				baseURL:    server.URL + "/rest/api/3",
				httpClient: server.Client(),
				limiter:    rate.NewLimiter(rate.Inf, 1),
			}
			_, err := client.GetProjectFields(context.Background(), []string{"10001"})
			if !errors.Is(err, want) {
				t.Fatalf("status %d: expected %v, got %v", status, want, err)
			}
			server.Close()
		}
	})

	t.Run("accumulates startAt pages", func(t *testing.T) {
		var seenStartAt []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/rest/api/3/field/search" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			seenStartAt = append(seenStartAt, r.URL.Query().Get("startAt"))
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Query().Get("startAt") {
			case "0":
				_ = json.NewEncoder(w).Encode(jiraStartAtPage[JiraCustomField]{
					StartAt: 0, MaxResults: 1, Total: 2,
					Values: []JiraCustomField{{ID: "customfield_1", Custom: true}},
				})
			case "1":
				_ = json.NewEncoder(w).Encode(jiraStartAtPage[JiraCustomField]{
					StartAt: 1, MaxResults: 1, Total: 2, IsLast: true,
					Values: []JiraCustomField{{ID: "customfield_2", Custom: true}},
				})
			default:
				t.Fatalf("unexpected startAt: %s", r.URL.Query().Get("startAt"))
			}
		}))
		defer server.Close()

		client := &cloudClient{
			baseURL:    server.URL + "/rest/api/3",
			httpClient: server.Client(),
			limiter:    rate.NewLimiter(rate.Inf, 1),
		}
		fields, err := client.GetProjectFields(context.Background(), []string{"10001"})
		if err != nil {
			t.Fatalf("GetProjectFields returned error: %v", err)
		}
		if fmt.Sprint(seenStartAt) != "[0 1]" {
			t.Fatalf("startAt sequence = %v, want [0 1]", seenStartAt)
		}
		if len(fields) != 2 || fields[0].ID != "customfield_1" || fields[1].ID != "customfield_2" {
			t.Fatalf("unexpected fields: %#v", fields)
		}
	})
}
