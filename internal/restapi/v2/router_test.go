//go:build test

package v2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	apispec "windshift/api"
	"windshift/internal/auth"
	"windshift/internal/contextkeys"
	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/objecttranslation"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/services/actioncatalog"
)

type fakeTokens struct {
	user       *models.User
	token      *models.APIToken
	allow      bool
	wantScopes []string
}

func (f fakeTokens) ValidateToken(raw string) (*models.User, *models.APIToken, error) {
	if raw != "crw_valid" {
		return nil, nil, errFakeToken
	}
	return f.user, f.token, nil
}

func (f fakeTokens) CheckTokenPermissions(_ *models.APIToken, scopes []string) bool {
	return f.allow && (f.wantScopes == nil || slices.Equal(scopes, f.wantScopes))
}

type fakeTokenError string

func (e fakeTokenError) Error() string { return string(e) }

const errFakeToken = fakeTokenError("invalid token")

type fakeUsers struct{ user *models.User }

func (f fakeUsers) GetByID(id int) (*models.User, error) {
	if f.user == nil || f.user.ID != id {
		return nil, errFakeToken
	}
	copy := *f.user
	return &copy, nil
}

type fakeCatalogs struct{}

func (fakeCatalogs) ListStatuses() ([]services.StatusResult, error) {
	return []services.StatusResult{}, nil
}
func (fakeCatalogs) GetStatus(int) (*services.StatusResult, error) { return nil, errFakeToken }
func (fakeCatalogs) ListCategories() ([]services.StatusCategoryResult, error) {
	return []services.StatusCategoryResult{}, nil
}
func (fakeCatalogs) GetCategory(int) (*services.StatusCategoryResult, error) {
	return nil, errFakeToken
}
func (fakeCatalogs) List() ([]services.WorkflowResult, error) {
	return []services.WorkflowResult{}, nil
}
func (fakeCatalogs) GetByID(int) (*services.WorkflowResult, error) { return nil, errFakeToken }
func (fakeCatalogs) GetTransitions(int) ([]services.WorkflowTransitionResult, error) {
	return []services.WorkflowTransitionResult{}, nil
}
func (fakeCatalogs) ListItemTypes() ([]services.ItemTypeResult, error) {
	return []services.ItemTypeResult{}, nil
}
func (fakeCatalogs) GetItemType(int) (*services.ItemTypeResult, error) { return nil, errFakeToken }
func (fakeCatalogs) ListPriorities() ([]services.PriorityResult, error) {
	return []services.PriorityResult{}, nil
}
func (fakeCatalogs) GetPriority(int) (*services.PriorityResult, error) { return nil, errFakeToken }
func (fakeCatalogs) ListCustomFields() ([]services.CustomFieldResult, error) {
	return []services.CustomFieldResult{}, nil
}
func (fakeCatalogs) ListCustomFieldsWithMeta() ([]services.CustomFieldResult, services.CustomFieldCatalogMeta, error) {
	return []services.CustomFieldResult{}, services.CustomFieldCatalogMeta{}, nil
}
func (fakeCatalogs) GetCustomField(int) (*services.CustomFieldResult, error) {
	return nil, errFakeToken
}
func (fakeCatalogs) ListWorkspaces(int, services.CatalogPageParams) ([]models.Workspace, int, error) {
	return []models.Workspace{}, 0, nil
}
func (fakeCatalogs) ListWorkspaceTemplates(context.Context, int) ([]models.WorkspaceTemplateSummary, error) {
	return []models.WorkspaceTemplateSummary{}, nil
}
func (fakeCatalogs) GetWorkspace(int, int) (*models.Workspace, error) { return nil, errFakeToken }
func (fakeCatalogs) VisitWorkspace(int, int) (*models.Workspace, error) {
	return nil, errFakeToken
}
func (fakeCatalogs) ListWorkspaceStatuses(int, int, *int) ([]services.StatusResult, error) {
	return []services.StatusResult{}, nil
}
func (fakeCatalogs) ListWorkspaceItemTypes(int, int) ([]services.ItemTypeResult, error) {
	return []services.ItemTypeResult{}, nil
}
func (fakeCatalogs) ListWorkspaceWorkflows(int, int) ([]services.WorkflowResult, error) {
	return []services.WorkflowResult{}, nil
}
func (fakeCatalogs) ListWorkspacePriorities(int, int) ([]services.PriorityResult, error) {
	return []services.PriorityResult{}, nil
}
func (fakeCatalogs) ListAssignableUsers(context.Context, int, int) ([]models.User, error) {
	return []models.User{}, nil
}
func (fakeCatalogs) ListUsers(int, services.CatalogPageParams) ([]models.User, int, error) {
	return []models.User{}, 0, nil
}
func (fakeCatalogs) GetUser(int, int) (*models.User, error) { return nil, errFakeToken }
func (fakeCatalogs) GetUserDisplayBatch(context.Context, int, []int) ([]models.User, error) {
	return []models.User{}, nil
}
func (fakeCatalogs) ListLabels(int, int) ([]models.Label, error)   { return []models.Label{}, nil }
func (fakeCatalogs) GetLabel(int, int, int) (*models.Label, error) { return nil, errFakeToken }
func (fakeCatalogs) ListItemTemplates(int, int, *int) (services.ItemTemplateListResult, error) {
	return services.ItemTemplateListResult{Items: []models.ItemTemplate{}}, nil
}
func (fakeCatalogs) GetItemTemplate(int, int, int) (*models.ItemTemplate, error) {
	return nil, errFakeToken
}

type fakeWorkspaces struct{}

func (fakeWorkspaces) Create(context.Context, services.AuditActor, services.CreateWorkspaceParams) (*models.Workspace, error) {
	return &models.Workspace{ID: 1}, nil
}
func (fakeWorkspaces) Update(services.AuditActor, services.UpdateWorkspaceParams) (*models.Workspace, error) {
	return &models.Workspace{ID: 1}, nil
}
func (fakeWorkspaces) Delete(services.AuditActor, int) error { return nil }

type fakeItemTemplates struct{}

func (fakeItemTemplates) Create(services.AuditActor, int, services.ItemTemplateInput) (*models.ItemTemplate, error) {
	return &models.ItemTemplate{ID: 1}, nil
}
func (fakeItemTemplates) Update(services.AuditActor, int, int, services.ItemTemplatePatch) (*models.ItemTemplate, error) {
	return &models.ItemTemplate{ID: 1}, nil
}
func (fakeItemTemplates) Delete(services.AuditActor, int, int) error { return nil }

type fakeConcurrency struct{ allow bool }

func (f fakeConcurrency) Acquire(int) (func(), bool) {
	if !f.allow {
		return nil, false
	}
	return func() {}, true
}

type fakeLabels struct{}

func (fakeLabels) Create(services.AuditActor, string, string) (*models.Label, error) {
	return &models.Label{}, nil
}
func (fakeLabels) Update(services.AuditActor, int, services.LabelUpdate) (*models.Label, error) {
	return &models.Label{}, nil
}
func (fakeLabels) Delete(services.AuditActor, int) error { return nil }
func (fakeLabels) ListForItem(int) ([]models.Label, error) {
	return []models.Label{}, nil
}
func (fakeLabels) SetForItem(services.AuditActor, int, []int) ([]models.Label, error) {
	return []models.Label{}, nil
}
func (fakeLabels) AddToItem(services.AuditActor, int, int) ([]models.Label, error) {
	return []models.Label{}, nil
}
func (fakeLabels) RemoveFromItem(services.AuditActor, int, int) error { return nil }

type fakeParticipants struct{}

func (fakeParticipants) List(int) ([]models.ItemParticipant, error) {
	return []models.ItemParticipant{}, nil
}
func (fakeParticipants) Add(context.Context, int, int, services.ParticipantInput) ([]models.ItemParticipant, bool, error) {
	return []models.ItemParticipant{}, false, nil
}
func (fakeParticipants) Remove(context.Context, int, int, int) ([]models.ItemParticipant, error) {
	return []models.ItemParticipant{}, nil
}

type fakeItems struct{}

