package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/database"
	"windshift/internal/jira"
	"windshift/internal/models"
)

func newJiraServiceManagementTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-service-management.db"))
	if err != nil {
		t.Fatalf("create SQLite database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize database: %v", err)
	}
	insertJiraImportConnection(t, db, "conn-jsm")
	insertJiraImportJob(t, db, "job-jsm", "conn-jsm", "running", []string{"HELP"})
	return db
}

func TestJiraPortalCustomerClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		accountID   string
		accountType string
		want        bool
	}{
		{name: "explicit customer account type", accountID: "abc", accountType: "customer", want: true},
		{name: "portal-only account ID", accountID: "qm:tenant:customer", want: true},
		{name: "portal-only account ID ignores case", accountID: "QM:tenant:customer", want: true},
		{name: "Atlassian user", accountID: "abc", accountType: "atlassian", want: false},
		{name: "application user", accountID: "abc", accountType: "app", want: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := jiraIsPortalCustomer(test.accountID, test.accountType); got != test.want {
				t.Fatalf("jiraIsPortalCustomer(%q, %q) = %t, want %t", test.accountID, test.accountType, got, test.want)
			}
		})
	}
}

func TestJiraUserIdentityMetadataPreservesPortalCustomerFallback(t *testing.T) {
	t.Parallel()

	identity := jiraUserIdentityMetadata(&jira.JiraUser{
		AccountID:    "qm:tenant:customer",
		AccountType:  "customer",
		DisplayName:  "Portal Customer",
		EmailAddress: "customer@example.invalid",
	})
	if identity["account_id"] != "qm:tenant:customer" ||
		identity["account_type"] != "customer" ||
		identity["display_name"] != "Portal Customer" ||
		identity["email"] != "customer@example.invalid" {
		t.Fatalf("identity metadata = %#v, want complete Jira customer identity", identity)
	}
}

func TestSplitJiraImportUsersKeepsAllObservedPortalCustomersOutOfInternalUsers(t *testing.T) {
	t.Parallel()

	users := make([]JiraUserSummary, 0, 247)
	for index := 0; index < 246; index++ {
		users = append(users, JiraUserSummary{
			AccountID:   fmt.Sprintf("qm:tenant:customer-%d", index),
			AccountType: "customer",
		})
	}
	users = append(users, JiraUserSummary{
		AccountID:   "atlassian-user",
		AccountType: "atlassian",
	})

	internalUsers, portalCustomers := splitJiraImportUsers(users)
	if len(portalCustomers) != 246 {
		t.Fatalf("portal customer count = %d, want 246", len(portalCustomers))
	}
	if len(internalUsers) != 1 || internalUsers[0].AccountID != "atlassian-user" {
		t.Fatalf("internal users = %#v, want only Atlassian user", internalUsers)
	}
}

