//go:build test

package handlers

import (
	"context"
	"reflect"
	"testing"

	"windshift/internal/jira"
)

// fakeServiceDeskRoleClient serves JSM project roles and group members so the
// Data Center customer classification can be exercised without a live Jira.
type fakeServiceDeskRoleClient struct {
	jira.Client
	roles   map[string][]jira.JiraProjectRole
	members map[string][]jira.JiraGroupMember
}

func (f *fakeServiceDeskRoleClient) ListGroups(context.Context) ([]jira.JiraGroup, error) {
	return nil, nil
}

func (f *fakeServiceDeskRoleClient) GetProjectRoles(_ context.Context, projectKey string) ([]jira.JiraProjectRole, error) {
	return f.roles[projectKey], nil
}

func (f *fakeServiceDeskRoleClient) GetGroupMembers(_ context.Context, groupName string) ([]jira.JiraGroupMember, error) {
	return f.members[groupName], nil
}

// TestResolveJiraServiceDeskCustomerIdentities covers the WI-2075 evidence
// source: Data Center has no accountType, so the JSM project roles decide who
// is a customer.
func TestResolveJiraServiceDeskCustomerIdentities(t *testing.T) {
	t.Parallel()

	client := &fakeServiceDeskRoleClient{
		roles: map[string][]jira.JiraProjectRole{
			"WSSD": {
				{
					Name: "Service Desk Customers",
					Actors: []jira.JiraRoleActor{
						{Type: "atlassian-user-role-actor", Name: "portaluser"},
						{Type: "atlassian-group-role-actor", Name: "sd-customers"},
					},
				},
				{
					Name: "Service Desk Team",
					Actors: []jira.JiraRoleActor{
						{Type: "atlassian-user-role-actor", Name: "agent"},
						{Type: "atlassian-group-role-actor", Name: "sd-agents"},
					},
				},
				{Name: "Administrators", Actors: []jira.JiraRoleActor{{Type: "atlassian-user-role-actor", Name: "admin"}}},
			},
		},
		members: map[string][]jira.JiraGroupMember{
			"sd-customers": {{Name: "groupcustomer"}},
			"sd-agents":    {{Name: "groupagent"}},
		},
	}

	got := resolveJiraServiceDeskCustomerIdentities(context.Background(), "WSSD", client)
	want := map[string]struct{}{"portaluser": {}, "groupcustomer": {}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("customer identities = %#v, want %#v", got, want)
	}
}

// TestResolveJiraServiceDeskCustomerIdentitiesAgentsWinDualRoles pins the
// deterministic policy: a genuine agent stays internal even when also granted
// the customers role.
func TestResolveJiraServiceDeskCustomerIdentitiesAgentsWinDualRoles(t *testing.T) {
	t.Parallel()

	client := &fakeServiceDeskRoleClient{
		roles: map[string][]jira.JiraProjectRole{
			"WSSD": {
				{Name: "Service Desk Customers", Actors: []jira.JiraRoleActor{{Type: "atlassian-user-role-actor", Name: "dualrole"}}},
				{Name: "Service Desk Team", Actors: []jira.JiraRoleActor{{Type: "atlassian-user-role-actor", Name: "dualrole"}}},
			},
		},
	}

	got := resolveJiraServiceDeskCustomerIdentities(context.Background(), "WSSD", client)
	if len(got) != 0 {
		t.Fatalf("customer identities = %#v, want none when the identity is also an agent", got)
	}
}

// TestResolveJiraServiceDeskCustomerIdentitiesWithoutRoleClient proves the
// feature degrades to Cloud accountType behaviour rather than guessing.
func TestResolveJiraServiceDeskCustomerIdentitiesWithoutRoleClient(t *testing.T) {
	t.Parallel()

	got := resolveJiraServiceDeskCustomerIdentities(context.Background(), "WSSD", &fakeJiraFilterClient{})
	if got != nil {
		t.Fatalf("customer identities = %#v, want nil when roles are unavailable", got)
	}
}