func (fakeItems) FindByID(int) (*models.Item, error) {
	return &models.Item{ID: 1, WorkspaceID: 1}, nil
}
func (fakeItems) FindByIDsInWorkspace(context.Context, int, []int) ([]*models.Item, error) {
	return []*models.Item{{ID: 1, WorkspaceID: 1}}, nil
}

type fakeAccess struct{}

func (fakeAccess) CanViewWorkspace(int, int) (bool, error)  { return true, nil }
func (fakeAccess) CanEditWorkspace(int, int) (bool, error)  { return true, nil }
func (fakeAccess) CanAdminWorkspace(int, int) (bool, error) { return true, nil }
func (fakeAccess) GetAccessibleWorkspaceIDs(int) ([]int, error) {
	return []int{1}, nil
}
func (fakeAccess) HasWorkspacePermissionFor(int, int, string) (bool, error) {
	return true, nil
}
func (fakeAccess) HasWorkspacePermission(int, int, string) (bool, error) { return true, nil }
func (fakeAccess) HasGlobalPermission(int, string) (bool, error)         { return true, nil }
func (fakeAccess) Can(int, int, int, string) (bool, error)               { return true, nil }

type fakePreferences struct{}

func (fakePreferences) GetSnapshot(int) (services.UserPreferencesSnapshot, error) {
	return services.UserPreferencesSnapshot{ColorMode: "system"}, nil
}
func (fakePreferences) UpdateSnapshot(int, services.UserPreferencesPatch) (services.UserPreferencesSnapshot, error) {
	return services.UserPreferencesSnapshot{ColorMode: "system"}, nil
}

type fakeRecurrence struct{}

func (fakeRecurrence) Get(int) (*models.RecurrenceRule, error) { return nil, errFakeToken }
func (fakeRecurrence) Create(int, int, int, models.CreateRecurrenceRequest, ...services.AuditActor) (*models.RecurrenceRule, error) {
	return &models.RecurrenceRule{}, nil
}
func (fakeRecurrence) UpdateWithPatch(int, services.RecurrenceUpdate, ...services.AuditActor) (*models.RecurrenceRule, error) {
	return &models.RecurrenceRule{}, nil
}
func (fakeRecurrence) Delete(int, ...services.AuditActor) error { return nil }
func (fakeRecurrence) ListInstances(int, int, int) (*services.RecurrenceInstances, error) {
	return &services.RecurrenceInstances{Items: []*models.RecurrenceInstance{}}, nil
}
func (fakeRecurrence) ForceGenerate(int, ...services.AuditActor) (int, error) { return 0, nil }
func (fakeRecurrence) ListByWorkspace(int) ([]*models.RecurrenceRule, error) {
	return []*models.RecurrenceRule{}, nil
}
func (fakeRecurrence) Preview(models.RRulePreviewRequest) (*services.RecurrencePreview, error) {
	return &services.RecurrencePreview{}, nil
}

type fakeItemDiagrams struct{}

func (fakeItemDiagrams) List(int) ([]models.ItemDiagram, error) { return []models.ItemDiagram{}, nil }
func (fakeItemDiagrams) Get(int) (*models.ItemDiagram, error) {
	return &models.ItemDiagram{ID: 1, ItemID: 1}, nil
}
func (fakeItemDiagrams) Create(int, string, string, int) (*models.ItemDiagram, error) {
	return &models.ItemDiagram{}, nil
}
func (fakeItemDiagrams) Patch(int, *string, *string, int) (*models.ItemDiagram, error) {
	return &models.ItemDiagram{}, nil
}
func (fakeItemDiagrams) Delete(int, int) (*models.ItemDiagram, error) {
	return &models.ItemDiagram{}, nil
}

type fakePages struct{}

func (fakePages) GetByID(int) (*models.Page, error) {
	return &models.Page{ID: 1, WorkspaceID: 1}, nil
}

type fakePageApplication struct{}

func (fakePageApplication) List(int, int) ([]models.Page, error) {
	return []models.Page{}, nil
}
func (fakePageApplication) ListTitlesAcrossWorkspaces(int, []int) ([]services.PageTitleRow, error) {
	return []services.PageTitleRow{}, nil
}
func (fakePageApplication) EffectiveLevels(int, int) (map[int]string, error) {
	return map[int]string{}, nil
}
func (fakePageApplication) Get(int, int, int) (*models.Page, error) {
	return &models.Page{ID: 1, WorkspaceID: 1}, nil
}
func (fakePageApplication) Search(int, int, string, int) ([]models.Page, error) {
	return []models.Page{}, nil
}
func (fakePageApplication) ListArchived(int, int) ([]repository.ArchivedPageRow, error) {
	return []repository.ArchivedPageRow{}, nil
}
func (fakePageApplication) ListHistory(int, int, int, int, int) ([]models.PageRevision, int, error) {
	return []models.PageRevision{}, 0, nil
}
func (fakePageApplication) GetRevision(int, int, int, int) (*models.PageRevision, error) {
	return &models.PageRevision{}, nil
}
func (fakePageApplication) GetPermissions(int, int, int) (services.PagePermissionsResult, error) {
	return services.PagePermissionsResult{ACL: []models.PagePermission{}}, nil
}
func (fakePageApplication) Create(services.AuditActor, services.CreatePageInput) (*models.Page, error) {
	return &models.Page{}, nil
}
func (fakePageApplication) Update(services.AuditActor, int, services.PageApplicationUpdateInput) (*models.Page, error) {
	return &models.Page{}, nil
}
func (fakePageApplication) Move(services.AuditActor, int, int, *int, *int, *int, *int) (*models.Page, error) {
	return &models.Page{}, nil
}
func (fakePageApplication) Archive(services.AuditActor, int, int) (*models.Page, error) {
	return &models.Page{}, nil
}
func (fakePageApplication) Restore(services.AuditActor, int, int, int) (*models.Page, error) {
	return &models.Page{}, nil
}
func (fakePageApplication) GrantPermission(services.AuditActor, int, int, string, int, string) (*models.PagePermission, error) {
	return &models.PagePermission{}, nil
}
func (fakePageApplication) RevokePermission(services.AuditActor, int, int, int) error {
	return nil
}
func (fakePageApplication) SetInheritance(services.AuditActor, int, int, bool) (*models.Page, error) {
	return &models.Page{}, nil
}
func (fakePageApplication) Unarchive(services.AuditActor, int, int) (*models.Page, error) {
	return &models.Page{}, nil
}

type fakePageDiagrams struct{}

func (fakePageDiagrams) Create(context.Context, services.AuditActor, services.CreatePageDiagramInput) (*services.PageDiagram, error) {
	return &services.PageDiagram{}, nil
}
func (fakePageDiagrams) List(context.Context, services.AuditActor, int) ([]services.PageDiagram, error) {
	return []services.PageDiagram{}, nil
}
func (fakePageDiagrams) Get(context.Context, services.AuditActor, int, int) (*services.PageDiagram, error) {
	return &services.PageDiagram{}, nil
}
func (fakePageDiagrams) Update(context.Context, services.AuditActor, services.UpdatePageDiagramInput) (*services.PageDiagram, error) {
	return &services.PageDiagram{}, nil
}

type fakePageLabels struct{}

func (fakePageLabels) List(int) ([]models.PageLabel, error) { return []models.PageLabel{}, nil }
func (fakePageLabels) Get(int, int) (*models.PageLabel, error) {
	return &models.PageLabel{}, nil
}
func (fakePageLabels) Create(int, string, string, ...services.AuditActor) (*models.PageLabel, error) {
	return &models.PageLabel{}, nil
}
func (fakePageLabels) Update(int, int, services.PageLabelUpdate, ...services.AuditActor) (*models.PageLabel, error) {
	return &models.PageLabel{}, nil
}
func (fakePageLabels) Delete(int, int, ...services.AuditActor) (*models.PageLabel, error) {
	return &models.PageLabel{}, nil
}
func (fakePageLabels) ListForPage(int) ([]models.PageLabel, error) {
	return []models.PageLabel{}, nil
}
func (fakePageLabels) SetForPage(int, int, []int) ([]models.PageLabel, error) {
	return []models.PageLabel{}, nil
}
func (fakePageLabels) AddToPage(int, int, int) ([]models.PageLabel, error) {
	return []models.PageLabel{}, nil
}
func (fakePageLabels) RemoveFromPage(int, int) error { return nil }

