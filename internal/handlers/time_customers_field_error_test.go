//go:build test

package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/customfields"
	"windshift/internal/validation"
)

// TestRespondCustomerFieldError locks the mapping that a validation failure
// must write a response: known custom-field errors are 400s, anything else is
// a 500 so an unexpected failure never lets an unvalidated write proceed.
func TestRespondCustomerFieldError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "validation error is a 400",
			err:        &validation.ValidationError{Field: "custom_field_values.1", Message: "bad value"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "ambiguous name is a 400",
			err:        &customfields.AmbiguousNameError{Name: "Priority"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unexpected error is a 500",
			err:        errors.New("database exploded"),
			wantStatus: http.StatusInternalServerError,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/customer-organisations", nil)
			respondCustomerFieldError(recorder, request, tc.err)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.wantStatus)
			}
		})
	}
}
