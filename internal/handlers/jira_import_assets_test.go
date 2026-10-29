package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/database"
	"windshift/internal/jira"
)

type fakeJiraAssetFieldClient struct {
	jira.Client
	issueKeys map[string][]string
	issues    map[string]jira.JiraIssue
}

func (f *fakeJiraAssetFieldClient) GetAllIssueKeys(_ context.Context, jql string) ([]string, error) {
	for projectKey, keys := range f.issueKeys {
		if len(jql) >= len("project = "+projectKey) && jql[:len("project = "+projectKey)] == "project = "+projectKey {
			return keys, nil
		}
	}
	return nil, nil
}

func (f *fakeJiraAssetFieldClient) GetProject(_ context.Context, projectKey string) (*jira.JiraProject, error) {
	return &jira.JiraProject{ID: projectKey + "-id", Key: projectKey}, nil
}

func (f *fakeJiraAssetFieldClient) GetProjectFields(_ context.Context, _ []string) ([]jira.JiraCustomField, error) {
	return []jira.JiraCustomField{
		{ID: "customfield_10040"},
		{ID: "customfield_10041"},
	}, nil
}

func (f *fakeJiraAssetFieldClient) BulkFetchIssues(_ context.Context, req jira.BulkFetchRequest) (*jira.BulkFetchResponse, error) {
	result := &jira.BulkFetchResponse{}
	for _, key := range req.IssueIdsOrKeys {
		if issue, ok := f.issues[key]; ok {
			result.Issues = append(result.Issues, issue)
		}
	}
	return result, nil
}