type fakeCannedResponses struct{}

func (fakeCannedResponses) List(int, bool) ([]models.CannedResponse, error) {
	return []models.CannedResponse{}, nil
}
func (fakeCannedResponses) Get(int, int) (*models.CannedResponse, error) {
	return &models.CannedResponse{}, nil
}
func (fakeCannedResponses) Create(int, services.CannedResponseInput, ...services.AuditActor) (*models.CannedResponse, error) {
	return &models.CannedResponse{}, nil
}
func (fakeCannedResponses) Update(int, int, services.CannedResponseUpdate, ...services.AuditActor) (*models.CannedResponse, error) {
	return &models.CannedResponse{}, nil
}
func (fakeCannedResponses) Delete(int, int, ...services.AuditActor) (*models.CannedResponse, error) {
	return &models.CannedResponse{}, nil
}
func (fakeCannedResponses) RenderPreview(int, int, int, ...services.AuditActor) (string, error) {
	return "", nil
}

type fakeWorklogs struct{}

func (fakeWorklogs) Create(int, services.WorklogMutationInput) (*services.WorklogMutationResult, error) {
	return &services.WorklogMutationResult{Worklog: &models.Worklog{}}, nil
}
func (fakeWorklogs) Update(int, int, services.WorklogMutationInput) (*services.WorklogMutationResult, error) {
	return &services.WorklogMutationResult{Worklog: &models.Worklog{}}, nil
}
func (fakeWorklogs) Delete(int) error { return nil }
func (fakeWorklogs) Get(int) (*models.Worklog, error) {
	return &models.Worklog{}, nil
}
func (fakeWorklogs) ListMine(repository.WorklogListFilter) ([]models.Worklog, int, error) {
	return []models.Worklog{}, 0, nil
}
func (fakeWorklogs) List(repository.WorklogDetailFilter) ([]models.Worklog, error) {
	return []models.Worklog{}, nil
}
func (fakeWorklogs) ListPage(repository.WorklogDetailFilter) ([]models.Worklog, int, error) {
	return []models.Worklog{}, 0, nil
}
func (fakeWorklogs) Aggregate(ctx context.Context, filter repository.WorklogDetailFilter, timezone string) (*services.WorklogAggregate, error) {
	return &services.WorklogAggregate{}, nil
}

type fakeTimeAccess struct{}

func (fakeTimeAccess) CanViewWorklog(int, int) (bool, error) { return true, nil }

func (fakeTimeAccess) CanBookTimeOnProject(int, int) (bool, error) { return true, nil }
func (fakeTimeAccess) CanEditWorklog(int, int) (bool, error)       { return true, nil }
func (fakeTimeAccess) CanViewProject(int, int) (bool, error)       { return true, nil }
func (fakeTimeAccess) AccessibleTimeProjectIDs(int) ([]int, error) { return []int{1}, nil }
func (fakeTimeAccess) IsTimeProjectManager(int, int) (bool, error) { return true, nil }

// nil means unrestricted access in WorklogDetailFilter.
func (fakeTimeAccess) GetAccessibleProjects(int) ([]int, error) { return nil, nil }
func (fakeTimeAccess) GetManagedProjects(int) ([]int, error)    { return nil, nil }

type fakeCatalogMutations struct{}

func (fakeCatalogMutations) CreateItemType(services.AuditActor, models.ItemType) (*models.ItemType, error) {
	return &models.ItemType{ID: 1}, nil
}
func (fakeCatalogMutations) PatchItemType(services.AuditActor, int, services.ItemTypePatch) (*models.ItemType, error) {
	return &models.ItemType{ID: 1}, nil
}
func (fakeCatalogMutations) DeleteItemType(services.AuditActor, int) error { return nil }
func (fakeCatalogMutations) CreatePriority(services.AuditActor, models.Priority) (*models.Priority, error) {
	return &models.Priority{ID: 1}, nil
}
func (fakeCatalogMutations) PatchPriority(services.AuditActor, int, services.PriorityPatch) (*models.Priority, error) {
	return &models.Priority{ID: 1}, nil
}
func (fakeCatalogMutations) DeletePriority(services.AuditActor, int) error { return nil }
func (fakeCatalogMutations) CreateStatus(services.AuditActor, models.Status) (*models.Status, error) {
	return &models.Status{ID: 1}, nil
}
func (fakeCatalogMutations) PatchStatus(services.AuditActor, int, services.StatusPatch) (*models.Status, error) {
	return &models.Status{ID: 1}, nil
}
func (fakeCatalogMutations) DeleteStatus(services.AuditActor, int) error { return nil }
func (fakeCatalogMutations) CreateStatusCategory(services.AuditActor, models.StatusCategory) (*models.StatusCategory, error) {
	return &models.StatusCategory{ID: 1}, nil
}
func (fakeCatalogMutations) PatchStatusCategory(services.AuditActor, int, services.StatusCategoryPatch) (*models.StatusCategory, error) {
	return &models.StatusCategory{ID: 1}, nil
}
func (fakeCatalogMutations) DeleteStatusCategory(services.AuditActor, int) error { return nil }
func (fakeCatalogMutations) NonDoneStatusIDs() ([]int, error)                    { return []int{}, nil }
func (fakeCatalogMutations) CreateWorkflow(services.AuditActor, models.Workflow) (*services.WorkflowResult, error) {
	return &services.WorkflowResult{ID: 1}, nil
}
func (fakeCatalogMutations) PatchWorkflow(services.AuditActor, int, services.WorkflowPatch) (*services.WorkflowResult, error) {
	return &services.WorkflowResult{ID: 1}, nil
}
func (fakeCatalogMutations) DeleteWorkflow(services.AuditActor, int) error { return nil }
func (fakeCatalogMutations) ReplaceWorkflowTransitions(services.AuditActor, int, []models.WorkflowTransition) ([]services.WorkflowTransitionResult, error) {
	return []services.WorkflowTransitionResult{}, nil
}
func (fakeCatalogMutations) AvailableWorkflowTransitions(int, int) ([]models.WorkflowTransition, error) {
	return []models.WorkflowTransition{}, nil
}
func (fakeCatalogMutations) ListLinkTypes(bool) ([]models.LinkType, error) {
	return []models.LinkType{}, nil
}
func (fakeCatalogMutations) GetLinkType(int) (*models.LinkType, error) {
	return &models.LinkType{ID: 1}, nil
}
func (fakeCatalogMutations) CreateLinkType(services.AuditActor, models.LinkType) (*models.LinkType, error) {
	return &models.LinkType{ID: 1}, nil
}
func (fakeCatalogMutations) PatchLinkType(services.AuditActor, int, services.LinkTypePatch) (*models.LinkType, error) {
	return &models.LinkType{ID: 1}, nil
}
func (fakeCatalogMutations) DeleteLinkType(services.AuditActor, int) error { return nil }

type fakeTimeProjects struct{}