func TestEnsurePortalCustomersImportsAllObservedCustomersWithoutInternalUsers(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	h := NewJiraImportHandler(db, "jira-jsm-all-customers-test", "", blobstore.Disabled())

	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Help Portal', 'portal', 'inbound', 'enabled', '{}') RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("create portal channel: %v", err)
	}

	customers := make([]JiraUserSummary, 0, 246)
	for index := 0; index < 246; index++ {
		customers = append(customers, JiraUserSummary{
			AccountID:   fmt.Sprintf("qm:tenant:customer-%d", index),
			AccountType: "customer",
			Email:       fmt.Sprintf("customer-%d@example.invalid", index),
			DisplayName: fmt.Sprintf("Portal Customer %d", index),
		})
	}
	customerMap, err := h.ensurePortalCustomers("job-jsm", channelID, customers, nil)
	if err != nil {
		t.Fatalf("ensure all observed portal customers: %v", err)
	}
	if len(customerMap) != 246 {
		t.Fatalf("portal customer mapping count = %d, want 246", len(customerMap))
	}

	var portalCustomerCount, channelAccessCount, matchingInternalUserCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM portal_customers WHERE email LIKE 'customer-%@example.invalid'
	`).Scan(&portalCustomerCount); err != nil {
		t.Fatalf("count portal customers: %v", err)
	}
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM portal_customer_channels WHERE channel_id = ?
	`, channelID).Scan(&channelAccessCount); err != nil {
		t.Fatalf("count portal customer channel access: %v", err)
	}
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM users WHERE email LIKE 'customer-%@example.invalid'
	`).Scan(&matchingInternalUserCount); err != nil {
		t.Fatalf("count incorrectly imported internal users: %v", err)
	}
	if portalCustomerCount != 246 || channelAccessCount != 246 || matchingInternalUserCount != 0 {
		t.Fatalf("portal customers/access/internal users = %d/%d/%d, want 246/246/0",
			portalCustomerCount, channelAccessCount, matchingInternalUserCount)
	}
}

func TestJiraRequestTypeIDUsesServiceManagementOriginField(t *testing.T) {
	t.Parallel()

	issue := &jira.JiraIssue{Fields: jira.JiraIssueFields{CustomFields: map[string]interface{}{
		"customfield_10010": map[string]interface{}{
			"requestType": map[string]interface{}{"id": "17"},
		},
	}}}
	got := jiraRequestTypeID(issue, []CustomFieldMapping{{
		JiraID:   "customfield_10010",
		JiraName: "Request Type",
		JiraType: jiraRequestTypeFieldType,
	}})
	if got != "17" {
		t.Fatalf("jiraRequestTypeID() = %q, want 17", got)
	}
}

func TestEnsureJiraPortalCreatesRequestTypesBeforeCustomers(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	h := NewJiraImportHandler(db, "jira-jsm-import-test-secret-32", "", blobstore.Disabled())

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

	channelID, err := h.ensureJiraPortal(context.Background(), "job-jsm",
		jira.JiraProject{ID: "10000", Key: "HELP", Name: "Help Desk", ProjectType: "service_desk"},
		jira.JiraServiceDesk{ID: "1", ProjectID: "10000", ProjectKey: "HELP"},
		workspaceID, 0)
	if err != nil {
		t.Fatalf("ensure Jira portal: %v", err)
	}
	requestTypeMap, err := h.ensureJiraRequestTypes("job-jsm", channelID, workspaceID, []jira.JiraServiceDeskRequestType{
		{ID: "1", Name: "Get help", IssueTypeID: "10003", ServiceDeskID: "1", RestrictionStatus: "OPEN"},
		{ID: "2", Name: "Report a bug", IssueTypeID: "10003", ServiceDeskID: "1", RestrictionStatus: "OPEN"},
	}, map[string]int{"10003": itemTypeID})
	if err != nil {
		t.Fatalf("ensure Jira request types: %v", err)
	}
	if err := h.ensureJiraPortalRequestTypeSection(channelID, requestTypeMap); err != nil {
		t.Fatalf("ensure Jira portal request-type section: %v", err)
	}
	repeatedChannelID, err := h.ensureJiraPortal(context.Background(), "job-jsm",
		jira.JiraProject{ID: "10000", Key: "HELP", Name: "Help Desk", ProjectType: "service_desk"},
		jira.JiraServiceDesk{ID: "1", ProjectID: "10000", ProjectKey: "HELP"},
		workspaceID, 0)
	if err != nil {
		t.Fatalf("repeat Jira portal import: %v", err)
	}
	repeatedRequestTypes, err := h.ensureJiraRequestTypes(
		"job-jsm", repeatedChannelID, workspaceID, []jira.JiraServiceDeskRequestType{
			{ID: "1", Name: "Get help", IssueTypeID: "10003", ServiceDeskID: "1", RestrictionStatus: "OPEN"},
			{ID: "2", Name: "Report a bug", IssueTypeID: "10003", ServiceDeskID: "1", RestrictionStatus: "OPEN"},
		}, map[string]int{"10003": itemTypeID},
	)
	if err != nil {
		t.Fatalf("repeat Jira request-type import: %v", err)
	}
	if repeatedChannelID != channelID ||
		repeatedRequestTypes["1"] != requestTypeMap["1"] ||
		repeatedRequestTypes["2"] != requestTypeMap["2"] {
		t.Fatalf("repeat import IDs = channel %d request types %#v, want %d/%#v",
			repeatedChannelID, repeatedRequestTypes, channelID, requestTypeMap)
	}

	var channelType, status, configJSON string
	if err := db.QueryRow(`SELECT type, status, config FROM channels WHERE id = ?`, channelID).
		Scan(&channelType, &status, &configJSON); err != nil {
		t.Fatalf("load imported portal: %v", err)
	}
	if channelType != "portal" || status != "enabled" {
		t.Fatalf("imported channel = type %q status %q, want portal/enabled", channelType, status)
	}
	var config models.ChannelConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		t.Fatalf("decode imported portal config: %v", err)
	}
	if config.PortalSlug != "jira-help" || len(config.PortalSections) != 1 ||
		len(config.PortalSections[0].RequestTypeIDs) != 2 {
		t.Fatalf("imported portal config = %#v, want slug and two request types", config)
	}
	var requestTypeCount, requestFieldCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM request_types WHERE channel_id = ?`, channelID).Scan(&requestTypeCount); err != nil {
		t.Fatalf("count request types: %v", err)
	}
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM request_type_fields
		WHERE request_type_id IN (SELECT id FROM request_types WHERE channel_id = ?)
	`, channelID).Scan(&requestFieldCount); err != nil {
		t.Fatalf("count request-type fields: %v", err)
	}
	if requestTypeCount != 2 || requestFieldCount != 4 {
		t.Fatalf("request types/fields = %d/%d, want 2/4", requestTypeCount, requestFieldCount)
	}

	customerMap, err := h.ensurePortalCustomers("job-jsm", channelID, []JiraUserSummary{{
		AccountID:   "qm:tenant:customer",
		AccountType: "customer",
		Email:       "customer@example.invalid",
		DisplayName: "Portal Customer",
	}}, nil)
	if err != nil {
		t.Fatalf("ensure portal customers: %v", err)
	}
	customerID := customerMap["qm:tenant:customer"]
	if customerID == 0 {
		t.Fatal("portal customer mapping is missing")
	}
	var accessCount, internalUserCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM portal_customer_channels
		WHERE portal_customer_id = ? AND channel_id = ?
	`, customerID, channelID).Scan(&accessCount); err != nil {
		t.Fatalf("count portal access rows: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE LOWER(email) = LOWER('customer@example.invalid')`).Scan(&internalUserCount); err != nil {
		t.Fatalf("count internal users: %v", err)
	}
	if accessCount != 1 || internalUserCount != 0 {
		t.Fatalf("portal access/internal users = %d/%d, want 1/0", accessCount, internalUserCount)
	}
	if _, err := h.ensurePortalCustomers("job-jsm", channelID, []JiraUserSummary{{
		AccountID:   "qm:tenant:customer",
		AccountType: "customer",
		Email:       "customer@example.invalid",
		DisplayName: "Portal Customer",
	}}, nil); err != nil {
		t.Fatalf("repeat portal-customer import: %v", err)
	}
	var customerCount, roleCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM portal_customers WHERE LOWER(email) = LOWER('customer@example.invalid')`).Scan(&customerCount); err != nil {
		t.Fatalf("count repeated portal customers: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM portal_customer_roles WHERE portal_customer_id = ?`, customerID).Scan(&roleCount); err != nil {
		t.Fatalf("count repeated portal customer roles: %v", err)
	}
	if customerCount != 1 || roleCount != 1 {
		t.Fatalf("repeat portal customer count/roles = %d/%d, want 1/1", customerCount, roleCount)
	}
	var portalCustomerRoleID int
	if err := db.QueryRow(`SELECT id FROM contact_roles WHERE name = 'Portal Customer'`).Scan(&portalCustomerRoleID); err != nil {
		t.Fatalf("load portal customer role: %v", err)
	}
	for _, mapping := range []struct {
		entityType string
		jiraID     string
	}{
		{entityType: "request_type", jiraID: "1"},
		{entityType: "portal_customer_channel", jiraID: "qm:tenant:customer:" + strconv.Itoa(channelID)},
		{entityType: "portal_customer_role", jiraID: "qm:tenant:customer:" + strconv.Itoa(portalCustomerRoleID)},
	} {
		var metadataJSON string
		if err := db.QueryRow(`
			SELECT metadata_json FROM jira_import_id_mappings
			WHERE job_id = 'job-jsm' AND entity_type = ? AND jira_id = ?
		`, mapping.entityType, mapping.jiraID).Scan(&metadataJSON); err != nil {
			t.Fatalf("load repeated %s mapping: %v", mapping.entityType, err)
		}
		if !jiraImportMappingWasCreated(sql.NullString{String: metadataJSON, Valid: true}) {
			t.Fatalf("repeated %s mapping lost original ownership: %s", mapping.entityType, metadataJSON)
		}
	}

	var secondChannelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Second Help Portal', 'portal', 'inbound', 'enabled', '{}') RETURNING id
	`).Scan(&secondChannelID); err != nil {
		t.Fatalf("create second portal: %v", err)
	}
	if _, err := h.ensurePortalCustomers("job-jsm", secondChannelID, []JiraUserSummary{{
		AccountID:   "qm:tenant:customer",
		AccountType: "customer",
		Email:       "customer@example.invalid",
		DisplayName: "Portal Customer",
	}}, nil); err != nil {
		t.Fatalf("grant existing imported customer second portal access: %v", err)
	}
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM portal_customer_channels WHERE portal_customer_id = ?
	`, customerID).Scan(&accessCount); err != nil {
		t.Fatalf("count multi-portal customer access: %v", err)
	}
	if accessCount != 2 {
		t.Fatalf("multi-portal customer access = %d, want 2", accessCount)
	}
}

