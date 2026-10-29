//go:build test

package v2

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/services"
)

type labelFixture struct{ fakeCatalogs }

func (*labelFixture) Create(actor services.AuditActor, name, color string) (*models.Label, error) {
	return &models.Label{
		ID: 9, Name: name, Color: color,
		CreatedAt: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
	}, nil
}
func (*labelFixture) Update(services.AuditActor, int, services.LabelUpdate) (*models.Label, error) {
	return &models.Label{}, nil
}
func (*labelFixture) Delete(services.AuditActor, int) error { return nil }
func (*labelFixture) ListForItem(int) ([]models.Label, error) {
	return []models.Label{{ID: 9, Name: "Backend", Color: "#123456"}}, nil
}
func (*labelFixture) SetForItem(int, []int) ([]models.Label, error) {
	return []models.Label{}, nil
}
func (*labelFixture) AddToItem(int, int) ([]models.Label, error) {
	return []models.Label{}, nil
}
func (*labelFixture) RemoveFromItem(int, int) error { return nil }
func (*labelFixture) FindByID(id int) (*models.Item, error) {
	return &models.Item{ID: id, WorkspaceID: 7}, nil
}
func (*labelFixture) CanViewWorkspace(int, int) (bool, error)  { return true, nil }
func (*labelFixture) CanEditWorkspace(int, int) (bool, error)  { return true, nil }
func (*labelFixture) CanAdminWorkspace(int, int) (bool, error) { return true, nil }

func TestLabelCreateContractMatchesAcrossMounts(t *testing.T) {
	fixture := &labelFixture{}
	mux, _ := newTestMuxWithCatalog(t, true, fixture)
	paths := []string{"/api/v2/workspaces/7/labels", "/rest/api/v2/workspaces/7/labels"}
	var bodies []string
	for index, path := range paths {
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"name":"Backend","color":"#123456"}`))
		request.Header.Set("Content-Type", "application/json")
		if index == 0 {
			request.Header.Set("X-Test-Session", "valid")
		} else {
			request.Header.Set("Authorization", "Bearer crw_valid")
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("%s status = %d; body=%s", path, response.Code, response.Body)
		}
		bodies = append(bodies, response.Body.String())
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("mount bodies differ:\nsession: %s\nbearer: %s", bodies[0], bodies[1])
	}
}

func TestLabelPatchRejectsExplicitNull(t *testing.T) {
	fixture := &labelFixture{}
	mux, _ := newTestMuxWithCatalog(t, true, fixture)
	request := httptest.NewRequest(http.MethodPatch, "/api/v2/workspaces/7/labels/9", bytes.NewBufferString(`{"name":null}`))
	request.Header.Set("Content-Type", "application/merge-patch+json")
	request.Header.Set("X-Test-Session", "valid")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
}