func TestJiraAssetAttributeFieldType(t *testing.T) {
	cases := []struct {
		name string
		attr jira.AssetObjectAttribute
		want string
	}{
		{name: "integer", attr: jira.AssetObjectAttribute{Type: 0, DefaultTypeID: 1}, want: "number"},
		{name: "boolean", attr: jira.AssetObjectAttribute{Type: 0, DefaultTypeID: 2}, want: "boolean"},
		{name: "date", attr: jira.AssetObjectAttribute{Type: 0, DefaultTypeID: 4}, want: "date"},
		{name: "object reference", attr: jira.AssetObjectAttribute{Type: 1}, want: "asset"},
		{name: "user reference", attr: jira.AssetObjectAttribute{Type: 2}, want: "user"},
		{name: "text", attr: jira.AssetObjectAttribute{Type: 0, DefaultTypeID: 0}, want: "textarea"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jiraAssetAttributeFieldType(tc.attr); got != tc.want {
				t.Fatalf("jiraAssetAttributeFieldType() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestJiraIssueAssetCandidatesExtractsIDsAndKeys(t *testing.T) {
	got := jiraIssueAssetCandidates([]interface{}{
		map[string]interface{}{"id": "100", "objectKey": "LAP-1", "label": "Laptop"},
		map[string]interface{}{"objectId": "200", "key": "MON-2", "name": "Monitor"},
		map[string]interface{}{"id": "workspace-uuid:300", "objectId": "300", "workspaceId": "workspace-uuid"},
	})
	want := []jiraIssueAssetCandidate{
		{ID: "100", Key: "LAP-1", Label: "Laptop"},
		{ID: "200", Key: "MON-2", Label: "Monitor"},
		{ID: "300"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("jiraIssueAssetCandidates() = %#v, want %#v", got, want)
	}
}

func TestJiraIssueAssetReferencesForSetExcludesOtherSchemas(t *testing.T) {
	refs := []jiraIssueAssetReference{
		{AssetID: 1, SetID: 77, Title: "Laptop"},
		{AssetID: 2, SetID: 88, Title: "Business service"},
	}
	got := jiraIssueAssetReferencesForSet(refs, 88)
	want := []jiraIssueAssetReference{{AssetID: 2, SetID: 88, Title: "Business service"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("jiraIssueAssetReferencesForSet() = %#v, want %#v", got, want)
	}
}

func TestPreflightJiraAssetFieldsResolvesByFieldAcrossProjects(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-asset-field-preflight.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}
	insertJiraImportConnection(t, db, "conn-1")
	insertJiraImportJob(t, db, "job-preflight", "conn-1", "running", []string{"ONE", "TWO"})

	createMappedAsset := func(schemaID string, setID, assetID int, objectID, objectKey string) {
		t.Helper()
		if _, err := db.ExecWrite(`INSERT INTO asset_management_sets (id, name, description) VALUES (?, ?, '')`, setID, "Set "+schemaID); err != nil {
			t.Fatalf("insert asset set %s: %v", schemaID, err)
		}
		var typeID int
		if err := db.QueryRow(`INSERT INTO asset_types (set_id, name) VALUES (?, ?) RETURNING id`, setID, "Type "+schemaID).Scan(&typeID); err != nil {
			t.Fatalf("insert asset type %s: %v", schemaID, err)
		}
		if _, err := db.ExecWrite(`
			INSERT INTO assets (id, set_id, asset_type_id, title, asset_tag, custom_field_values)
			VALUES (?, ?, ?, ?, ?, '{}')
		`, assetID, setID, typeID, "Asset "+objectKey, objectKey); err != nil {
			t.Fatalf("insert asset %s: %v", objectID, err)
		}
		if _, err := db.ExecWrite(`
			INSERT INTO jira_import_id_mappings (job_id, entity_type, jira_id, jira_key, windshift_id, metadata_json)
			VALUES ('job-preflight', 'asset_set', ?, ?, ?, '{}'),
			       ('job-preflight', 'asset', ?, ?, ?, ?)
		`, schemaID, schemaID, setID, objectID, objectKey, assetID, `{"asset_set_id":`+fmt.Sprint(setID)+`}`); err != nil {
			t.Fatalf("insert Jira mappings for %s: %v", schemaID, err)
		}
	}
	createMappedAsset("schema-1", 77, 701, "100", "HW-1")
	createMappedAsset("schema-2", 88, 801, "200", "SVC-1")

	client := &fakeJiraAssetFieldClient{
		issueKeys: map[string][]string{
			"ONE": {"ONE-1"},
			"TWO": {"TWO-1"},
		},
		issues: map[string]jira.JiraIssue{
			"ONE-1": {
				Key: "ONE-1",
				Fields: jira.JiraIssueFields{CustomFields: map[string]interface{}{
					"customfield_10040": []interface{}{map[string]interface{}{"objectId": "100"}},
					"customfield_10041": []interface{}{map[string]interface{}{"objectId": "200"}},
					"customfield_10042": map[string]interface{}{"id": "1", "value": "Red"},
					"customfield_10043": []interface{}{
						map[string]interface{}{"id": "2", "value": "Alpha"},
						map[string]interface{}{"id": "3", "value": "Beta"},
					},
				}},
			},
			"TWO-1": {
				Key: "TWO-1",
				Fields: jira.JiraIssueFields{CustomFields: map[string]interface{}{
					// The same Jira custom field is reused by another project
					// and still resolves to its one configured schema.
					"customfield_10040": []interface{}{map[string]interface{}{"objectId": "100"}},
					"customfield_10042": map[string]interface{}{"id": "4", "value": "Blue"},
					"customfield_10043": []interface{}{
						map[string]interface{}{"id": "5", "value": "beta"},
					},
				}},
			},
		},
	}
	mappings := []CustomFieldMapping{
		{JiraID: "customfield_10040", WindshiftType: "asset", Action: "create", AssetSchemaID: "auto"},
		{JiraID: "customfield_10041", WindshiftType: "asset", Action: "create", AssetSchemaID: "auto"},
		{JiraID: "customfield_10042", WindshiftType: "select", Action: "create"},
		{JiraID: "customfield_10043", WindshiftType: "multiselect", Action: "create"},
	}

	h := NewJiraImportHandler(db, "jira-asset-field-preflight-secret", "", blobstore.Disabled())
	keys, got, applicable, choiceLabels := h.preflightJiraCustomFields(
		t.Context(),
		"job-preflight",
		client,
		[]string{"ONE", "TWO"},
		false,
		mappings,
	)
	if !reflect.DeepEqual(keys, client.issueKeys) {
		t.Fatalf("prefetched issue keys = %#v, want %#v", keys, client.issueKeys)
	}
	want := map[string]int{"customfield_10040": 77, "customfield_10041": 88}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("asset field set mappings = %#v, want %#v", got, want)
	}
	wantChoiceLabels := map[string][]string{
		"customfield_10042": {"Blue", "Red"},
		"customfield_10043": {"Alpha", "Beta"},
	}
	if !reflect.DeepEqual(choiceLabels, wantChoiceLabels) {
		t.Fatalf("choice labels = %#v, want %#v", choiceLabels, wantChoiceLabels)
	}
	for _, projectKey := range []string{"ONE", "TWO"} {
		if !applicable[projectKey]["customfield_10040"] || !applicable[projectKey]["customfield_10041"] {
			t.Fatalf("applicable fields for %s = %#v, want both fields", projectKey, applicable[projectKey])
		}
	}
}

func TestResolveJiraIssueAssetReferencesUsesImportedAssetMappings(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-asset-links.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}
	insertJiraImportConnection(t, db, "conn-1")
	insertJiraImportJob(t, db, "job-assets", "conn-1", "running", []string{"ASFJ"})

	var setID int
	if err := db.QueryRow(`
		INSERT INTO asset_management_sets (name, description) VALUES ('Imported Assets', '') RETURNING id
	`).Scan(&setID); err != nil {
		t.Fatalf("insert asset set: %v", err)
	}
	var typeID int
	if err := db.QueryRow(`
		INSERT INTO asset_types (set_id, name) VALUES (?, 'Laptop') RETURNING id
	`, setID).Scan(&typeID); err != nil {
		t.Fatalf("insert asset type: %v", err)
	}
	var assetID int
	if err := db.QueryRow(`
		INSERT INTO assets (set_id, asset_type_id, title, asset_tag, custom_field_values)
		VALUES (?, ?, 'Laptop 1', 'LAP-1', '{}') RETURNING id
	`, setID, typeID).Scan(&assetID); err != nil {
		t.Fatalf("insert asset: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO jira_import_id_mappings (job_id, entity_type, jira_id, jira_key, windshift_id, metadata_json)
		VALUES ('job-assets', 'asset', '100', 'LAP-1', ?, '{}')
	`, assetID); err != nil {
		t.Fatalf("insert mapping: %v", err)
	}

	h := NewJiraImportHandler(db, "jira-import-asset-link-test-secret-32", "", blobstore.Disabled())
	refs := h.resolveJiraIssueAssetReferences("job-assets", []interface{}{map[string]interface{}{"objectId": "100", "label": "Laptop 1"}})
	if len(refs) != 1 {
		t.Fatalf("reference count = %d, want 1 (%#v)", len(refs), refs)
	}
	if refs[0].AssetID != assetID || refs[0].AssetTag != "LAP-1" || refs[0].Title != "Laptop 1" {
		t.Fatalf("resolved ref = %#v, want asset id/title/tag", refs[0])
	}
}

func TestJiraAssetAttributeValueUsesDisplayValues(t *testing.T) {
	got, ok := jiraAssetAttributeValue(jira.AssetObjectAttributeValue{
		ObjectAttributeValues: []jira.AssetAttributeValue{
			{DisplayValue: "Server A", SearchValue: "server-a"},
			{SearchValue: "fallback-search"},
		},
	})
	if !ok {
		t.Fatal("jiraAssetAttributeValue() did not return a value")
	}
	if got != "Server A\nfallback-search" {
		t.Fatalf("jiraAssetAttributeValue() = %#v, want newline-separated display/search values", got)
	}
}

func TestJiraAssetHierarchyReferencesStatusesAndUsersUseNativeModels(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "jira-asset-fidelity.db"))
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}
	insertJiraImportConnection(t, db, "conn-assets")
	insertJiraImportJob(t, db, "job-assets", "conn-assets", "running", []string{"ASSET"})

	var setID int
	if err := db.QueryRow(`
		INSERT INTO asset_management_sets (name, description)
		VALUES ('Jira Assets Fidelity', '') RETURNING id
	`).Scan(&setID); err != nil {
		t.Fatalf("create asset set: %v", err)
	}
	h := NewJiraImportHandler(db, "jira-asset-fidelity-test-secret", "", blobstore.Disabled())
	categoryIDs := h.ensureJiraAssetTypeCategories("job-assets", setID, []jira.AssetObjectType{
		{ID: "parent", Name: "Hardware", AbstractObjectType: true},
		{ID: "child", Name: "Servers", ParentObjectTypeID: "parent"},
	})
	if categoryIDs["parent"] == 0 || categoryIDs["child"] == 0 {
		t.Fatalf("category ids = %#v", categoryIDs)
	}
	var parentID int
	if err := db.QueryRow(`
		SELECT parent_id FROM asset_categories WHERE id = ?
	`, categoryIDs["child"]).Scan(&parentID); err != nil {
		t.Fatalf("load imported hierarchy: %v", err)
	}
	if parentID != categoryIDs["parent"] {
		t.Fatalf("child parent = %d, want %d", parentID, categoryIDs["parent"])
	}

	statusID := h.ensureJiraAssetStatus("job-assets", setID, jira.AssetStatus{
		ID: "status-retired", Name: "Retired", Category: 0,
	})
	var statusName string
	if err := db.QueryRow(`SELECT name FROM asset_statuses WHERE id = ?`, statusID).Scan(&statusName); err != nil {
		t.Fatalf("load imported status: %v", err)
	}
	if statusName != "Retired" {
		t.Fatalf("status name = %q, want Retired", statusName)
	}

	var typeID, fieldID int
	if err := db.QueryRow(`
		INSERT INTO asset_types (set_id, name) VALUES (?, 'Server') RETURNING id
	`, setID).Scan(&typeID); err != nil {
		t.Fatalf("create asset type: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO custom_field_definitions (name, field_type)
		VALUES ('Depends on', 'asset') RETURNING id
	`).Scan(&fieldID); err != nil {
		t.Fatalf("create asset reference field: %v", err)
	}
	var targetID, sourceID int
	if err := db.QueryRow(`
		INSERT INTO assets (set_id, asset_type_id, title, asset_tag, custom_field_values)
		VALUES (?, ?, 'Database', 'DB-1', '{}') RETURNING id
	`, setID, typeID).Scan(&targetID); err != nil {
		t.Fatalf("create target asset: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO assets (set_id, asset_type_id, title, asset_tag, custom_field_values)
		VALUES (?, ?, 'Application', 'APP-1', '{}') RETURNING id
	`, setID, typeID).Scan(&sourceID); err != nil {
		t.Fatalf("create source asset: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO jira_import_id_mappings
		       (job_id, entity_type, jira_id, jira_key, windshift_id, metadata_json)
		VALUES ('job-assets', 'asset', '100', 'DB-1', ?, ?),
		       ('job-assets', 'asset', '200', 'APP-1', ?, ?)
	`, targetID, fmt.Sprintf(`{"asset_set_id":%d}`, setID),
		sourceID, fmt.Sprintf(`{"asset_set_id":%d}`, setID)); err != nil {
		t.Fatalf("create asset mappings: %v", err)
	}
	h.resolveJiraAssetReferences("job-assets", []jiraPendingAssetReference{{
		AssetID: sourceID,
		SetID:   setID,
		FieldID: fieldID,
		Values:  []jira.AssetAttributeValue{{Value: "100", DisplayValue: "Database"}},
	}})
	var customValuesJSON string
	if err := db.QueryRow(`
		SELECT custom_field_values FROM assets WHERE id = ?
	`, sourceID).Scan(&customValuesJSON); err != nil {
		t.Fatalf("load resolved asset: %v", err)
	}
	var customValues map[string]interface{}
	if err := json.Unmarshal([]byte(customValuesJSON), &customValues); err != nil {
		t.Fatalf("decode resolved reference: %v", err)
	}
	ref, _ := customValues[fmt.Sprint(fieldID)].(map[string]interface{})
	if ref["id"] != float64(targetID) || ref["asset_tag"] != "DB-1" {
		t.Fatalf("resolved asset reference = %#v", ref)
	}

	userValue, ok := jiraAssetUserAttributeValue(jira.AssetObjectAttributeValue{
		ObjectAttributeValues: []jira.AssetAttributeValue{{
			User: &jira.JiraUser{AccountID: "user-1"},
		}},
	}, map[string]int{"user-1": 42})
	if !ok || userValue != 42 {
		t.Fatalf("resolved user attribute = %#v, %v", userValue, ok)
	}
}