func TestImportJiraServiceManagementRequestUsesPortalCustomerAttribution(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	h := NewJiraImportHandler(db, "jira-jsm-attribution-test-secret", "", blobstore.Disabled())

	var workspaceID, itemTypeID, statusID int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, active) VALUES ('Help Desk', 'HELP', true) RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM item_types ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("find item type: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM statuses ORDER BY id LIMIT 1`).Scan(&statusID); err != nil {
		t.Fatalf("find status: %v", err)
	}
	channelID, err := h.ensureJiraPortal(context.Background(), "job-jsm",
		jira.JiraProject{ID: "10000", Key: "HELP", Name: "Help Desk", ProjectType: "service_desk"},
		jira.JiraServiceDesk{ID: "1", ProjectID: "10000", ProjectKey: "HELP"},
		workspaceID, 0)
	if err != nil {
		t.Fatalf("ensure portal: %v", err)
	}
	requestTypes, err := h.ensureJiraRequestTypes("job-jsm", channelID, workspaceID, []jira.JiraServiceDeskRequestType{{
		ID: "1", Name: "Get help", IssueTypeID: "10003", ServiceDeskID: "1", RestrictionStatus: "OPEN",
	}}, map[string]int{"10003": itemTypeID})
	if err != nil {
		t.Fatalf("ensure request type: %v", err)
	}
	customerMap, err := h.ensurePortalCustomers("job-jsm", channelID, []JiraUserSummary{{
		AccountID: "qm:tenant:customer", Email: "customer@example.invalid", DisplayName: "Portal Customer",
	}}, nil)
	if err != nil {
		t.Fatalf("ensure portal customer: %v", err)
	}
	customerID := customerMap["qm:tenant:customer"]

	customer := &jira.JiraUser{
		AccountID: "qm:tenant:customer", AccountType: "customer",
		EmailAddress: "customer@example.invalid", DisplayName: "Portal Customer",
	}
	serviceDeskPublic := false
	issue := &jira.JiraIssue{
		ID:  "10001",
		Key: "HELP-1",
		Fields: jira.JiraIssueFields{
			Summary:   "Need assistance",
			Project:   &jira.JiraProject{Key: "HELP"},
			IssueType: &jira.JiraIssueType{ID: "10003"},
			Status:    &jira.JiraStatus{ID: "1"},
			Reporter:  customer,
			Creator:   customer,
			CustomFields: map[string]interface{}{
				"customfield_10010": map[string]interface{}{
					"requestType": map[string]interface{}{"id": "1"},
				},
			},
			Comment: &jira.JiraCommentContainer{Comments: []jira.JiraComment{{
				ID: "20001", Author: customer, Body: "Customer reply", ServiceDeskPublic: &serviceDeskPublic,
			}}},
		},
	}
	jsmImport := &jiraServiceManagementImport{
		ServiceDeskID: "1", ChannelID: channelID, RequestTypes: requestTypes,
	}
	im := &jiraImportContext{
		jobID:             "job-jsm",
		progress:          &ImportProgress{},
		workspaceID:       workspaceID,
		statusMap:         map[string]int{"1": statusID},
		itemTypeMap:       map[string]int{"10003": itemTypeID},
		userMap:           map[string]int{},
		usernameMap:       map[string]string{},
		portalCustomerMap: customerMap,
		versionMap:        map[string]int{},
		iterationMap:      map[string]int{},
		customFieldIDMap:  map[string]int{},
		choiceOptionIDs:   map[string]map[string]int{},
		customFieldMappings: []CustomFieldMapping{{
			JiraID: "customfield_10010", JiraName: "Request Type", JiraType: jiraRequestTypeFieldType,
		}},
		jsmImport: jsmImport,
	}
	err = h.importIssue(context.Background(), im, issue)
	if err != nil {
		t.Fatalf("import JSM request: %v", err)
	}

	var creatorCustomerID, importedChannelID, importedRequestTypeID sql.NullInt64
	var reporterID, creatorID sql.NullInt64
	var customFieldValuesJSON string
	if err := db.QueryRow(`
		SELECT creator_portal_customer_id, channel_id, request_type_id, reporter_id, creator_id, custom_field_values
		FROM items WHERE title = 'Need assistance'
	`).Scan(&creatorCustomerID, &importedChannelID, &importedRequestTypeID, &reporterID, &creatorID, &customFieldValuesJSON); err != nil {
		t.Fatalf("load imported request: %v", err)
	}
	if !creatorCustomerID.Valid || int(creatorCustomerID.Int64) != customerID ||
		!importedChannelID.Valid || int(importedChannelID.Int64) != channelID ||
		!importedRequestTypeID.Valid || int(importedRequestTypeID.Int64) != requestTypes["1"] ||
		reporterID.Valid || creatorID.Valid {
		t.Fatalf("imported request attribution = customer %v channel %v request type %v reporter %v creator %v",
			creatorCustomerID, importedChannelID, importedRequestTypeID, reporterID, creatorID)
	}
	var customFieldValues map[string]any
	if err := json.Unmarshal([]byte(customFieldValuesJSON), &customFieldValues); err != nil {
		t.Fatalf("decode imported request metadata: %v", err)
	}
	reporter, ok := customFieldValues["_jira_reporter"].(map[string]any)
	if !ok || reporter["account_id"] != "qm:tenant:customer" ||
		reporter["account_type"] != "customer" ||
		reporter["display_name"] != "Portal Customer" {
		t.Fatalf("imported Jira reporter fallback = %#v, want portal customer identity", reporter)
	}

	var commentPortalCustomerID, commentAuthorID sql.NullInt64
	var commentIsPrivate bool
	if err := db.QueryRow(`
		SELECT portal_customer_id, author_id, is_private FROM comments
		WHERE item_id = (SELECT id FROM items WHERE title = 'Need assistance')
	`).Scan(&commentPortalCustomerID, &commentAuthorID, &commentIsPrivate); err != nil {
		t.Fatalf("load imported customer comment: %v", err)
	}
	if !commentPortalCustomerID.Valid || int(commentPortalCustomerID.Int64) != customerID || commentAuthorID.Valid || !commentIsPrivate {
		t.Fatalf("imported comment attribution = portal customer %v author %v private %t", commentPortalCustomerID, commentAuthorID, commentIsPrivate)
	}
}

