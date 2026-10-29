//go:build test

package v2

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/services"
)

type catalogFixture struct{ fakeCatalogs }

func (catalogFixture) ListCustomFieldsWithMeta() ([]services.CustomFieldResult, services.CustomFieldCatalogMeta, error) {
	return []services.CustomFieldResult{{
		ID: 5, Name: "Impact", FieldType: "select", Options: `["High"]`,
		SystemDefault: true, AppliesToPortalCustomers: true,
		AssetTypeUsages: []services.CustomFieldAssetUsage{{AssetTypeName: "Server", SetName: "CMDB"}},
		Indexed:         models.CustomFieldIndexInfo{Items: true},
	}}, services.CustomFieldCatalogMeta{IndexCounts: map[string]services.CustomFieldIndexCount{
		"items": {Current: 1, Max: 30}, "assets": {Current: 0, Max: 30},
	}}, nil
}

func (catalogFixture) ListWorkspaceStatuses(_ int, workspaceID int, itemTypeID *int) ([]services.StatusResult, error) {
	if workspaceID != 7 || itemTypeID == nil || *itemTypeID != 9 {
		return nil, errors.New("unexpected workspace status arguments")
	}
	return []services.StatusResult{{
		ID: 4, BuiltinKey: "open", Name: "Open", Description: "Ready",
		CategoryID: 2, CategoryBuiltinKey: "todo", CategoryName: "To do",
		CategoryDescription: "Not started", CategoryColor: "#123456",
		CategoryIsDefault: true, IsDefault: true,
	}}, nil
}

func (catalogFixture) ListUsers(_ int, page services.CatalogPageParams) ([]models.User, int, error) {
	if page.Limit != 1 || page.Offset != 0 || page.Sort != "username" || page.Desc {
		return nil, 0, errors.New("unexpected user page")
	}
	return []models.User{{
		ID: 8, Email: "must-not-leak@example.test", Username: "grace",
		FirstName: "Grace", LastName: "Hopper", FullName: "Grace Hopper",
		IsActive: true, CreatedAt: time.Date(2026, 9, 2, 10, 0, 0, 0, time.FixedZone("test", 2*60*60)),
	}}, 2, nil
}

func (catalogFixture) ListItemTemplates(_ int, workspaceID int, itemTypeID *int) (services.ItemTemplateListResult, error) {
	if workspaceID != 7 || itemTypeID == nil || *itemTypeID != 9 {
		return services.ItemTemplateListResult{}, errors.New("unexpected template arguments")
	}
	mandatory := 12
	return services.ItemTemplateListResult{
		Items:               []models.ItemTemplate{{ID: 12, WorkspaceID: 7, Name: "Bug", ItemTypeIDs: nil}},
		MandatoryTemplateID: &mandatory,
	}, nil
}

