//go:build test

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/restapi"
)

// TestRespondInternalErrorMapsClientCancellationToRequestTimeout pins the
// contract that a canceled request context (navigation or reload abort) is not
// answered as an internal server error.
func TestRespondInternalErrorMapsClientCancellationToRequestTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/2/bootstrap", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()

	respondInternalError(recorder, request, fmt.Errorf("assignable users: %w", context.Canceled))

	if recorder.Code != http.StatusRequestTimeout {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusRequestTimeout)
	}
	var body restapi.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != "REQUEST_CANCELED" {
		t.Fatalf("code = %q, want REQUEST_CANCELED", body.Code)
	}
}

// TestRespondInternalErrorDoesNotLogClientCancellationAsError pins that the
// expected client-abort path stops polluting error logs.
func TestRespondInternalErrorDoesNotLogClientCancellationAsError(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/2/bootstrap", nil)
	respondInternalError(httptest.NewRecorder(), request, fmt.Errorf("list bindings: %w", context.Canceled))

	logged := buf.String()
	if strings.Contains(logged, "internal server error") {
		t.Fatalf("client cancellation logged as internal error: %s", logged)
	}
	if !strings.Contains(logged, "request canceled by client") {
		t.Fatalf("client cancellation not logged: %s", logged)
	}
}

// TestRespondInternalErrorStillReportsServerFaults guards the normal path.
func TestRespondInternalErrorStillReportsServerFaults(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/2/bootstrap", nil)
	recorder := httptest.NewRecorder()

	respondInternalError(recorder, request, errors.New("database is down"))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(buf.String(), "internal server error") {
		t.Fatalf("server fault not logged as internal error: %s", buf.String())
	}
}