type fakeJiraCustomerOrganizationClient struct {
	organizations []jira.JiraServiceDeskOrganization
	users         map[string][]jira.JiraUser
}

func (f fakeJiraCustomerOrganizationClient) ListServiceDeskOrganizations(context.Context, string) ([]jira.JiraServiceDeskOrganization, error) {
	return f.organizations, nil
}

func (f fakeJiraCustomerOrganizationClient) ListServiceDeskOrganizationUsers(_ context.Context, organizationID string) ([]jira.JiraUser, error) {
	return f.users[organizationID], nil
}

func TestEnsureJiraCustomerOrganizationsCreatesOrganizationBeforeCustomer(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	h := NewJiraImportHandler(db, "jira-jsm-organization-test-secret", "", blobstore.Disabled())

	client := fakeJiraCustomerOrganizationClient{
		organizations: []jira.JiraServiceDeskOrganization{{
			ID: "11", Name: "Example Customer", UUID: "organization-uuid",
		}},
		users: map[string][]jira.JiraUser{
			"11": {{
				AccountID: "qm:tenant:organization-customer", EmailAddress: "org-customer@example.invalid",
				DisplayName: "Organization Customer",
			}},
		},
	}
	customers, customerOrganizations, err := h.ensureJiraCustomerOrganizations(
		context.Background(), "job-jsm", "HELP", "1", client, true,
	)
	if err != nil {
		t.Fatalf("ensure Jira customer organizations: %v", err)
	}
	if len(customers) != 1 || customerOrganizations["qm:tenant:organization-customer"] == 0 {
		t.Fatalf("discovered customers/organizations = %#v/%#v", customers, customerOrganizations)
	}

	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Help Portal', 'portal', 'inbound', 'enabled', '{}') RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("create portal channel: %v", err)
	}
	customerMap, err := h.ensurePortalCustomers(
		"job-jsm", channelID, customers, customerOrganizations,
	)
	if err != nil {
		t.Fatalf("ensure organization portal customers: %v", err)
	}

	var customerOrganizationID sql.NullInt64
	if err := db.QueryRow(`
		SELECT customer_organisation_id FROM portal_customers WHERE id = ?
	`, customerMap["qm:tenant:organization-customer"]).Scan(&customerOrganizationID); err != nil {
		t.Fatalf("load organization customer: %v", err)
	}
	if !customerOrganizationID.Valid ||
		int(customerOrganizationID.Int64) != customerOrganizations["qm:tenant:organization-customer"] {
		t.Fatalf("portal customer organization = %v, want %d", customerOrganizationID,
			customerOrganizations["qm:tenant:organization-customer"])
	}
}

