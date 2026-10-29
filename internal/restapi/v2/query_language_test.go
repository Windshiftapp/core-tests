//go:build test

package v2

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
)

type queryLanguageCatalogFixture struct{ fakeCatalogs }

func (queryLanguageCatalogFixture) ListCustomFields() ([]services.CustomFieldResult, error) {
	return []services.CustomFieldResult{
		{ID: 5, Name: "Impact", FieldType: "select", Options: `["High","Low"]`},
	}, nil
}

func TestQueryLanguageCompletionCatalogIncludesBuiltinsAndCustomFields(t *testing.T) {
	request := httptest.NewRequest("GET", "/query-language/catalog", nil)
	catalog, err := queryLanguageCompletionCatalog(queryLanguageCatalogFixture{})(request)
	if err != nil {
		t.Fatalf("query language catalog: %v", err)
	}

	milestoneIndex := slices.IndexFunc(catalog.Fields, func(field qlCompletionFieldDTO) bool {
		return field.Name == "milestone"
	})
	if milestoneIndex < 0 {
		t.Fatal("milestone completion field is missing")
	}
	milestone := catalog.Fields[milestoneIndex]
	if milestone.ValueHelp == nil || milestone.ValueHelp.Source != "milestones" {
		t.Fatalf("milestone value help = %#v", milestone.ValueHelp)
	}
	labelIndex := slices.IndexFunc(catalog.Fields, func(field qlCompletionFieldDTO) bool {
		return field.Name == "label"
	})
	if labelIndex < 0 || catalog.Fields[labelIndex].ValueHelp == nil || catalog.Fields[labelIndex].ValueHelp.Source != "labels" {
		t.Fatalf("label value help = %#v", catalog.Fields[labelIndex].ValueHelp)
	}
	workspaceKeyIndex := slices.IndexFunc(catalog.Fields, func(field qlCompletionFieldDTO) bool {
		return field.Name == "workspaceKey"
	})
	if workspaceKeyIndex < 0 || catalog.Fields[workspaceKeyIndex].ValueHelp.ValueField != "key" {
		t.Fatalf("workspaceKey value help = %#v", catalog.Fields[workspaceKeyIndex].ValueHelp)
	}

	customIndex := slices.IndexFunc(catalog.Fields, func(field qlCompletionFieldDTO) bool {
		return field.Name == "cfid_5"
	})
	if customIndex < 0 {
		t.Fatal("custom completion field is missing")
	}
	custom := catalog.Fields[customIndex]
	if custom.Label != "Impact" || len(custom.Values) != 2 || custom.Values[0].Label != "High" {
		t.Fatalf("custom completion field = %#v", custom)
	}
	if custom.FieldType != "select" {
		t.Fatalf("custom field type = %q", custom.FieldType)
	}
	if !slices.Equal(catalog.LogicalOperators, []string{"AND", "OR"}) {
		t.Fatalf("logical operators = %v", catalog.LogicalOperators)
	}
}

type qlValueCatalogFixture struct{ fakeCatalogs }

func (qlValueCatalogFixture) ListWorkspaces(_ int, page services.CatalogPageParams) ([]models.Workspace, int, error) {
	return []models.Workspace{{ID: 3, Name: "Visible", Key: "VIS"}}, 1, nil
}

func (qlValueCatalogFixture) ListLabels(_ int, workspaceID int) ([]models.Label, error) {
	if workspaceID != 3 {
		return nil, repository.ErrNotFound
	}
	return []models.Label{{ID: 7, Name: "Frontend"}, {ID: 8, Name: "Backend"}}, nil
}

type qlValuePlanningFixture struct {
	fakePlanning
	workspaceIDs  []int
	includeGlobal bool
}

func (f *qlValuePlanningFixture) ListMilestones(_ int, params services.MilestoneListParams) ([]services.MilestoneResult, int, error) {
	f.workspaceIDs = slices.Clone(params.WorkspaceIDs)
	f.includeGlobal = params.IncludeGlobal
	return []services.MilestoneResult{
		{ID: 11, Name: "Public launch", IsGlobal: true},
		{ID: 12, Name: "Visible launch", WorkspaceID: intPointer(3), WorkspaceName: "Visible"},
	}, 2, nil
}

type qlValueTimeProjectsFixture struct{ fakeTimeProjects }

func (qlValueTimeProjectsFixture) List(int, string) ([]repository.TimeProjectDetail, error) {
	return []repository.TimeProjectDetail{{ID: 21, Name: "Customer launch"}}, nil
}