func (fakeTimeProjects) List(int, string) ([]repository.TimeProjectDetail, error) {
	return []repository.TimeProjectDetail{}, nil
}
func (fakeTimeProjects) ListForWorkspace(int, int, string) ([]repository.TimeProjectDetail, error) {
	return []repository.TimeProjectDetail{}, nil
}
func (fakeTimeProjects) Get(int, int) (*repository.TimeProjectDetail, error) {
	return &repository.TimeProjectDetail{ID: 1}, nil
}
func (fakeTimeProjects) ListCategories() ([]models.TimeProjectCategory, error) {
	return []models.TimeProjectCategory{}, nil
}
func (fakeTimeProjects) GetCategory(int) (*models.TimeProjectCategory, error) {
	return &models.TimeProjectCategory{ID: 1}, nil
}
func (fakeTimeProjects) CreateCategory(services.AuditActor, models.TimeProjectCategory) (*models.TimeProjectCategory, error) {
	return &models.TimeProjectCategory{ID: 1}, nil
}
func (fakeTimeProjects) PatchCategory(services.AuditActor, int, services.TimeProjectCategoryPatch) (*models.TimeProjectCategory, error) {
	return &models.TimeProjectCategory{ID: 1}, nil
}
func (fakeTimeProjects) DeleteCategory(services.AuditActor, int) error { return nil }
func (fakeTimeProjects) ReorderCategories(int, []services.TimeProjectOrder) ([]models.TimeProjectCategory, error) {
	return []models.TimeProjectCategory{}, nil
}
func (fakeTimeProjects) CreateProject(services.AuditActor, models.TimeProject) (*repository.TimeProjectDetail, error) {
	return &repository.TimeProjectDetail{ID: 1}, nil
}
func (fakeTimeProjects) PatchProject(services.AuditActor, int, services.TimeProjectPatch) (*repository.TimeProjectDetail, error) {
	return &repository.TimeProjectDetail{ID: 1}, nil
}
func (fakeTimeProjects) DeleteProject(services.AuditActor, int) error { return nil }
func (fakeTimeProjects) ListManagers(int, int) ([]models.TimeProjectManager, error) {
	return []models.TimeProjectManager{}, nil
}
func (fakeTimeProjects) AddManager(services.AuditActor, int, models.TimeProjectManagerRequest) (*models.TimeProjectManager, error) {
	return &models.TimeProjectManager{ID: 1}, nil
}
func (fakeTimeProjects) RemoveManager(services.AuditActor, int, int) error { return nil }
func (fakeTimeProjects) ListMembers(int, int) ([]models.TimeProjectMember, error) {
	return []models.TimeProjectMember{}, nil
}
func (fakeTimeProjects) AddMember(services.AuditActor, int, models.TimeProjectMemberRequest) (*models.TimeProjectMember, error) {
	return &models.TimeProjectMember{ID: 1}, nil
}
func (fakeTimeProjects) RemoveMember(services.AuditActor, int, int) error { return nil }

type fakeTimers struct{}

func (fakeTimers) StartTimer(int, int, int, *int, string) (*models.ActiveTimer, error) {
	return &models.ActiveTimer{ID: 1}, nil
}
func (fakeTimers) GetActiveForUser(int) (*models.ActiveTimer, error) {
	return nil, repository.ErrNotFound
}
func (fakeTimers) StopActiveForUser(int) (*services.StopResult, error) {
	return &services.StopResult{TimerID: 1}, nil
}

type fakeSystemAdmins struct{}

func (fakeSystemAdmins) IsSystemAdmin(int) (bool, error) { return true, nil }

type fakeGroups struct{}

func (fakeGroups) ListPage(int, int) ([]models.TeamGroup, int, error) {
	return []models.TeamGroup{}, 0, nil
}
func (fakeGroups) Get(int, bool) (*models.TeamGroup, error) {
	return &models.TeamGroup{ID: 1, Members: []models.TeamGroupMember{}}, nil
}
func (fakeGroups) Create(string, string, int) (*services.GroupMutationResult, error) {
	return &services.GroupMutationResult{Group: &models.TeamGroup{ID: 1}}, nil
}
func (fakeGroups) Update(int, services.GroupUpdateInput) (*services.GroupMutationResult, error) {
	return &services.GroupMutationResult{Group: &models.TeamGroup{ID: 1}}, nil
}
func (fakeGroups) Delete(int) (*repository.GroupDeleteSnapshot, error) {
	return &repository.GroupDeleteSnapshot{}, nil
}

type fakeAdminUsers struct{ user *models.User }

func (f fakeAdminUsers) ListAdmin(services.PaginationParams) ([]models.User, int, error) {
	if f.user == nil {
		return []models.User{}, 0, nil
	}
	return []models.User{*f.user}, 1, nil
}
func (f fakeAdminUsers) GetByID(int) (*models.User, error) { return f.user, nil }
func (fakeAdminUsers) GetGroupIDs(int) ([]int, error)      { return []int{}, nil }
func (f fakeAdminUsers) UpdateAdmin(int, services.AdminUserUpdate) (*services.AdminUserUpdateResult, error) {
	return &services.AdminUserUpdateResult{User: f.user}, nil
}

type fakeComments struct{}

func (fakeComments) Get(int) (*services.CommentWithDetails, error) {
	return &services.CommentWithDetails{Comment: models.Comment{ID: 1, ItemID: 1}, WorkspaceID: 1}, nil
}
func (fakeComments) GetFeedByItemID(int, bool, services.CommentFeedOptions) (*services.CommentFeedPage, error) {
	return &services.CommentFeedPage{Comments: []models.Comment{}}, nil
}
func (fakeComments) GetFeedByItemIDs([]int, bool, services.CommentFeedOptions) (map[int]*services.CommentFeedPage, error) {
	return map[int]*services.CommentFeedPage{}, nil
}
func (fakeComments) CountFeedByItemID(int) (int, error) { return 0, nil }
func (fakeComments) UserCanReadItemAsApprover(context.Context, int, int) (bool, error) {
	return false, nil
}
func (fakeComments) Create(services.CreateCommentParams) (*services.CreateCommentResult, error) {
	return &services.CreateCommentResult{CommentID: 1}, nil
}
func (fakeComments) UpdateWithEffects(services.UpdateCommentParams) (*models.Comment, error) {
	return &models.Comment{}, nil
}
func (fakeComments) DeleteWithEffects(services.DeleteCommentParams) error { return nil }
func (fakeComments) AttachReactions([]models.Comment) error               { return nil }
func (fakeComments) AddReaction(int, int, string) ([]models.CommentReaction, error) {
	return []models.CommentReaction{}, nil
}
func (fakeComments) RemoveReaction(int, int, string) error { return nil }

type fakeAttachments struct{}

func (fakeAttachments) UploadPolicy() (services.ItemAttachmentUploadPolicy, error) {
	return services.ItemAttachmentUploadPolicy{Enabled: true, MaxFileSize: 1024}, nil
}
func (fakeAttachments) ListItemAttachments(int, int, int, int) ([]models.Attachment, int, error) {
	return []models.Attachment{}, 0, nil
}
func (fakeAttachments) GetItemAttachment(int, int) (*models.Attachment, error) {
	return &models.Attachment{ID: 1}, nil
}
func (fakeAttachments) OpenItemAttachment(context.Context, int, int, bool) (*services.ItemAttachmentBinary, error) {
	return nil, errFakeToken
}
func (fakeAttachments) UploadItemAttachment(context.Context, services.ItemAttachmentUploadInput) (models.AttachmentUploadResponse, error) {
	return models.AttachmentUploadResponse{Success: true, Attachment: models.Attachment{ID: 1}}, nil
}
func (fakeAttachments) DeleteItemAttachment(context.Context, int, int) error { return nil }

type fakePageAttachments struct{}

func (fakePageAttachments) UploadPageAttachment(context.Context, services.PageAttachmentUploadInput) (models.AttachmentUploadResponse, error) {
	return models.AttachmentUploadResponse{Attachment: models.Attachment{ID: 1}}, nil
}
func (fakePageAttachments) OpenPageAttachment(context.Context, int, int, bool) (*services.ItemAttachmentBinary, error) {
	return nil, services.ErrPageAttachmentNotFound
}

type fakeCollections struct{}

