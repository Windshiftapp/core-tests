//go:build test

package v2

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func TestTestManagementErrorContract(t *testing.T) {
	failure := errors.New("database unavailable: private connection details")
	for _, tc := range []struct {
		name          string
		err           error
		status        int
		code, message string
	}{
		{"database", failure, 500, "internal_error", "An unexpected error occurred"},
		{"wrapped failure", fmt.Errorf("permission lookup: %w", failure), 500, "internal_error", "An unexpected error occurred"},
		{"forbidden", services.ErrTestManagementForbidden, 404, "not_found", "Test resource was not found"},
		{"missing", repository.ErrNotFound, 404, "not_found", "Test resource was not found"},
		{"wrapped validation", fmt.Errorf("private context: %w", services.ErrTestFolderNameRequired), 400, "invalid_request", "folder name is required"},
		{"folder validation", services.ErrTestFolderNameRequired, 400, "invalid_request", "folder name is required"},
		{"result validation", services.ErrInvalidTestResultStatus, 400, "invalid_request", services.ErrInvalidTestResultStatus.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mapped := testManagementError(tc.err)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/test-cases", nil)
			request.Header.Set("X-Request-ID", "regression-1297")
			Adapt(func(http.ResponseWriter, *http.Request) error { return mapped }).ServeHTTP(recorder, request)
			var body errorBody
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			want := errorBody{Error: errorContent{Code: tc.code, Message: tc.message}, RequestID: "regression-1297"}
			if recorder.Code != tc.status || body != want {
				t.Fatalf("response = %d %+v; want %d %+v", recorder.Code, body, tc.status, want)
			}
			if tc.status == 500 && !errors.Is(mapped, failure) {
				t.Fatal("internal cause was lost")
			}
		})
	}
	if err := testManagementError(nil); err != nil {
		t.Fatalf("success = %v", err)
	}
}

func TestTestManagementRunLookupFailureIsInternal(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	service := services.NewTestRunService(tdb.GetDatabase())
	tdb.Close()
	assignee := 1
	for _, tc := range []struct {
		name    string
		request services.TestRunCreateRequest
	}{
		{"set", services.TestRunCreateRequest{SetID: 1}},
		{"template", services.TestRunCreateRequest{TemplateID: 1}},
		{"assignee", services.TestRunCreateRequest{AssigneeID: &assignee}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.Create(1, tc.request)
			var mapped *Error
			if !errors.As(testManagementError(err), &mapped) || mapped.Status != 500 || mapped.Code != "internal_error" || mapped.Message != "An unexpected error occurred" {
				t.Fatalf("lookup failure = %#v; underlying error = %v", mapped, err)
			}
		})
	}
}

func TestTestManagementCaseValidationRemainsBadRequest(t *testing.T) {
	service := services.NewTestCaseService(nil)
	for _, tc := range []struct {
		name    string
		request services.TestCaseCreateRequest
		message string
	}{
		{"title", services.TestCaseCreateRequest{}, "test case title is required"},
		{"priority", services.TestCaseCreateRequest{Title: "Case", Priority: "invalid"}, "invalid priority value: must be low, medium, high, or critical"},
		{"status", services.TestCaseCreateRequest{Title: "Case", Status: "invalid"}, "invalid status value: must be active, inactive, or draft"},
		{"duration", services.TestCaseCreateRequest{Title: "Case", EstimatedDuration: -1}, "estimated duration cannot be negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.Create(1, tc.request)
			var mapped *Error
			if !errors.As(testManagementError(err), &mapped) || mapped.Status != 400 || mapped.Code != "invalid_request" || mapped.Message != tc.message {
				t.Fatalf("validation = %#v; want 400 invalid_request %q", mapped, tc.message)
			}
		})
	}
}

func TestTestManagementRunMissingReferencesRemainBadRequest(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	service := services.NewTestRunService(tdb.GetDatabase())
	missing := 999999
	for _, tc := range []struct {
		name    string
		request services.TestRunCreateRequest
		message string
	}{
		{"set", services.TestRunCreateRequest{SetID: missing}, "test set not found in workspace"},
		{"template", services.TestRunCreateRequest{TemplateID: missing}, "test run template not found in workspace"},
		{"assignee", services.TestRunCreateRequest{AssigneeID: &missing}, "assignee not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.Create(1, tc.request)
			var mapped *Error
			if !errors.As(testManagementError(err), &mapped) || mapped.Status != 400 || mapped.Code != "invalid_request" || mapped.Message != tc.message {
				t.Fatalf("validation = %#v; want 400 invalid_request %q", mapped, tc.message)
			}
		})
	}
}
