//go:build test

package v2

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/services"
)

func TestAssetErrorPreservesInfrastructureFailure(t *testing.T) {
	failure := errors.New("database unavailable with private connection details")
	for _, tc := range []struct {
		name          string
		err           error
		status        int
		code, message string
	}{
		{"database", failure, 500, "internal_error", "An unexpected error occurred"},
		{"validation", &services.AssetValidationError{Msg: "asset_type_id is required"}, 400, "invalid_request", "asset_type_id is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			Adapt(func(http.ResponseWriter, *http.Request) error { return assetError(tc.err) }).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets", nil))
			var body errorBody
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != tc.status || body.Error.Code != tc.code || body.Error.Message != tc.message {
				t.Fatalf("response = %d %+v", recorder.Code, body)
			}
		})
	}
}