func (fakeCollections) List(services.CollectionListParams) ([]models.Collection, int, error) {
	return []models.Collection{}, 0, nil
}
func (fakeCollections) Get(int, int) (*models.Collection, error) {
	return &models.Collection{ID: 1}, nil
}
func (fakeCollections) GetBySlug(int, string) (*models.Collection, error) {
	return &models.Collection{ID: 1}, nil
}
func (fakeCollections) Create(services.AuditActor, models.Collection) (*models.Collection, error) {
	return &models.Collection{ID: 1}, nil
}
func (fakeCollections) Update(services.AuditActor, int, services.CollectionUpdate) (*models.Collection, error) {
	return &models.Collection{ID: 1}, nil
}
func (fakeCollections) UpdateSharing(services.AuditActor, int, services.CollectionSharingUpdate) (*models.Collection, error) {
	return &models.Collection{ID: 1}, nil
}
func (fakeCollections) Delete(services.AuditActor, int) error { return nil }
func (fakeCollections) ListCategories() ([]models.CollectionCategory, error) {
	return []models.CollectionCategory{}, nil
}
func (fakeCollections) GetCategory(int) (*models.CollectionCategory, error) {
	return &models.CollectionCategory{ID: 1}, nil
}
func (fakeCollections) CreateCategory(services.AuditActor, models.CollectionCategory) (*models.CollectionCategory, error) {
	return &models.CollectionCategory{ID: 1}, nil
}
func (fakeCollections) PatchCategory(services.AuditActor, int, services.CollectionCategoryPatch) (*models.CollectionCategory, error) {
	return &models.CollectionCategory{ID: 1}, nil
}
func (fakeCollections) DeleteCategory(services.AuditActor, int) error { return nil }
func (fakeCollections) GetBoardConfiguration(int, services.BoardConfigurationScope) (*models.BoardConfiguration, error) {
	return &models.BoardConfiguration{ID: 1}, nil
}
func (fakeCollections) GetBoardConfigurationBootstrap(context.Context, int, services.BoardConfigurationScope, *int) (*services.BoardConfigurationBootstrap, error) {
	return &services.BoardConfigurationBootstrap{BoardConfiguration: &models.BoardConfiguration{ID: 1}}, nil
}
func (fakeCollections) PutBoardConfiguration(services.AuditActor, services.BoardConfigurationScope, models.BoardConfigurationRequest) (*models.BoardConfiguration, error) {
	return &models.BoardConfiguration{ID: 1}, nil
}
func (fakeCollections) DeleteBoardConfiguration(services.AuditActor, services.BoardConfigurationScope) error {
	return nil
}

type fakePlanning struct{}

func (fakePlanning) ListMilestones(int, services.MilestoneListParams) ([]services.MilestoneResult, int, error) {
	return []services.MilestoneResult{}, 0, nil
}
func (fakePlanning) GetMilestone(int, int) (*services.MilestoneResult, error) {
	return &services.MilestoneResult{ID: 1, Name: "Milestone", IsGlobal: true}, nil
}
func (fakePlanning) CreateMilestone(int, services.AuditActor, services.CreateMilestoneParams) (*services.MilestoneResult, error) {
	return &services.MilestoneResult{ID: 1}, nil
}
func (fakePlanning) UpdateMilestone(int, services.AuditActor, services.UpdateMilestoneParams) (*services.MilestoneResult, error) {
	return &services.MilestoneResult{ID: 1}, nil
}
func (fakePlanning) DeleteMilestone(int, services.AuditActor, int) error { return nil }
func (fakePlanning) ReorderMilestones(int, services.AuditActor, services.MilestoneScope, []int) error {
	return nil
}
func (fakePlanning) GetMilestoneProgress(int, int) (*services.MilestoneProgressReport, error) {
	return &services.MilestoneProgressReport{}, nil
}
func (fakePlanning) GetMilestoneTestStatistics(int, int) (*services.MilestoneTestStats, error) {
	return &services.MilestoneTestStats{}, nil
}
func (fakePlanning) GetMilestoneTestStatisticsBatch(int, []int) (map[int]*services.MilestoneTestStats, error) {
	return map[int]*services.MilestoneTestStats{}, nil
}
func (fakePlanning) ReleaseMilestone(context.Context, int, services.AuditActor, int, services.ReleaseMilestoneInput) (*services.MilestoneResult, error) {
	return &services.MilestoneResult{ID: 1}, nil
}
func (fakePlanning) ListIterations(int, services.IterationListParams) ([]services.IterationResult, int, error) {
	return []services.IterationResult{}, 0, nil
}
func (fakePlanning) GetIteration(int, int) (*services.IterationResult, error) {
	return &services.IterationResult{ID: 1, Name: "Iteration", IsGlobal: true}, nil
}
func (fakePlanning) CreateIteration(int, services.AuditActor, services.CreateIterationParams) (*services.IterationResult, error) {
	return &services.IterationResult{ID: 1}, nil
}
func (fakePlanning) UpdateIteration(int, services.AuditActor, services.UpdateIterationParams) (*services.IterationResult, error) {
	return &services.IterationResult{ID: 1}, nil
}
func (fakePlanning) DeleteIteration(int, services.AuditActor, int) error { return nil }
func (fakePlanning) CompleteIteration(context.Context, int, int, *int) (*services.CompleteIterationResult, error) {
	return &services.CompleteIterationResult{IterationID: 1}, nil
}
func (fakePlanning) GetIterationProgress(int, int) (*services.IterationProgressReport, error) {
	return &services.IterationProgressReport{}, nil
}
func (fakePlanning) GetIterationProgressBatch(int, []int) (map[int]*services.IterationProgressReport, error) {
	return map[int]*services.IterationProgressReport{}, nil
}
func (fakePlanning) GetIterationBurndown(int, int) (*services.IterationBurndownData, error) {
	return &services.IterationBurndownData{}, nil
}

type fakeLinks struct{}

func (fakeLinks) ListLinkTypes(bool) ([]models.LinkType, error) {
	return []models.LinkType{}, nil
}

func (fakeLinks) ListFieldLinks(int, int, int) ([]models.ItemLink, error) {
	return []models.ItemLink{}, nil
}
func (fakeLinks) ListLinksForEntityWithChecks(int, string, int) ([]models.ItemLink, []models.ItemLink, error) {
	return []models.ItemLink{}, []models.ItemLink{}, nil
}
func (fakeLinks) ListBatch(context.Context, int, services.BatchLinkParams) ([]services.BatchItemLinks, int, error) {
	return []services.BatchItemLinks{}, 0, nil
}
func (fakeLinks) CreateManagedLink(int, services.CreateItemLinkParams) (*models.ItemLink, error) {
	return &models.ItemLink{ID: 1}, nil
}
func (fakeLinks) DeleteLinkWithChecks(int, int) error { return nil }
func (fakeLinks) SearchLinkable(int, string, string, int, []int) ([]models.LinkableItem, error) {
	return []models.LinkableItem{}, nil
}

type fakeAgentRuns struct{}

func (fakeAgentRuns) ListForWorkspace(context.Context, int, int, int, int) ([]*models.AgentRun, error) {
	return []*models.AgentRun{}, nil
}
func (fakeAgentRuns) ListForItem(context.Context, int, int, int, int) ([]*models.AgentRun, error) {
	return []*models.AgentRun{}, nil
}
func (fakeAgentRuns) Get(context.Context, int, int) (*models.AgentRun, error) {
	return &models.AgentRun{ID: 1}, nil
}
func (fakeAgentRuns) Usage(context.Context, int, int) (repository.RunUsageTotals, error) {
	return repository.RunUsageTotals{}, nil
}
func (fakeAgentRuns) Events(context.Context, int, int, int, int) ([]*models.AgentRunEvent, error) {
	return []*models.AgentRunEvent{}, nil
}
func (fakeAgentRuns) Rerun(context.Context, int, int) (bool, error) { return true, nil }
func (fakeAgentRuns) Cancel(context.Context, int, int, bool) (services.AgentRunCancelResult, error) {
	return services.AgentRunCancelResult{Canceled: true}, nil
}

type fakeAgentSkills struct{}

func (fakeAgentSkills) List(context.Context, int, int, int) ([]*models.WorkspaceAgentSkill, error) {
	return []*models.WorkspaceAgentSkill{}, nil
}
func (fakeAgentSkills) Get(context.Context, int, int, int, int) (*models.WorkspaceAgentSkill, error) {
	return &models.WorkspaceAgentSkill{ID: 1}, nil
}
func (fakeAgentSkills) Create(context.Context, services.AuditActor, int, services.AgentSkillInput) (*models.WorkspaceAgentSkill, error) {
	return &models.WorkspaceAgentSkill{ID: 1}, nil
}
func (fakeAgentSkills) Patch(context.Context, services.AuditActor, int, int, services.AgentSkillPatch) (*models.WorkspaceAgentSkill, error) {
	return &models.WorkspaceAgentSkill{ID: 1}, nil
}
func (fakeAgentSkills) Delete(context.Context, services.AuditActor, int, int) error { return nil }

