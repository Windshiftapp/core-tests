//go:build test

package handlers

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/jira"
)

type fakeJiraServiceManagementClient struct {
	jira.Client
	organizationErr error
}

func (c *fakeJiraServiceManagementClient) GetProject(context.Context, string) (*jira.JiraProject, error) {
	return &jira.JiraProject{ID: "10000", Key: "HELP", Name: "Help Desk", ProjectType: "service_desk"}, nil
}

func (c *fakeJiraServiceManagementClient) ListServiceDesks(context.Context) ([]jira.JiraServiceDesk, error) {
	return []jira.JiraServiceDesk{{ID: "1", ProjectID: "10000", ProjectKey: "HELP"}}, nil
}

func (c *fakeJiraServiceManagementClient) ListServiceDeskRequestTypes(context.Context, string) ([]jira.JiraServiceDeskRequestType, error) {
	return []jira.JiraServiceDeskRequestType{{
		ID: "1", Name: "Get help", IssueTypeID: "10003", ServiceDeskID: "1", RestrictionStatus: "OPEN",
	}}, nil
}

func (c *fakeJiraServiceManagementClient) ListServiceDeskOrganizations(context.Context, string) ([]jira.JiraServiceDeskOrganization, error) {
	return nil, c.organizationErr
}

func (c *fakeJiraServiceManagementClient) ListServiceDeskOrganizationUsers(context.Context, string) ([]jira.JiraUser, error) {
	return nil, nil
}

// TestPrepareJiraServiceManagementImportDegradesWhenOrganizationsUnavailable is
// the WI-2074 regression: a rejected experimental organization read must not
// abort the project's core issue import.
func TestPrepareJiraServiceManagementImportDegradesWhenOrganizationsUnavailable(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	h := NewJiraImportHandler(db, "jira-jsm-degradation-secret", "", blobstore.Disabled())

	var workspaceID int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, active) VALUES ('Help Desk', 'HELP', true) RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	var itemTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("find item type: %v", err)
	}

	client := &fakeJiraServiceManagementClient{organizationErr: errors.New("X-ExperimentalApi header required")}
	jsm, err := h.prepareJiraServiceManagementImport(
		context.Background(),
		"job-jsm",
		"HELP",
		workspaceID,
		map[string]int{"10003": itemTypeID},
		client,
		0,
		true,
	)
	if err != nil {
		t.Fatalf("prepareJiraServiceManagementImport() error = %v, want graceful degradation", err)
	}
	if jsm == nil {
		t.Fatal("prepareJiraServiceManagementImport() returned nil import, want the request-type catalog")
	}
	if len(jsm.OrganizationCustomers) != 0 {
		t.Fatalf("OrganizationCustomers = %#v, want none when organization metadata is unavailable", jsm.OrganizationCustomers)
	}
	if len(jsm.RequestTypes) != 1 {
		t.Fatalf("RequestTypes = %#v, want the discovered request type retained", jsm.RequestTypes)
	}
}