func TestCatalogPathParametersAndDTOsMatchAcrossMounts(t *testing.T) {
	mux, _ := newTestMuxWithCatalog(t, true, catalogFixture{})
	paths := []string{
		"/api/v2/workspaces/7/statuses?item_type_id=9",
		"/rest/api/v2/workspaces/7/statuses?item_type_id=9",
	}
	var bodies []string
	for i, path := range paths {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if i == 0 {
			request.Header.Set("X-Test-Session", "valid")
		} else {
			request.Header.Set("Authorization", "Bearer crw_valid")
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d; body=%s", path, response.Code, response.Body)
		}
		bodies = append(bodies, response.Body.String())
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("mount bodies differ:\nsession: %s\nbearer: %s", bodies[0], bodies[1])
	}
	var body struct {
		Data []statusDTO `json:"data"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].Category.Description != "Not started" || !body.Data[0].Category.IsDefault {
		t.Fatalf("status DTO = %#v", body.Data)
	}
}

func TestCatalogPagesUseCanonicalEnvelopeAndSafeUserDTO(t *testing.T) {
	mux, _ := newTestMuxWithCatalog(t, true, catalogFixture{})
	request := httptest.NewRequest(http.MethodGet, "/api/v2/users?page_size=1", nil)
	request.Header.Set("X-Test-Session", "valid")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
	var body struct {
		Data       []map[string]any   `json:"data"`
		Pagination paginationDocument `json:"pagination"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Pagination != (paginationDocument{Page: 1, PageSize: 1, TotalItems: 2, TotalPages: 2}) {
		t.Fatalf("pagination = %#v", body.Pagination)
	}
	if len(body.Data) != 1 || body.Data[0]["username"] != "grace" {
		t.Fatalf("data = %#v", body.Data)
	}
	for _, forbidden := range []string{"email", "timezone", "language"} {
		if _, present := body.Data[0][forbidden]; present {
			t.Fatalf("safe user DTO contains %q: %#v", forbidden, body.Data[0])
		}
	}
}

func TestTemplateListKeepsMandatoryIDBesideData(t *testing.T) {
	mux, _ := newTestMuxWithCatalog(t, true, catalogFixture{})
	request := httptest.NewRequest(http.MethodGet, "/rest/api/v2/workspaces/7/item-templates?item_type_id=9", nil)
	request.Header.Set("Authorization", "Bearer crw_valid")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
	var body itemTemplateListDocument
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.MandatoryTemplateID == nil || *body.MandatoryTemplateID != 12 || len(body.Data) != 1 {
		t.Fatalf("template document = %#v", body)
	}
	if !reflect.DeepEqual(body.Data[0].ItemTypeIDs, []int{}) {
		t.Fatalf("item_type_ids = %#v", body.Data[0].ItemTypeIDs)
	}
}

type legacyOptionsReader struct{ fakeCatalogs }

func (legacyOptionsReader) GetCustomField(int) (*services.CustomFieldResult, error) {
	return &services.CustomFieldResult{ID: 3, FieldType: "select", Options: `["One","Two"]`}, nil
}

func TestLegacyCustomFieldOptionsAreNormalized(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/custom-fields/3", nil)
	request.SetPathValue("custom_field_id", "3")
	got, err := getCustomField(legacyOptionsReader{})(request)
	if err != nil {
		t.Fatalf("get custom field: %v", err)
	}
	want := map[string]any{
		"next_id": float64(3),
		"items": []any{
			map[string]any{"id": float64(1), "label": "One"},
			map[string]any{"id": float64(2), "label": "Two"},
		},
	}
	if !reflect.DeepEqual(got.Options, want) {
		t.Fatalf("options = %#v, want %#v", got.Options, want)
	}
}

func TestCustomFieldCatalogIncludesCanonicalMetadata(t *testing.T) {
	mux, _ := newTestMuxWithCatalog(t, true, catalogFixture{})
	request := httptest.NewRequest(http.MethodGet, "/api/v2/custom-fields", nil)
	request.Header.Set("X-Test-Session", "valid")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
	var body struct {
		Data []customFieldDTO                `json:"data"`
		Meta services.CustomFieldCatalogMeta `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Data) != 1 || !body.Data[0].SystemDefault || !body.Data[0].Indexed.Items {
		t.Fatalf("data = %#v", body.Data)
	}
	if body.Meta.IndexCounts["items"] != (services.CustomFieldIndexCount{Current: 1, Max: 30}) {
		t.Fatalf("meta = %#v", body.Meta)
	}
}

// globalCatalogFixture returns one row per required catalog so the aggregate
// can be compared field-by-field with the individual list operations.
type globalCatalogFixture struct{ catalogFixture }

func (globalCatalogFixture) ListStatuses() ([]services.StatusResult, error) {
	return []services.StatusResult{{
		ID: 4, BuiltinKey: "open", Name: "Open", Description: "Ready",
		CategoryID: 2, CategoryBuiltinKey: "todo", CategoryName: "To do",
		CategoryDescription: "Not started", CategoryColor: "#123456",
		CategoryIsDefault: true, IsDefault: true,
	}}, nil
}

func (globalCatalogFixture) ListCategories() ([]services.StatusCategoryResult, error) {
	return []services.StatusCategoryResult{{
		ID: 2, BuiltinKey: "todo", Name: "To do", Description: "Not started",
		Color: "#123456", IsDefault: true,
	}}, nil
}

func (globalCatalogFixture) ListItemTypes() ([]services.ItemTypeResult, error) {
	return []services.ItemTypeResult{{
		ID: 3, BuiltinKey: "task", Name: "Task", Icon: "check", Color: "#00ff00",
		HierarchyLevel: 1, SortOrder: 2, IsDefault: true,
	}}, nil
}

func (globalCatalogFixture) ListPriorities() ([]services.PriorityResult, error) {
	return []services.PriorityResult{{
		ID: 5, BuiltinKey: "high", Name: "High", Icon: "arrow-up", Color: "#ff0000",
		SortOrder: 1, IsDefault: true,
	}}, nil
}

func TestGlobalCatalogBootstrapAggregatesEveryCatalog(t *testing.T) {
	mux, _ := newTestMuxWithCatalog(t, true, globalCatalogFixture{})
	request := httptest.NewRequest(http.MethodGet, "/api/v2/catalogs/global-bootstrap", nil)
	request.Header.Set("X-Test-Session", "valid")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
	var body struct {
		Data globalBootstrapDTO `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Data.ItemTypes) != 1 || body.Data.ItemTypes[0].Name != "Task" {
		t.Fatalf("item types = %#v", body.Data.ItemTypes)
	}
	if len(body.Data.Statuses) != 1 || body.Data.Statuses[0].Category.Name != "To do" {
		t.Fatalf("statuses = %#v", body.Data.Statuses)
	}
	if len(body.Data.StatusCategories) != 1 || body.Data.StatusCategories[0].Name != "To do" {
		t.Fatalf("status categories = %#v", body.Data.StatusCategories)
	}
	if len(body.Data.Priorities) != 1 || body.Data.Priorities[0].Name != "High" {
		t.Fatalf("priorities = %#v", body.Data.Priorities)
	}
	if len(body.Data.CustomFieldDefinitions) != 1 || body.Data.CustomFieldDefinitions[0].Name != "Impact" {
		t.Fatalf("custom fields = %#v", body.Data.CustomFieldDefinitions)
	}
	if body.Data.CustomFieldsError != nil {
		t.Fatalf("custom fields error = %v", *body.Data.CustomFieldsError)
	}
}

func TestGlobalCatalogBootstrapRequiresCoreScopes(t *testing.T) {
	mux, _ := newTestMuxWithCatalog(t, false, globalCatalogFixture{})
	request := httptest.NewRequest(http.MethodGet, "/rest/api/v2/catalogs/global-bootstrap", nil)
	request.Header.Set("Authorization", "Bearer crw_valid")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
}

func TestGlobalCatalogBootstrapKeepsCustomFieldsOptionalForBearer(t *testing.T) {
	coreScopes := []string{"item-types:read", "statuses:read", "priorities:read"}
	tokens := fakeTokens{user: &models.User{ID: 42}, token: &models.APIToken{ID: 7}, allow: true, wantScopes: coreScopes}
	mux, _ := newTestMuxWithCatalog(t, true, globalCatalogFixture{}, tokens)
	request := httptest.NewRequest(http.MethodGet, "/rest/api/v2/catalogs/global-bootstrap", nil)
	request.Header.Set("Authorization", "Bearer crw_valid")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
	var body struct {
		Data globalBootstrapDTO `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Data.CustomFieldDefinitions != nil {
		t.Fatalf("custom fields = %#v, want null", body.Data.CustomFieldDefinitions)
	}
	if body.Data.CustomFieldsError == nil || *body.Data.CustomFieldsError != "insufficient_permission" {
		t.Fatalf("custom fields error = %v", body.Data.CustomFieldsError)
	}
	if len(body.Data.ItemTypes) != 1 || len(body.Data.Priorities) != 1 {
		t.Fatalf("required catalogs missing: %#v", body.Data)
	}
}

func TestGlobalCatalogBootstrapKeepsCoreCatalogsWhenCustomFieldsFail(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v2/catalogs/global-bootstrap", nil)
	result, err := globalCatalogBootstrap(Deps{
		Configuration: failingCustomFieldFixture{},
		Statuses:      failingCustomFieldFixture{},
	})(request)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if result.CustomFieldDefinitions != nil {
		t.Fatalf("custom fields = %#v, want nil", result.CustomFieldDefinitions)
	}
	if result.CustomFieldsError == nil || *result.CustomFieldsError != "custom_fields_unavailable" {
		t.Fatalf("custom fields error = %v", result.CustomFieldsError)
	}
	if len(result.ItemTypes) != 1 || len(result.Priorities) != 1 || len(result.Statuses) != 1 {
		t.Fatalf("required catalogs missing: %#v", result)
	}
}

type failingCustomFieldFixture struct{ globalCatalogFixture }

func (failingCustomFieldFixture) ListCustomFieldsWithMeta() ([]services.CustomFieldResult, services.CustomFieldCatalogMeta, error) {
	return nil, services.CustomFieldCatalogMeta{}, errors.New("custom fields unavailable")
}

type recordingCatalogLocalizer struct{ types []string }

func (l *recordingCatalogLocalizer) LocalizeResponse(_ context.Context, _, objectType string, _ any) error {
	l.types = append(l.types, objectType)
	return nil
}

func TestGlobalCatalogBootstrapLocalizesEveryCatalog(t *testing.T) {
	localizer := &recordingCatalogLocalizer{}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/catalogs/global-bootstrap", nil)
	result, err := globalCatalogBootstrap(Deps{
		Configuration:      globalCatalogFixture{},
		Statuses:           globalCatalogFixture{},
		ObjectTranslations: localizer,
	})(request)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !slices.Equal(localizer.types, []string{"item_type", "status", "status_category", "priority"}) {
		t.Fatalf("localized object types = %v", localizer.types)
	}
	if len(result.CustomFieldDefinitions) != 1 {
		t.Fatalf("custom fields = %#v", result.CustomFieldDefinitions)
	}
}

func TestGlobalCatalogBootstrapKeepsEmptyCatalogsDistinctFromFailures(t *testing.T) {
	mux, _ := newTestMuxWithCatalog(t, true, fakeCatalogs{})
	request := httptest.NewRequest(http.MethodGet, "/api/v2/catalogs/global-bootstrap", nil)
	request.Header.Set("X-Test-Session", "valid")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	for _, field := range []string{"item_types", "statuses", "status_categories", "priorities", "custom_field_definitions"} {
		if string(body.Data[field]) != "[]" {
			t.Errorf("%s = %s, want an empty array", field, body.Data[field])
		}
	}
	if string(body.Data["custom_fields_error"]) != "null" {
		t.Errorf("custom_fields_error = %s, want null", body.Data["custom_fields_error"])
	}
}

func TestGlobalCatalogBootstrapFailsWhenRequiredCatalogFails(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v2/catalogs/global-bootstrap", nil)
	_, err := globalCatalogBootstrap(Deps{
		Configuration: failingItemTypeFixture{},
		Statuses:      failingItemTypeFixture{},
	})(request)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusInternalServerError {
		t.Fatalf("error = %v, want 500 internal_error", err)
	}
}

type failingItemTypeFixture struct{ globalCatalogFixture }

func (failingItemTypeFixture) ListItemTypes() ([]services.ItemTypeResult, error) {
	return nil, errors.New("item types unavailable")
}