type fakeConditionSets struct{}

func (fakeConditionSets) List(*int) ([]models.ConditionSet, error) {
	return []models.ConditionSet{}, nil
}
func (fakeConditionSets) Get(int) (*models.ConditionSet, error) {
	return &models.ConditionSet{ID: 1}, nil
}
func (fakeConditionSets) Create(services.AuditActor, models.ConditionSet) (*models.ConditionSet, error) {
	return &models.ConditionSet{ID: 1}, nil
}
func (fakeConditionSets) Patch(services.AuditActor, int, services.ConditionSetPatch) (*models.ConditionSet, error) {
	return &models.ConditionSet{ID: 1}, nil
}
func (fakeConditionSets) Delete(services.AuditActor, int) error { return nil }

type fakeGovernance struct{}

func (fakeGovernance) ListApprovalSets(context.Context, *int) ([]models.ApprovalSet, error) {
	return []models.ApprovalSet{}, nil
}
func (fakeGovernance) GetApprovalSet(context.Context, int) (*models.ApprovalSet, error) {
	return &models.ApprovalSet{ID: 1}, nil
}
func (fakeGovernance) CreateApprovalSet(context.Context, services.AuditActor, models.ApprovalSet) (*models.ApprovalSet, error) {
	return &models.ApprovalSet{ID: 1}, nil
}
func (fakeGovernance) PatchApprovalSet(context.Context, services.AuditActor, int, services.ApprovalSetPatch) (*models.ApprovalSet, error) {
	return &models.ApprovalSet{ID: 1}, nil
}
func (fakeGovernance) DeleteApprovalSet(context.Context, services.AuditActor, int) error {
	return nil
}
func (fakeGovernance) ListItemApprovals(context.Context, int, int, int, int) ([]*models.ApprovalRequest, int, error) {
	return []*models.ApprovalRequest{}, 0, nil
}
func (fakeGovernance) ListMyApprovals(context.Context, int, string, int, int) ([]*models.ApprovalRequest, int, error) {
	return []*models.ApprovalRequest{}, 0, nil
}
func (fakeGovernance) GetApproval(context.Context, int, int) (*models.ApprovalRequest, error) {
	return &models.ApprovalRequest{ID: 1}, nil
}
func (fakeGovernance) DecideApproval(context.Context, services.AuditActor, int, string, string) (*services.ApprovalDecisionResult, error) {
	return &services.ApprovalDecisionResult{}, nil
}
func (fakeGovernance) CancelApproval(context.Context, services.AuditActor, int, string) (*models.ApprovalRequest, error) {
	return &models.ApprovalRequest{ID: 1}, nil
}
func (fakeGovernance) DelegateApproval(context.Context, services.AuditActor, int, int, string) (*models.ApprovalRequest, error) {
	return &models.ApprovalRequest{ID: 1}, nil
}
func (fakeGovernance) RefreshApprovalApprovers(context.Context, services.AuditActor, int, int, string) (*models.ApprovalRequest, error) {
	return &models.ApprovalRequest{ID: 1}, nil
}
func (fakeGovernance) EscalateApproval(context.Context, services.AuditActor, int, int) (*models.ApprovalRequest, error) {
	return &models.ApprovalRequest{ID: 1}, nil
}
func (fakeGovernance) GetTransitionGovernance(context.Context, int) (*services.TransitionGovernance, error) {
	return &services.TransitionGovernance{TransitionID: 1}, nil
}

type fakeActions struct{}

func (fakeActions) ListTemplates() []services.ActionTemplateSummary { return nil }
func (fakeActions) ApplyTemplate(context.Context, int, int, services.AuditActor, string) (*services.ApplyToWorkspaceResult, error) {
	return &services.ApplyToWorkspaceResult{ActionID: 1}, nil
}

func (fakeActions) Catalog(int, int) (services.ActionCatalog, error) {
	return services.ActionCatalog{}, nil
}
func (fakeActions) Validate(int, int, models.CreateActionRequest) (actioncatalog.ValidationErrors, error) {
	return nil, nil
}
func (fakeActions) List(int, int) ([]*models.Action, error) { return []*models.Action{}, nil }
func (fakeActions) Get(int, int, int) (*models.Action, error) {
	return &models.Action{ID: 1}, nil
}
func (fakeActions) Create(int, int, services.AuditActor, models.CreateActionRequest) (*models.Action, error) {
	return &models.Action{ID: 1}, nil
}
func (fakeActions) Update(int, int, int, services.AuditActor, models.UpdateActionRequest) (*models.Action, error) {
	return &models.Action{ID: 1}, nil
}
func (fakeActions) Delete(int, int, int, services.AuditActor) error { return nil }
func (fakeActions) Toggle(int, int, int, services.AuditActor, *bool) (*models.Action, error) {
	return &models.Action{ID: 1}, nil
}
func (fakeActions) Logs(int, int, int, int, int) ([]*models.ActionExecutionLog, int, error) {
	return []*models.ActionExecutionLog{}, 0, nil
}
func (fakeActions) Execute(int, int, int, int) (models.ActionExecutionStatus, error) {
	return models.ActionStatusCompleted, nil
}

func TestCanonicalRoutesServeBothAuthenticationMounts(t *testing.T) {
	t.Parallel()
	mux, user := newTestMux(t, true)

	session := httptest.NewRequest(http.MethodGet, "/api/v2/users/me", nil)
	session.Header.Set("X-Test-Session", "valid")
	session.Header.Set("X-Request-ID", "req_session")
	sessionResponse := httptest.NewRecorder()
	mux.ServeHTTP(sessionResponse, session)

	bearer := httptest.NewRequest(http.MethodGet, "/rest/api/v2/users/me", nil)
	bearer.Header.Set("Authorization", "Bearer crw_valid")
	bearer.Header.Set("X-Request-ID", "req_bearer")
	bearerResponse := httptest.NewRecorder()
	mux.ServeHTTP(bearerResponse, bearer)

	if sessionResponse.Code != http.StatusOK || bearerResponse.Code != http.StatusOK {
		t.Fatalf("status: session=%d bearer=%d", sessionResponse.Code, bearerResponse.Code)
	}
	if sessionResponse.Body.String() != bearerResponse.Body.String() {
		t.Fatalf("mount responses differ:\nsession: %s\nbearer: %s", sessionResponse.Body, bearerResponse.Body)
	}

	var body struct {
		Data userDTO `json:"data"`
	}
	if err := json.Unmarshal(sessionResponse.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := mapUser(user)
	if !reflect.DeepEqual(body.Data, want) {
		t.Fatalf("user DTO mismatch:\n got: %#v\nwant: %#v", body.Data, want)
	}
}

func TestSessionOnlyRoutesAreNotMountedForBearer(t *testing.T) {
	t.Parallel()
	mux, _ := newTestMux(t, true)

	session := httptest.NewRequest(http.MethodGet, "/api/v2/admin/groups", nil)
	session.Header.Set("X-Test-Session", "valid")
	sessionResponse := httptest.NewRecorder()
	mux.ServeHTTP(sessionResponse, session)
	if sessionResponse.Code != http.StatusOK {
		t.Fatalf("session status = %d, want %d; body=%s", sessionResponse.Code, http.StatusOK, sessionResponse.Body)
	}

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantAllow  string
	}{
		{name: "admin path", method: http.MethodGet, path: "/rest/api/v2/admin/groups", wantStatus: http.StatusNotFound},
		{name: "asset catalog method", method: http.MethodPost, path: "/rest/api/v2/asset-sets", wantStatus: http.StatusMethodNotAllowed, wantAllow: http.MethodGet},
		{name: "asset catalog command", method: http.MethodPost, path: "/rest/api/v2/asset-categories/1/move", wantStatus: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set("Authorization", "Bearer crw_valid")
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != test.wantStatus || response.Header().Get("Allow") != test.wantAllow {
				t.Fatalf("status/Allow = %d/%q, want %d/%q; body=%s", response.Code, response.Header().Get("Allow"), test.wantStatus, test.wantAllow, response.Body)
			}
		})
	}
}

