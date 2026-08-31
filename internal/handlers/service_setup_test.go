//go:build test

package handlers

import (
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// serviceSetup creates fixture state below the HTTP layer. Handler tests must
// not call another handler to prepare the behavior under test: doing so
// bypasses route middleware while coupling fixtures to unrelated handlers.
// Prefer a production service and fall back to the owning repository only
// when the domain has no create service.
type serviceSetup struct {
	t   *testing.T
	tdb *testutils.TestDB
}

func newServiceSetup(t *testing.T, tdb *testutils.TestDB) *serviceSetup {
	t.Helper()
	return &serviceSetup{t: t, tdb: tdb}
}

// GrantGlobal uses the permission repository because global grant mutation is
// not currently exposed by PermissionService. The fixture is created before
// the test's permission service is constructed, so no cache invalidation is
// required here.
func (s *serviceSetup) GrantGlobal(userID int, permissionKey string) {
	s.t.Helper()
	repo := repository.NewPermissionRepository(s.tdb.GetDatabase())
	permissions, err := repo.ListAll()
	if err != nil {
		s.t.Fatalf("list permissions: %v", err)
	}
	for _, permission := range permissions {
		if permission.PermissionKey != permissionKey {
			continue
		}
		if err := repo.GrantGlobalToUser(userID, permission.ID, 1); err != nil {
			s.t.Fatalf("grant permission %q: %v", permissionKey, err)
		}
		return
	}
	s.t.Fatalf("permission %q not found", permissionKey)
}

func (s *serviceSetup) CreateWorkspace(name, key string) int {
	s.t.Helper()
	result, err := services.NewWorkspaceService(s.tdb.GetDatabase()).Create(s.t.Context(), services.CreateWorkspaceParams{
		Name:        name,
		Key:         key,
		Description: "Test workspace",
		CreatorID:   1,
	})
	if err != nil {
		s.t.Fatalf("create workspace: %v", err)
	}
	return result.Workspace.ID
}

func (s *serviceSetup) CreateWorkflow(name string) int {
	s.t.Helper()
	id, err := repository.NewWorkflowRepository(s.tdb.GetDatabase()).Create(name, "Test workflow", false)
	if err != nil {
		s.t.Fatalf("create workflow: %v", err)
	}
	return id
}

// Screens do not yet have a create service/repository contract. Keep this
// narrow fixture write here until that production operation is extracted.
func (s *serviceSetup) CreateScreen(name string) int {
	s.t.Helper()
	var id int
	if err := s.tdb.QueryRow(`
		INSERT INTO screens (name, description, created_at, updated_at)
		VALUES (?, 'Test screen', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, name).Scan(&id); err != nil {
		s.t.Fatalf("create screen fixture: %v", err)
	}
	return id
}

func (s *serviceSetup) CreateConfigurationSet(cs models.ConfigurationSet) int {
	s.t.Helper()
	id, err := repository.NewConfigurationSetRepository(s.tdb.GetDatabase()).CreateFull(&cs)
	if err != nil {
		s.t.Fatalf("create configuration set: %v", err)
	}
	return int(id)
}

func (s *serviceSetup) CreateLabel(_ int, name string) models.Label {
	s.t.Helper()
	repo := repository.NewLabelRepository(s.tdb.GetDatabase())
	id, createdAt, err := repo.Create(name, "#3B82F6")
	if err != nil {
		s.t.Fatalf("create label: %v", err)
	}
	return models.Label{
		ID:        int(id),
		Name:      name,
		Color:     "#3B82F6",
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
}

func (s *serviceSetup) CreateStatusCategory(name, color string) int {
	s.t.Helper()
	entity, err := services.NewEnumService(
		s.tdb.GetDatabase(),
		services.NewStatusCategoryConfig(),
	).Create(&models.StatusCategory{
		Name:        name,
		Color:       color,
		Description: "Test status category",
	}, nil)
	if err != nil {
		s.t.Fatalf("create status category: %v", err)
	}
	category, ok := entity.(*models.StatusCategory)
	if !ok {
		s.t.Fatalf("create status category returned %T", entity)
	}
	return category.ID
}

func (s *serviceSetup) CreateAction(workspaceID int, name string) int {
	s.t.Helper()
	creatorID := 1
	id, err := repository.NewActionRepository(s.tdb.GetDatabase()).Create(&models.Action{
		WorkspaceID: workspaceID,
		Name:        name,
		Description: "Test action",
		IsEnabled:   true,
		TriggerType: models.ActionTriggerManual,
		CreatedBy:   &creatorID,
	})
	if err != nil {
		s.t.Fatalf("create action: %v", err)
	}
	return id
}

func (s *serviceSetup) CreateUser(email, username, firstName, lastName string) int {
	s.t.Helper()
	id, err := repository.NewUserRepository(s.tdb.GetDatabase()).Create(repository.CreateUserParams{
		Email:         email,
		Username:      username,
		FirstName:     firstName,
		LastName:      lastName,
		IsActive:      true,
		EmailVerified: true,
	})
	if err != nil {
		s.t.Fatalf("create user: %v", err)
	}
	return int(id)
}

func (s *serviceSetup) CreateCustomer(name string) int {
	s.t.Helper()
	customer := &models.CustomerOrganisation{Name: name, Active: true}
	id, _, err := repository.NewCustomerOrganisationRepository(s.tdb.GetDatabase()).Create(customer)
	if err != nil {
		s.t.Fatalf("create customer: %v", err)
	}
	return id
}

func (s *serviceSetup) CreateTimeProject(name string, customerID int) int {
	s.t.Helper()
	project := &models.TimeProject{
		Name:   name,
		Status: "Active",
		Color:  "#3B82F6",
	}
	if customerID != 0 {
		project.CustomerID = &customerID
	}
	if err := repository.NewTimeProjectRepository(s.tdb.GetDatabase()).Create(project); err != nil {
		s.t.Fatalf("create time project: %v", err)
	}
	return project.ID
}

func (s *serviceSetup) CreateLinkType(name, forwardLabel, reverseLabel string) int {
	s.t.Helper()
	linkType := &models.LinkType{
		Name:         name,
		ForwardLabel: forwardLabel,
		ReverseLabel: reverseLabel,
		Color:        "#64748B",
	}
	id, _, err := repository.NewLinkTypeRepository(s.tdb.GetDatabase()).Create(linkType)
	if err != nil {
		s.t.Fatalf("create link type: %v", err)
	}
	return id
}
