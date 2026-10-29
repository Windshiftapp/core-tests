//go:build test

package v2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/models"
)

type userDisplayBatchFixture struct {
	fakeCatalogs
	err     error
	gotIDs  []int
	userIDs []int
}

func (f *userDisplayBatchFixture) GetUserDisplayBatch(_ context.Context, _ int, ids []int) ([]models.User, error) {
	f.gotIDs = append([]int(nil), ids...)
	if f.err != nil {
		return nil, f.err
	}
	users := make([]models.User, 0, len(ids))
	for _, id := range ids {
		users = append(users, models.User{
			ID: id, Email: fmt.Sprintf("user-%d@example.test", id), Username: fmt.Sprintf("user-%d", id),
			FirstName: "First", LastName: fmt.Sprintf("Last%d", id), IsActive: true,
		})
	}
	return users, nil
}

func postUserDisplayBatch(t *testing.T, mux *http.ServeMux, body any, bearer bool) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	path := "/api/v2/users/display-batch"
	if bearer {
		path = "/rest/api/v2/users/display-batch"
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	if bearer {
		request.Header.Set("Authorization", "Bearer crw_valid")
	} else {
		request.Header.Set("X-Test-Session", "valid")
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func TestUserDisplayBatchValidatesRawIDBounds(t *testing.T) {
	fixture := &userDisplayBatchFixture{}
	mux, _ := newTestMuxWithCatalog(t, true, fixture)

	tooMany := make([]int, 201)
	for i := range tooMany {
		tooMany[i] = i + 1
	}
	cases := []struct {
		name string
		body any
	}{
		{name: "missing ids", body: map[string]any{}},
		{name: "empty ids", body: map[string]any{"ids": []int{}}},
		{name: "over 200 raw ids", body: map[string]any{"ids": tooMany}},
		{name: "zero id", body: map[string]any{"ids": []int{1, 0}}},
		{name: "negative id", body: map[string]any{"ids": []int{1, -2}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := postUserDisplayBatch(t, mux, tc.body, false)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", response.Code, response.Body)
			}
		})
	}
	if len(fixture.gotIDs) != 0 {
		t.Fatalf("invalid requests reached the catalog: %v", fixture.gotIDs)
	}
}

func TestUserDisplayBatchDeduplicatesInRequestOrder(t *testing.T) {
	fixture := &userDisplayBatchFixture{}
	mux, _ := newTestMuxWithCatalog(t, true, fixture)

	response := postUserDisplayBatch(t, mux, map[string]any{"ids": []int{3, 1, 3, 2, 1}}, false)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
	want := []int{3, 1, 2}
	if fmt.Sprint(fixture.gotIDs) != fmt.Sprint(want) {
		t.Fatalf("catalog ids = %v, want %v", fixture.gotIDs, want)
	}

	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Data) != 3 {
		t.Fatalf("data length = %d, want 3", len(body.Data))
	}
	for i, wantID := range want {
		if got := int(body.Data[i]["id"].(float64)); got != wantID {
			t.Fatalf("data[%d].id = %d, want %d", i, got, wantID)
		}
		if _, leaked := body.Data[i]["email"]; leaked {
			t.Fatalf("user summary leaked email: %#v", body.Data[i])
		}
	}
}

func TestUserDisplayBatchAcceptsTwoHundredRawIDs(t *testing.T) {
	fixture := &userDisplayBatchFixture{}
	mux, _ := newTestMuxWithCatalog(t, true, fixture)
	ids := make([]int, 200)
	for i := range ids {
		ids[i] = i + 1
	}

	response := postUserDisplayBatch(t, mux, map[string]any{"ids": ids}, false)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
	if len(fixture.gotIDs) != 200 {
		t.Fatalf("catalog ids = %d, want 200", len(fixture.gotIDs))
	}
}

func TestUserDisplayBatchOperationalFailureIsNotAnOmission(t *testing.T) {
	fixture := &userDisplayBatchFixture{err: errors.New("database unavailable")}
	mux, _ := newTestMuxWithCatalog(t, true, fixture)

	response := postUserDisplayBatch(t, mux, map[string]any{"ids": []int{1, 2}}, false)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", response.Code, response.Body)
	}
}

func TestUserDisplayBatchRequiresUsersReadBearerScope(t *testing.T) {
	fixture := &userDisplayBatchFixture{}
	mux, _ := newTestMuxWithCatalog(t, false, fixture)

	response := postUserDisplayBatch(t, mux, map[string]any{"ids": []int{1}}, true)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", response.Code, response.Body)
	}
	if len(fixture.gotIDs) != 0 {
		t.Fatalf("denied bearer reached the catalog: %v", fixture.gotIDs)
	}
}