func TestCanonicalMountsCoexistWithSPACatchAll(t *testing.T) {
	t.Parallel()
	mux, _ := newTestMux(t, true)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/api/v2/users/me", nil)
	request.Header.Set("X-Test-Session", "valid")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("v2 status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body)
	}

	request = httptest.NewRequest(http.MethodGet, "/client-route", nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("SPA status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestAuthenticationMountsRejectCrossedCredentials(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		path       string
		headers    map[string]string
		allowScope bool
		status     int
		code       string
	}{
		{name: "session requires session", path: "/api/v2/users/me", status: 401, code: "authentication_required"},
		{name: "session rejects bearer beside session", path: "/api/v2/users/me", headers: map[string]string{"X-Test-Session": "valid", "Authorization": "Bearer crw_valid"}, status: 401, code: "invalid_token"},
		{name: "session public route rejects bearer", path: "/api/v2/openapi.json", headers: map[string]string{"Authorization": "Bearer crw_valid"}, status: 401, code: "invalid_token"},
		{name: "bearer ignores session", path: "/rest/api/v2/users/me", headers: map[string]string{"X-Test-Session": "valid"}, allowScope: true, status: 401, code: "authentication_required"},
		{name: "bearer rejects non crw token", path: "/rest/api/v2/users/me", headers: map[string]string{"Authorization": "Bearer session-token"}, allowScope: true, status: 401, code: "invalid_token"},
		{name: "bearer requires scope", path: "/rest/api/v2/users/me", headers: map[string]string{"Authorization": "Bearer crw_valid"}, status: 403, code: "insufficient_permission"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mux, _ := newTestMux(t, test.allowScope)
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("X-Request-ID", "req_test")
			for name, value := range test.headers {
				request.Header.Set(name, value)
			}
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.status, response.Body)
			}
			var body errorBody
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Error.Code != test.code || body.RequestID != "req_test" {
				t.Fatalf("error = %#v, request_id=%q", body.Error, body.RequestID)
			}
		})
	}
}

func TestPublicOpenAPIDocumentIsIdenticalAtBothMounts(t *testing.T) {
	t.Parallel()
	mux, _ := newTestMux(t, true)
	var bodies [][]byte
	var etags []string
	for _, path := range []string{"/api/v2/openapi.json", "/rest/api/v2/openapi.json"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d; body=%s", path, response.Code, response.Body)
		}
		bodies = append(bodies, slices.Clone(response.Body.Bytes()))
		etags = append(etags, response.Header().Get("ETag"))
	}
	if !slices.Equal(bodies[0], apispec.V2SpecJSON) || !slices.Equal(bodies[0], bodies[1]) {
		t.Fatal("mounted OpenAPI documents do not match the embedded v2 spec")
	}
	if etags[0] == "" || etags[0] != etags[1] {
		t.Fatalf("ETags = %q", etags)
	}
}

func TestPrefixFallbacksUseCanonicalErrors(t *testing.T) {
	t.Parallel()
	mux, _ := newTestMux(t, true)
	tests := []struct {
		name   string
		method string
		path   string
		status int
		code   string
		allow  string
	}{
		{name: "unknown route", method: http.MethodGet, path: "/api/v2/missing", status: 404, code: "not_found"},
		{name: "wrong method", method: http.MethodPost, path: "/rest/api/v2/users/me", status: 405, code: "method_not_allowed", allow: http.MethodGet},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set("X-Request-ID", "req_fallback")
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.status, response.Body)
			}
			var body errorBody
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Error.Code != test.code || body.RequestID != "req_fallback" {
				t.Fatalf("body = %#v", body)
			}
			if response.Header().Get("Allow") != test.allow {
				t.Fatalf("Allow = %q, want %q", response.Header().Get("Allow"), test.allow)
			}
		})
	}
}

func TestDispatchPrefersStaticRouteAndUsesItsScopes(t *testing.T) {
	t.Parallel()
	user := &models.User{ID: 42}
	routes := []route{
		{
			Route: Route{Method: http.MethodGet, Path: "/items/changes", Auth: AuthAuthenticated, Scopes: []string{"assets:read"}},
			handler: func(w http.ResponseWriter, _ *http.Request) error {
				w.WriteHeader(http.StatusNoContent)
				return nil
			},
		},
		{
			Route: Route{Method: http.MethodGet, Path: "/items/{item_id}", Auth: AuthAuthenticated, Scopes: []string{"items:read"}},
			handler: func(w http.ResponseWriter, _ *http.Request) error {
				w.WriteHeader(http.StatusTeapot)
				return nil
			},
		},
	}
	mux := http.NewServeMux()
	registerMount(mux, restPrefix, routes, Deps{
		Tokens:      fakeTokens{user: user, token: &models.APIToken{ID: 7}, allow: true, wantScopes: []string{"assets:read"}},
		Concurrency: fakeConcurrency{allow: true},
		CORS:        NewCORS(nil, false, true),
	}, false)

	request := httptest.NewRequest(http.MethodGet, "/rest/api/v2/items/changes", nil)
	request.Header.Set("Authorization", "Bearer crw_valid")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusNoContent, response.Body)
	}
}