type qlValueLocalizerFixture struct {
	locale     string
	objectType string
}

func (f *qlValueLocalizerFixture) LocalizeResponse(_ context.Context, locale, objectType string, value any) error {
	f.locale = locale
	f.objectType = objectType
	values := value.(*[]qlCompletionValueDTO)
	(*values)[0].DisplayName = "Offen"
	return nil
}

func TestQueryLanguageValueLoaderUsesVisibleWorkspaceScope(t *testing.T) {
	planning := &qlValuePlanningFixture{}
	loader := queryLanguageValueLoader{
		configuration: qlValueCatalogFixture{},
		statuses:      qlValueCatalogFixture{},
		catalog:       qlValueCatalogFixture{},
		planning:      planning,
		timeProjects:  qlValueTimeProjectsFixture{},
	}

	values, err := loader.Load(context.Background(), 42, "milestones", "name")
	if err != nil {
		t.Fatalf("load milestone values: %v", err)
	}
	if !slices.Equal(planning.workspaceIDs, []int{3}) {
		t.Fatalf("workspace ids = %v, want [3]", planning.workspaceIDs)
	}
	if !planning.includeGlobal {
		t.Fatal("global milestones were excluded")
	}
	if len(values) != 2 || values[1].Value != "Visible launch" || values[1].Label != "Visible launch" {
		t.Fatalf("milestone values = %#v", values)
	}
}

func TestQueryLanguageValueLoaderLoadsLabelsThroughVisibleWorkspace(t *testing.T) {
	loader := queryLanguageValueLoader{
		configuration: qlValueCatalogFixture{},
		statuses:      qlValueCatalogFixture{},
		catalog:       qlValueCatalogFixture{},
		planning:      &qlValuePlanningFixture{},
		timeProjects:  qlValueTimeProjectsFixture{},
	}

	values, err := loader.Load(context.Background(), 42, "labels", "name")
	if err != nil {
		t.Fatalf("load label values: %v", err)
	}
	if len(values) != 2 || values[0].Value != "Frontend" || values[0].Label != "Frontend" {
		t.Fatalf("label values = %#v", values)
	}
}

func TestLocalizeQueryLanguageValuesUsesVisibleCatalogName(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v2/query-language/values", nil)
	request.Header.Set("Accept-Language", "de")
	localizer := &qlValueLocalizerFixture{}
	values := []qlCompletionValueDTO{qlCompletionValue(1, "Open", "", "name", "Open")}

	if err := localizeQLCompletionValues(request, localizer, "statuses", values); err != nil {
		t.Fatalf("localize values: %v", err)
	}
	if localizer.locale != "de" || localizer.objectType != "status" {
		t.Fatalf("localization request = %q, %q", localizer.locale, localizer.objectType)
	}
	if values[0].Value != "Open" || values[0].Label != "Offen" {
		t.Fatalf("localized value = %#v", values[0])
	}
}