func TestEnsureJiraCustomerOrganizationsDiscoversCustomersWithoutCreatingOrganizations(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	h := NewJiraImportHandler(db, "jira-jsm-customer-discovery-test", "", blobstore.Disabled())

	client := fakeJiraCustomerOrganizationClient{
		organizations: []jira.JiraServiceDeskOrganization{{
			ID: "11", Name: "Example Customer",
		}},
		users: map[string][]jira.JiraUser{
			"11": {{
				AccountID: "qm:tenant:organization-customer", AccountType: "customer",
				EmailAddress: "org-customer@example.invalid", DisplayName: "Organization Customer",
			}},
		},
	}
	customers, customerOrganizations, err := h.ensureJiraCustomerOrganizations(
		context.Background(), "job-jsm", "HELP", "1", client, false,
	)
	if err != nil {
		t.Fatalf("discover Jira customer organizations: %v", err)
	}
	if len(customers) != 1 || customers[0].AccountID != "qm:tenant:organization-customer" {
		t.Fatalf("discovered customers = %#v, want organization member", customers)
	}
	if len(customerOrganizations) != 0 {
		t.Fatalf("customer organization assignments = %#v, want none", customerOrganizations)
	}
	var organizationCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM customer_organisations WHERE builtin_key IS NULL`).Scan(&organizationCount); err != nil {
		t.Fatalf("count customer organizations: %v", err)
	}
	if organizationCount != 0 {
		t.Fatalf("customer organization count = %d, want 0", organizationCount)
	}
	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Help Portal', 'portal', 'inbound', 'enabled', '{}') RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("create portal channel: %v", err)
	}
	customerMap, err := h.ensurePortalCustomers("job-jsm", channelID, customers, customerOrganizations)
	if err != nil {
		t.Fatalf("import discovered portal customers: %v", err)
	}
	var customerOrganizationID sql.NullInt64
	if err := db.QueryRow(`
		SELECT customer_organisation_id FROM portal_customers WHERE id = ?
	`, customerMap["qm:tenant:organization-customer"]).Scan(&customerOrganizationID); err != nil {
		t.Fatalf("load imported portal customer: %v", err)
	}
	if customerOrganizationID.Valid {
		t.Fatalf("portal customer organization = %v, want unassigned", customerOrganizationID)
	}
}