func TestInventoryMatchesOpenAPI(t *testing.T) {
	t.Parallel()
	type openAPIOperation struct {
		Tags              []string              `json:"tags"`
		Summary           string                `json:"summary"`
		Description       string                `json:"description"`
		Security          []map[string][]string `json:"security"`
		RequiredScopes    []string              `json:"x-required-scopes"`
		RequiredAnyScopes []string              `json:"x-required-any-scopes"`
	}
	var spec struct {
		Tags []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tags"`
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(apispec.V2SpecJSON, &spec); err != nil {
		t.Fatalf("decode OpenAPI: %v", err)
	}
	declaredTags := make(map[string]bool, len(spec.Tags))
	for _, tag := range spec.Tags {
		if strings.TrimSpace(tag.Name) == "" || strings.TrimSpace(tag.Description) == "" {
			t.Fatalf("invalid OpenAPI tag: %#v", tag)
		}
		declaredTags[tag.Name] = true
	}
	for _, route := range BearerInventory() {
		operations, ok := spec.Paths[route.Path]
		if !ok {
			t.Errorf("%s is missing from OpenAPI", route.Path)
			continue
		}
		rawOperation, ok := operations[httpMethodKey(route.Method)]
		if !ok {
			t.Errorf("%s %s is missing from OpenAPI", route.Method, route.Path)
			continue
		}
		var operation openAPIOperation
		if err := json.Unmarshal(rawOperation, &operation); err != nil {
			t.Errorf("decode %s %s: %v", route.Method, route.Path, err)
			continue
		}
		if len(operation.Tags) != 1 || !declaredTags[operation.Tags[0]] {
			t.Errorf("%s %s tags = %v, want one declared domain", route.Method, route.Path, operation.Tags)
		}
		if summary := strings.TrimSpace(operation.Summary); summary == "" || len(summary) > 80 {
			t.Errorf("%s %s summary = %q, want 1–80 characters", route.Method, route.Path, operation.Summary)
		}
		if strings.TrimSpace(operation.Description) == "" {
			t.Errorf("%s %s has no description", route.Method, route.Path)
		}
		if route.Auth == AuthPublic {
			if len(operation.Security) != 0 || len(operation.RequiredScopes) != 0 || len(operation.RequiredAnyScopes) != 0 {
				t.Errorf("public route %s %s declares bearer security", route.Method, route.Path)
			}
			continue
		}
		if len(operation.Security) != 1 || len(operation.Security[0]["BearerAuth"]) != 0 {
			t.Errorf("%s %s must declare standard HTTP bearer security without OAuth scopes", route.Method, route.Path)
		}
		if !slices.Equal(operation.RequiredScopes, route.Scopes) {
			t.Errorf("%s %s x-required-scopes = %v, want %v", route.Method, route.Path, operation.RequiredScopes, route.Scopes)
		}
		if !slices.Equal(operation.RequiredAnyScopes, route.ScopesAny) {
			t.Errorf("%s %s x-required-any-scopes = %v, want %v", route.Method, route.Path, operation.RequiredAnyScopes, route.ScopesAny)
		}
	}
}

func TestOpenAPIOperationIDsAreSuffixFree(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(apispec.V2SpecJSON, &spec); err != nil {
		t.Fatalf("decode OpenAPI: %v", err)
	}
	seen := make(map[string]string)
	for path, methods := range spec.Paths {
		for method, rawOperation := range methods {
			if !slices.Contains([]string{"get", "post", "put", "patch", "delete"}, method) {
				continue
			}
			var operation struct {
				OperationID string `json:"operationId"`
			}
			if err := json.Unmarshal(rawOperation, &operation); err != nil {
				t.Errorf("decode %s %s: %v", method, path, err)
				continue
			}
			if operation.OperationID == "" {
				t.Errorf("%s %s has no operation ID", method, path)
				continue
			}
			if strings.HasSuffix(operation.OperationID, "V2") {
				t.Errorf("%s %s operation ID %q has a version suffix", method, path, operation.OperationID)
			}
			if previous, exists := seen[operation.OperationID]; exists {
				t.Errorf("operation ID %q is duplicated by %s and %s %s", operation.OperationID, previous, method, path)
			}
			seen[operation.OperationID] = method + " " + path
		}
	}
}

func newTestMux(t *testing.T, allowScope bool) (*http.ServeMux, *models.User) {
	return newTestMuxWithCatalog(t, allowScope, fakeCatalogs{})
}

func newTestMuxWithCatalog(t *testing.T, allowScope bool, catalog catalogReader, tokens ...tokenAuthenticator) (*http.ServeMux, *models.User) {
	t.Helper()
	user := &models.User{
		ID:            42,
		Email:         "ada@example.test",
		Username:      "ada",
		FirstName:     "Ada",
		LastName:      "Lovelace",
		FullName:      "Ada Lovelace",
		IsActive:      true,
		AgentPresence: "offline",
		Timezone:      "Europe/Zurich",
		Language:      "en",
		CreatedAt:     time.Date(2026, 9, 2, 8, 0, 0, 0, time.FixedZone("test", 2*60*60)),
	}
	mux := http.NewServeMux()
	configuration := configurationReader(fakeCatalogs{})
	if configured, ok := catalog.(configurationReader); ok {
		configuration = configured
	}
	statuses := statusReader(fakeCatalogs{})
	if configured, ok := catalog.(statusReader); ok {
		statuses = configured
	}
	labels := labelApplication(fakeLabels{})
	if configured, ok := catalog.(labelApplication); ok {
		labels = configured
	}
	items := itemReader(fakeItems{})
	if configured, ok := catalog.(itemReader); ok {
		items = configured
	}
	access := resourceAccess(fakeAccess{})
	if configured, ok := catalog.(resourceAccess); ok {
		access = configured
	}
	preferences := preferencesApplication(fakePreferences{})
	if configured, ok := catalog.(preferencesApplication); ok {
		preferences = configured
	}
	recurrence := recurrenceApplication(fakeRecurrence{})
	if configured, ok := catalog.(recurrenceApplication); ok {
		recurrence = configured
	}
	itemDiagrams := itemDiagramApplication(fakeItemDiagrams{})
	if configured, ok := catalog.(itemDiagramApplication); ok {
		itemDiagrams = configured
	}
	pages := pageReader(fakePages{})
	if configured, ok := catalog.(pageReader); ok {
		pages = configured
	}
	pageApplicationDependency := pageApplication(fakePageApplication{})
	if configured, ok := catalog.(pageApplication); ok {
		pageApplicationDependency = configured
	}
	pageDiagrams := pageDiagramApplication(fakePageDiagrams{})
	if configured, ok := catalog.(pageDiagramApplication); ok {
		pageDiagrams = configured
	}
	pageAccessDependency := pageAccess(fakeAccess{})
	if configured, ok := catalog.(pageAccess); ok {
		pageAccessDependency = configured
	}
	pageLabels := pageLabelApplication(fakePageLabels{})
	if configured, ok := catalog.(pageLabelApplication); ok {
		pageLabels = configured
	}
	worklogs := worklogApplication(fakeWorklogs{})
	if configured, ok := catalog.(worklogApplication); ok {
		worklogs = configured
	}
	timeAccessDependency := timeAccess(fakeTimeAccess{})
	if configured, ok := catalog.(timeAccess); ok {
		timeAccessDependency = configured
	}
	sessionMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Test-Session") == "valid" {
				ctx := context.WithValue(r.Context(), contextkeys.User, user)
				ctx = context.WithValue(ctx, contextkeys.AuthMethod, "session-header")
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	}
	tokenAuth := tokenAuthenticator(fakeTokens{user: user, token: &models.APIToken{ID: 7}, allow: allowScope})
	if len(tokens) > 0 && tokens[0] != nil {
		tokenAuth = tokens[0]
	}
	err := RegisterRoutes(Deps{
		Mux:               mux,
		Tokens:            tokenAuth,
		Users:             fakeUsers{user: user},
		Statuses:          statuses,
		Workflows:         fakeCatalogs{},
		Configuration:     configuration,
		Catalog:           catalog,
		CatalogMutations:  fakeCatalogMutations{},
		Workspaces:        fakeWorkspaces{},
		ItemTemplates:     fakeItemTemplates{},
		Labels:            labels,
		Participants:      fakeParticipants{},
		Items:             items,
		Access:            access,
		Preferences:       preferences,
		Recurrence:        recurrence,
		ItemDiagrams:      itemDiagrams,
		Pages:             pages,
		PageApplication:   pageApplicationDependency,
		PageDiagrams:      pageDiagrams,
		PageAccess:        pageAccessDependency,
		PageLabels:        pageLabels,
		CannedResponses:   fakeCannedResponses{},
		PagePublication:   services.NewKnowledgePublicationService(nil),
		Worklogs:          worklogs,
		TimeAccess:        timeAccessDependency,
		TimeProjects:      fakeTimeProjects{},
		Timers:            fakeTimers{},
		SystemAdmins:      fakeSystemAdmins{},
		Groups:            fakeGroups{},
		AdminUsers:        fakeAdminUsers{user: user},
		AuditLogs:         repository.NewAuditLogRepository(nil),
		AdminTokens:       &auth.TokenManager{},
		AdminAuditor:      logger.NewAuditor(nil),
		AdminTranslations: objecttranslation.NewService(nil),
		Comments:          fakeComments{},
		CommentAccess:     fakeAccess{},
		Attachments:       fakeAttachments{},
		PageAttachments:   fakePageAttachments{},
		Collections:       fakeCollections{},
		Planning:          fakePlanning{},
		Links:             fakeLinks{},
		AgentRuns:         fakeAgentRuns{},
		AgentSkills:       fakeAgentSkills{},
		ConditionSets:     fakeConditionSets{},
		Governance:        fakeGovernance{},
		Actions:           fakeActions{},
		TestManagement:    services.NewTestManagementApplicationService(nil, nil),
		Assets:            services.NewAssetApplicationService(nil, nil, nil, nil),
		ItemApplication:   services.NewItemApplicationService(nil, nil, nil, nil, nil),
		ItemDetail:        services.NewItemDetailApplicationService(nil, nil, nil, nil),
		SessionMiddleware: sessionMiddleware,
		CORS:              NewCORS([]string{"https://app.example.test"}, false, true),
		Concurrency:       fakeConcurrency{allow: true},
	})
	if err != nil {
		t.Fatalf("register routes: %v", err)
	}
	return mux, user
}

func httpMethodKey(method string) string {
	switch method {
	case http.MethodGet:
		return "get"
	case http.MethodPost:
		return "post"
	case http.MethodPut:
		return "put"
	case http.MethodPatch:
		return "patch"
	case http.MethodDelete:
		return "delete"
	default:
		return method
	}
}

func (fakeCatalogs) GetEffectiveConfig(int, int, *int) (*services.EffectiveConfigResult, error) {
	return nil, errFakeToken
}