func TestQueryLanguageValuesSearchesCentralizedCatalog(t *testing.T) {
	mux, _ := newTestMuxWithCatalog(t, true, qlValueCatalogFixture{})
	request := httptest.NewRequest(http.MethodGet, "/api/v2/query-language/values?source=labels&value_field=name&q=front", nil)
	request.Header.Set("X-Test-Session", "valid")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
	var body struct {
		Data []qlCompletionValueDTO `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].Value != "Frontend" || body.Data[0].Label != "Frontend" {
		t.Fatalf("values = %#v", body.Data)
	}
}

func intPointer(value int) *int { return &value }

type qlValueTeamFixture struct{}

func (qlValueTeamFixture) List() ([]models.Team, error) {
	return []models.Team{{ID: 4, Name: "Platform"}, {ID: 9, Name: "Payments"}}, nil
}

func TestQueryLanguageCompletionCatalogExposesTeamValueHelp(t *testing.T) {
	request := httptest.NewRequest("GET", "/query-language/catalog", nil)
	catalog, err := queryLanguageCompletionCatalog(queryLanguageCatalogFixture{})(request)
	if err != nil {
		t.Fatalf("query language catalog: %v", err)
	}
	index := slices.IndexFunc(catalog.Fields, func(field qlCompletionFieldDTO) bool {
		return field.Name == "team"
	})
	if index < 0 {
		t.Fatal("team completion field is missing")
	}
	team := catalog.Fields[index]
	if team.ValueHelp == nil || team.ValueHelp.Source != "teams" || team.ValueHelp.ValueField != "id" {
		t.Fatalf("team value help = %#v, want source teams and value_field id", team.ValueHelp)
	}
}

func TestQueryLanguageValueLoaderLoadsTeams(t *testing.T) {
	loader := queryLanguageValueLoader{teams: qlValueTeamFixture{}}

	values, err := loader.Load(context.Background(), 42, "teams", "id")
	if err != nil {
		t.Fatalf("load team values: %v", err)
	}
	if len(values) != 2 {
		t.Fatalf("team values = %#v, want 2", values)
	}
	if values[0].Value != 4 || values[0].Label != "Platform" {
		t.Fatalf("team value[0] = %#v, want id 4 / Platform", values[0])
	}
}

func TestQueryLanguageValueLoaderTeamsSourceIsOptional(t *testing.T) {
	loader := queryLanguageValueLoader{}

	values, err := loader.Load(context.Background(), 42, "teams", "id")
	if err != nil {
		t.Fatalf("load team values without a reader: %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("team values without a reader = %#v, want empty", values)
	}
}

type qlValueAssignableUsersFixture struct {
	qlValueCatalogFixture
	listUsersCalled bool
}

func (f *qlValueAssignableUsersFixture) ListWorkspaces(_ int, _ services.CatalogPageParams) ([]models.Workspace, int, error) {
	return []models.Workspace{
		{ID: 3, Name: "Visible", Key: "VIS"},
		{ID: 4, Name: "Second", Key: "SEC"},
	}, 2, nil
}

func (f *qlValueAssignableUsersFixture) ListAssignableUsers(_ context.Context, _ int, workspaceID int) ([]models.User, error) {
	switch workspaceID {
	case 3:
		return []models.User{
			{ID: 31, Username: "bravo", FullName: "Bravo One"},
			{ID: 32, Username: "alpha", FullName: "Alpha Two"},
		}, nil
	case 4:
		return []models.User{
			{ID: 32, Username: "alpha", FullName: "Alpha Two"},
			{ID: 33, Username: "charlie", FullName: ""},
		}, nil
	default:
		return nil, repository.ErrNotFound
	}
}

// ListUsers is the user.list-gated directory read; the loader must not use it.
func (f *qlValueAssignableUsersFixture) ListUsers(int, services.CatalogPageParams) ([]models.User, int, error) {
	f.listUsersCalled = true
	return nil, 0, errors.New("directory read must not be used for user completion values")
}

func TestQueryLanguageValueLoaderUnionsAssignableUsersAcrossVisibleWorkspaces(t *testing.T) {
	catalog := &qlValueAssignableUsersFixture{}
	loader := queryLanguageValueLoader{catalog: catalog}

	values, err := loader.Load(context.Background(), 42, "users", "id")
	if err != nil {
		t.Fatalf("load user values: %v", err)
	}
	if catalog.listUsersCalled {
		t.Fatal("loader used the user.list-gated directory read")
	}

	// Deduped across workspaces, sorted by label (username fallback when the
	// full name is empty), then ID.
	wantIDs := []int{32, 31, 33}
	wantLabels := []string{"Alpha Two", "Bravo One", "charlie"}
	wantDescriptions := []string{"alpha", "bravo", "charlie"}
	if len(values) != len(wantIDs) {
		t.Fatalf("user values = %#v, want %d entries", values, len(wantIDs))
	}
	for i, value := range values {
		id, ok := value.Value.(int)
		if !ok || id != wantIDs[i] {
			t.Fatalf("user value[%d] = %#v, want id %d", i, value.Value, wantIDs[i])
		}
		if value.Label != wantLabels[i] {
			t.Fatalf("user value[%d] label = %q, want %q", i, value.Label, wantLabels[i])
		}
		// The description carries the unique username so value pickers can
		// disambiguate users whose display names are identical.
		if value.Description != wantDescriptions[i] {
			t.Fatalf("user value[%d] description = %q, want %q", i, value.Description, wantDescriptions[i])
		}
	}
}

func TestQueryLanguageValueLoaderUserValuesWithoutVisibleWorkspaces(t *testing.T) {
	loader := queryLanguageValueLoader{catalog: qlValueCatalogFixture{}}

	// qlValueCatalogFixture.ListWorkspaces returns workspace 3 with no
	// assignable users, so the union is empty rather than an error.
	values, err := loader.Load(context.Background(), 42, "users", "id")
	if err != nil {
		t.Fatalf("load user values: %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("user values = %#v, want empty", values)
	}
}
