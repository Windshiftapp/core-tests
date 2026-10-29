//go:build test

package v2

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/services"
)

// Error classification must be type-driven: only typed client errors map to
// 4xx. An untyped error whose message happens to contain a sniffed phrase
// must stay an internal error, which was not true when mappers matched on
// substrings of err.Error().
func TestLinkErrorClassifiesByTypeNotMessage(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		status      int
		code        string
		message     string
		internalMsg string
	}{
		{
			name:    "typed invalid request",
			err:     &services.InvalidRequestError{Message: "ids must contain between 1 and 500 unique values"},
			status:  http.StatusBadRequest,
			code:    "invalid_request",
			message: "ids must contain between 1 and 500 unique values",
		},
		{
			name:    "wrapped typed invalid request",
			err:     fmt.Errorf("prepare link: %w", &services.InvalidRequestError{Message: "custom field not found"}),
			status:  http.StatusBadRequest,
			code:    "invalid_request",
			message: "prepare link: custom field not found",
		},
		{
			name:        "untyped error containing sniffed phrase stays internal",
			err:         errors.New("database connection is required for this operation"),
			status:      http.StatusInternalServerError,
			code:        "internal_error",
			internalMsg: "database connection is required for this operation",
		},
		{
			name:        "untyped error mentioning custom field stays internal",
			err:         errors.New("query custom field definitions: sql: connection refused"),
			status:      http.StatusInternalServerError,
			code:        "internal_error",
			internalMsg: "query custom field definitions: sql: connection refused",
		},
		{
			name:    "self reference sentinel",
			err:     services.ErrLinkSelfReference,
			status:  http.StatusBadRequest,
			code:    "invalid_request",
			message: services.ErrLinkSelfReference.Error(),
		},
		{
			name:    "duplicate link conflict",
			err:     services.ErrLinkExists,
			status:  http.StatusConflict,
			code:    "conflict",
			message: "Link already exists",
		},
		{
			name:    "missing link collapses to not found",
			err:     services.ErrLinkNotFound,
			status:  http.StatusNotFound,
			code:    "not_found",
			message: "Link was not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapped := linkError(tt.err)
			apiErr, ok := mapped.(*Error)
			if !ok {
				t.Fatalf("linkError(%v) = %T, want *v2.Error", tt.err, mapped)
			}
			if apiErr.Status != tt.status {
				t.Fatalf("status = %d, want %d", apiErr.Status, tt.status)
			}
			if apiErr.Code != tt.code {
				t.Fatalf("code = %q, want %q", apiErr.Code, tt.code)
			}
			if tt.status == http.StatusInternalServerError {
				if apiErr.Message != "An unexpected error occurred" {
					t.Fatalf("internal message = %q, want generic message", apiErr.Message)
				}
				if apiErr.Error() != tt.internalMsg {
					t.Fatalf("cause = %q, want %q", apiErr.Error(), tt.internalMsg)
				}
			} else if apiErr.Message != tt.message {
				t.Fatalf("message = %q, want %q", apiErr.Message, tt.message)
			}
		})
	}
}

func TestActionErrorClassifiesByTypeNotMessage(t *testing.T) {
	invalid := &services.InvalidRequestError{Message: "allowed_role_ids must contain positive role IDs"}
	mapped := actionError(invalid)
	apiErr, ok := mapped.(*Error)
	if !ok {
		t.Fatalf("actionError(%v) = %T, want *v2.Error", invalid, mapped)
	}
	if apiErr.Status != http.StatusBadRequest || apiErr.Code != "invalid_request" {
		t.Fatalf("typed invalid request = %d/%q, want 400/invalid_request", apiErr.Status, apiErr.Code)
	}
	if apiErr.Message != invalid.Message {
		t.Fatalf("message = %q, want %q", apiErr.Message, invalid.Message)
	}

	internal := actionError(errors.New("allowed_role_ids: statement cache is not allowed to be nil"))
	apiErr, ok = internal.(*Error)
	if !ok {
		t.Fatalf("actionError(internal) = %T, want *v2.Error", internal)
	}
	if apiErr.Status != http.StatusInternalServerError {
		t.Fatalf("untyped error containing allowed_role_ids = %d, want 500", apiErr.Status)
	}
}
