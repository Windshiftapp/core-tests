package handlers

import (
	"context"
	"database/sql"
	"strconv"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/jira"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

type fakeJiraScreenImportClient struct {
	jira.Client
	project       jira.JiraProject
	issueTypes    []jira.JiraIssueType
	configuration *jira.JiraProjectScreenConfiguration
}

type fakeJiraWorkflowImportClient struct {
	jira.Client
	project           jira.JiraProject
	issueTypeStatuses []jira.JiraIssueTypeWithStatuses
	configuration     *jira.JiraProjectWorkflowConfiguration
}

func (c *fakeJiraWorkflowImportClient) GetProject(
	context.Context,
	string,
) (*jira.JiraProject, error) {
	project := c.project
	return &project, nil
}

func (c *fakeJiraWorkflowImportClient) GetProjectIssueTypeStatuses(
	context.Context,
	string,
) ([]jira.JiraIssueTypeWithStatuses, error) {
	return c.issueTypeStatuses, nil
}

func (c *fakeJiraWorkflowImportClient) GetProjectWorkflowConfiguration(
	context.Context,
	string,
	[]string,
) (*jira.JiraProjectWorkflowConfiguration, error) {
	return c.configuration, nil
}

func (c *fakeJiraScreenImportClient) GetProject(
	context.Context,
	string,
) (*jira.JiraProject, error) {
	project := c.project
	return &project, nil
}

func (c *fakeJiraScreenImportClient) GetProjectIssueTypes(
	context.Context,
	string,
) ([]jira.JiraIssueType, error) {
	return c.issueTypes, nil
}

func (c *fakeJiraScreenImportClient) GetProjectScreenConfiguration(
	context.Context,
	string,
	string,
	[]string,
) (*jira.JiraProjectScreenConfiguration, error) {
	return c.configuration, nil
}

func TestAuthoritativeJiraWorkflowEdgesPreserveTopologyAndOnlyLockKnownRules(t *testing.T) {
	group := jiraImportWorkflowGroup{
		statusIDs: []int{11, 22, 33},
		transitions: []jira.JiraConfiguredWorkflowTransition{
			{
				ID:         "initial",
				Type:       jira.JiraWorkflowTransitionInitial,
				ToStatusID: "open",
			},
			{
				ID:            "directed",
				Type:          jira.JiraWorkflowTransitionDirected,
				FromStatusIDs: []string{"open"},
				ToStatusID:    "doing",
			},
			{
				ID:             "guarded",
				Type:           jira.JiraWorkflowTransitionDirected,
				FromStatusIDs:  []string{"doing"},
				ToStatusID:     "done",
				ValidatorCount: 1,
				ActionCount:    2,
				TriggerCount:   1,
			},
			{
				ID:         "global",
				Type:       jira.JiraWorkflowTransitionGlobal,
				ToStatusID: "open",
			},
		},
	}
	statusMap := map[string]int{"open": 11, "doing": 22, "done": 33}

	edges := authoritativeJiraWorkflowEdges(&group, statusMap)
	got := make(map[[2]int]bool)
	initialTargets := make(map[int]bool)
	for _, edge := range edges {
		if edge.fromStatusID == nil {
			initialTargets[edge.toStatusID] = true
			continue
		}
		got[[2]int{*edge.fromStatusID, edge.toStatusID}] = true
	}

	if !initialTargets[11] {
		t.Fatalf("initial targets = %#v, want status 11", initialTargets)
	}
	for _, want := range [][2]int{{11, 22}, {22, 11}, {33, 11}, {22, 33}} {
		if !got[want] {
			t.Errorf("missing directed edge %v; got %#v", want, got)
		}
	}
	if got[[2]int{11, 11}] {
		t.Fatal("global transition created a redundant self-edge")
	}
	locked := make(map[[2]int]bool)
	for _, edge := range edges {
		if edge.fromStatusID != nil {
			locked[[2]int{*edge.fromStatusID, edge.toStatusID}] = edge.reviewLocked
		}
	}
	if locked[[2]int{11, 22}] {
		t.Fatal("unidentified Jira rule created a review lock")
	}
	if !locked[[2]int{22, 33}] {
		t.Fatal("explicitly guarded Jira transition was not review locked")
	}
	if group.guardedTransitions != 1 ||
		group.unsupportedLoops != 1 ||
		group.unsupportedActions != 2 ||
		group.unsupportedTriggers != 1 {
		t.Fatalf("fidelity counters = %#v", group)
	}
}

func TestFallbackJiraWorkflowGroupsDoNotInventTransitions(t *testing.T) {
	groups := fallbackJiraWorkflowGroups("APP", []jiraImportIssueTypeInfo{
		{
			jiraID:              "100",
			jiraName:            "Bug",
			windshiftItemTypeID: 7,
			windshiftStatusIDs:  []int{1, 2, 3},
		},
		{
			jiraID:              "101",
			jiraName:            "Task",
			windshiftItemTypeID: 8,
			windshiftStatusIDs:  []int{1, 2, 3},
		},
	})
	if len(groups) != 1 {
		t.Fatalf("group count = %d, want 1", len(groups))
	}
	if groups[0].authoritative {
		t.Fatal("fallback group marked authoritative")
	}
	if len(groups[0].transitions) != 0 {
		t.Fatalf("fallback transitions = %#v, want none", groups[0].transitions)
	}
	if len(groups[0].itemTypeIDs) != 2 {
		t.Fatalf("item type IDs = %#v, want two", groups[0].itemTypeIDs)
	}
}

func TestAuthoritativeJiraWorkflowGroupsRejectIncompleteSourceData(t *testing.T) {
	issueTypes := []jiraImportIssueTypeInfo{
		{jiraID: "100", windshiftItemTypeID: 7},
		{jiraID: "101", windshiftItemTypeID: 8},
	}
	tests := []struct {
		name      string
		config    *jira.JiraProjectWorkflowConfiguration
		statusMap map[string]int
	}{
		{
			name: "missing issue type assignment",
			config: &jira.JiraProjectWorkflowConfiguration{
				IssueTypeWorkflowIDs: map[string]string{"100": "wf-1"},
				Workflows: []jira.JiraWorkflowConfiguration{
					{ID: "wf-1", StatusIDs: []string{"open"}},
				},
			},
			statusMap: map[string]int{"open": 11},
		},
		{
			name: "assignment references absent workflow",
			config: &jira.JiraProjectWorkflowConfiguration{
				IssueTypeWorkflowIDs: map[string]string{"100": "wf-1", "101": "wf-missing"},
				Workflows: []jira.JiraWorkflowConfiguration{
					{ID: "wf-1", StatusIDs: []string{"open"}},
				},
			},
			statusMap: map[string]int{"open": 11},
		},
		{
			name: "workflow contains unmapped status",
			config: &jira.JiraProjectWorkflowConfiguration{
				IssueTypeWorkflowIDs: map[string]string{"100": "wf-1", "101": "wf-1"},
				Workflows: []jira.JiraWorkflowConfiguration{
					{ID: "wf-1", StatusIDs: []string{"open", "missing"}},
				},
			},
			statusMap: map[string]int{"open": 11},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if groups := authoritativeJiraWorkflowGroups(tt.config, issueTypes, tt.statusMap); groups != nil {
				t.Fatalf("groups = %#v, want conservative fallback", groups)
			}
		})
	}
}

func TestEnsureJiraConfiguredScreenMapsSupportedFieldsInSourceOrder(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	handler := NewJiraImportHandler(tdb.GetDatabase(), "jira-screen-test-secret-32-bytes", "", blobstore.Disabled())

	var customFieldID int
	if err := tdb.QueryRow(`
		INSERT INTO custom_field_definitions (name, field_type, description, options)
		VALUES ('Impact', 'text', '', '') RETURNING id
	`).Scan(&customFieldID); err != nil {
		t.Fatalf("insert custom field: %v", err)
	}

	screenID, created, omitted, err := handler.ensureJiraConfiguredScreen(
		"APP - Work Screen (Jira 100)",
		jira.JiraScreenConfiguration{
			ID:   "100",
			Name: "Work Screen",
			Fields: []jira.JiraScreenField{
				{ID: "summary", Name: "Summary"},
				{ID: "customfield_sprint", Name: "Sprint"},
				{ID: "customfield_impact", Name: "Impact"},
				{ID: "reporter", Name: "Reporter"},
				{ID: "summary", Name: "Summary duplicate"},
			},
		},
		map[string]int{"customfield_impact": customFieldID},
		[]CustomFieldMapping{{
			JiraID:        "customfield_sprint",
			JiraType:      "com.pyxis.greenhopper.jira:gh-sprint",
			WindshiftType: string(jira.FieldTypeIteration),
		}},
	)
	if err != nil {
		t.Fatalf("ensureJiraConfiguredScreen() error = %v", err)
	}
	if !created {
		t.Fatal("screen was not created")
	}
	if omitted != 1 {
		t.Fatalf("omitted fields = %d, want 1", omitted)
	}

	rows, err := tdb.Query(`
		SELECT field_type, field_identifier, display_order, is_required
		FROM screen_fields
		WHERE screen_id = ?
		ORDER BY display_order
	`, screenID)
	if err != nil {
		t.Fatalf("query screen fields: %v", err)
	}
	defer rows.Close()
	type field struct {
		kind       string
		identifier string
		order      int
		required   bool
	}
	var fields []field
	for rows.Next() {
		var got field
		if err := rows.Scan(&got.kind, &got.identifier, &got.order, &got.required); err != nil {
			t.Fatalf("scan screen field: %v", err)
		}
		fields = append(fields, got)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate screen fields: %v", err)
	}
	if len(fields) != 3 {
		t.Fatalf("fields = %#v, want 3", fields)
	}
	if fields[0].kind != "system" || fields[0].identifier != "title" || !fields[0].required ||
		fields[1].kind != "system" || fields[1].identifier != "iteration" ||
		fields[2].kind != "custom" || fields[2].identifier != strconv.Itoa(customFieldID) {
		t.Fatalf("mapped fields = %#v", fields)
	}
}

func TestJiraWorkflowReviewLockBlocksImportedTransition(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	handler := NewJiraImportHandler(tdb.GetDatabase(), "jira-workflow-test-secret-32-bytes", "", blobstore.Disabled())

	insertID := func(query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := tdb.QueryRow(query+" RETURNING id", args...).Scan(&id); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
		return id
	}
	categoryID := insertID(`INSERT INTO status_categories (name, color) VALUES ('Jira Review', '#123456')`)
	fromID := insertID(
		`INSERT INTO statuses (name, description, category_id) VALUES ('Jira Review Open', '', ?)`,
		categoryID,
	)
	toID := insertID(
		`INSERT INTO statuses (name, description, category_id) VALUES ('Jira Review Done', '', ?)`,
		categoryID,
	)
	workflowID := insertID(`INSERT INTO workflows (name, description) VALUES ('Jira Review Workflow', '')`)
	transitionID := insertID(
		`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, display_order) VALUES (?, ?, ?, 1)`,
		workflowID,
		fromID,
		toID,
	)

	conditionSetID, err := handler.imports.CreateWorkflowReviewConditionSet(workflowID, "Jira Review Workflow")
	if err != nil {
		t.Fatalf("createJiraWorkflowReviewConditionSet() error = %v", err)
	}
	if err := handler.imports.LockWorkflowTransitionForReview(conditionSetID, transitionID); err != nil {
		t.Fatalf("lockJiraWorkflowTransitionForReview() error = %v", err)
	}

	allowed, message, err := services.NewConditionService(
		tdb.GetDatabase(),
		nil,
		nil,
	).EvaluateTransitionConditions(
		context.Background(),
		conditionSetID,
		transitionID,
		1,
		map[string]interface{}{"title": "A real work item"},
		[]string{"validator"},
	)
	if err != nil {
		t.Fatalf("EvaluateTransitionConditions() error = %v", err)
	}
	if allowed {
		t.Fatal("imported transition review lock allowed the transition")
	}
	if message != "Imported Jira transition is locked pending review of source conditions and validators." {
		t.Fatalf("failure message = %q", message)
	}
}

func TestEnsureJiraProjectScreensAssignsDefaultsAndItemTypeOverrides(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	insertJiraImportConnection(t, tdb.Database, "connection-screen-config")
	insertJiraImportJob(t, tdb.Database, "job-screen-config", "connection-screen-config", "running", []string{"APP"})

	insertID := func(query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := tdb.QueryRow(query+" RETURNING id", args...).Scan(&id); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
		return id
	}
	workspaceID := insertID(`INSERT INTO workspaces (name, key) VALUES ('Screen Workspace', 'APP')`)
	configSetID := insertID(`INSERT INTO configuration_sets (name, description) VALUES ('Screen Config', '')`)
	if _, err := tdb.Exec(
		`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`,
		workspaceID,
		configSetID,
	); err != nil {
		t.Fatalf("assign configuration set: %v", err)
	}
	bugID := insertID(`INSERT INTO item_types (name) VALUES ('Screen Bug')`)
	taskID := insertID(`INSERT INTO item_types (name) VALUES ('Screen Task')`)
	for _, itemTypeID := range []int{bugID, taskID} {
		if _, err := tdb.Exec(
			`INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id) VALUES (?, ?)`,
			configSetID,
			itemTypeID,
		); err != nil {
			t.Fatalf("insert item type config: %v", err)
		}
	}

	screen := func(id, name string) jira.JiraScreenConfiguration {
		return jira.JiraScreenConfiguration{
			ID:     id,
			Name:   name,
			Fields: []jira.JiraScreenField{{ID: "summary", Name: "Summary"}},
		}
	}
	client := &fakeJiraScreenImportClient{
		project:    jira.JiraProject{ID: "10000", Key: "APP", Name: "App"},
		issueTypes: []jira.JiraIssueType{{ID: "bug", Name: "Bug"}, {ID: "task", Name: "Task"}},
		configuration: &jira.JiraProjectScreenConfiguration{
			IssueTypeScreens: map[string]jira.JiraIssueTypeScreens{
				"bug":  {CreateScreenID: "100", EditScreenID: "101", ViewScreenID: "102"},
				"task": {CreateScreenID: "100", EditScreenID: "103", ViewScreenID: "102"},
			},
			Screens: []jira.JiraScreenConfiguration{
				screen("100", "Create"),
				screen("101", "Bug Edit"),
				screen("102", "View"),
				screen("103", "Task Edit"),
			},
		},
	}
	handler := NewJiraImportHandler(tdb.GetDatabase(), "jira-screen-config-test-secret-32", "", blobstore.Disabled())
	if err := handler.ensureJiraProjectScreens(
		context.Background(),
		"job-screen-config",
		"APP",
		workspaceID,
		map[string]int{"bug": bugID, "task": taskID, "other-project": 999},
		map[string]int{},
		nil,
		client,
	); err != nil {
		t.Fatalf("ensureJiraProjectScreens() error = %v", err)
	}

	defaults := make(map[string]int)
	rows, err := tdb.Query(`
		SELECT context, screen_id
		FROM configuration_set_screens
		WHERE configuration_set_id = ?
	`, configSetID)
	if err != nil {
		t.Fatalf("query default screens: %v", err)
	}
	for rows.Next() {
		var contextName string
		var screenID int
		if err := rows.Scan(&contextName, &screenID); err != nil {
			t.Fatalf("scan default screen: %v", err)
		}
		defaults[contextName] = screenID
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close default screens: %v", err)
	}
	if len(defaults) != 3 {
		t.Fatalf("default screens = %#v, want three contexts", defaults)
	}

	var bugEdit, taskEdit sql.NullInt64
	if err := tdb.QueryRow(`
		SELECT edit_screen_id FROM configuration_set_item_types
		WHERE configuration_set_id = ? AND item_type_id = ?
	`, configSetID, bugID).Scan(&bugEdit); err != nil {
		t.Fatalf("query bug screen override: %v", err)
	}
	if err := tdb.QueryRow(`
		SELECT edit_screen_id FROM configuration_set_item_types
		WHERE configuration_set_id = ? AND item_type_id = ?
	`, configSetID, taskID).Scan(&taskEdit); err != nil {
		t.Fatalf("query task screen override: %v", err)
	}
	if bugEdit.Valid == taskEdit.Valid {
		t.Fatalf("expected exactly one edit override; bug=%#v task=%#v", bugEdit, taskEdit)
	}
	override := bugEdit
	if taskEdit.Valid {
		override = taskEdit
	}
	if int(override.Int64) == defaults["edit"] {
		t.Fatalf("item-type edit override %d equals default %d", override.Int64, defaults["edit"])
	}
}

func TestEnsureWorkflowsPreservesSourceIdentityGraphAndIssueTypeAssignment(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	insertJiraImportConnection(t, tdb.Database, "connection-workflow-config")
	insertJiraImportJob(t, tdb.Database, "job-workflow-config", "connection-workflow-config", "running", []string{"APP"})

	insertID := func(query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := tdb.QueryRow(query+" RETURNING id", args...).Scan(&id); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
		return id
	}
	categoryID := insertID(`INSERT INTO status_categories (name, color) VALUES ('Jira Graph', '#123456')`)
	openID := insertID(
		`INSERT INTO statuses (name, description, category_id) VALUES ('Jira Graph Open', '', ?)`,
		categoryID,
	)
	doneID := insertID(
		`INSERT INTO statuses (name, description, category_id) VALUES ('Jira Graph Done', '', ?)`,
		categoryID,
	)
	bugID := insertID(`INSERT INTO item_types (name) VALUES ('Jira Graph Bug')`)
	taskID := insertID(`INSERT INTO item_types (name) VALUES ('Jira Graph Task')`)
	workspaceID := insertID(`INSERT INTO workspaces (name, key) VALUES ('Jira Graph Workspace', 'APP')`)

	statuses := []jira.JiraStatus{{ID: "open", Name: "Open"}, {ID: "done", Name: "Done"}}
	client := &fakeJiraWorkflowImportClient{
		project: jira.JiraProject{ID: "10000", Key: "APP", Name: "App"},
		issueTypeStatuses: []jira.JiraIssueTypeWithStatuses{
			{ID: "bug", Name: "Bug", Statuses: statuses},
			{ID: "task", Name: "Task", Statuses: statuses},
		},
		configuration: &jira.JiraProjectWorkflowConfiguration{
			IssueTypeWorkflowIDs: map[string]string{"bug": "wf-a", "task": "wf-b"},
			Workflows: []jira.JiraWorkflowConfiguration{
				{
					ID:        "wf-a",
					Name:      "Bug flow",
					StatusIDs: []string{"open", "done"},
					Transitions: []jira.JiraConfiguredWorkflowTransition{
						{ID: "a-init", Type: jira.JiraWorkflowTransitionInitial, ToStatusID: "open"},
						{
							ID:            "a-finish",
							Type:          jira.JiraWorkflowTransitionDirected,
							FromStatusIDs: []string{"open"},
							ToStatusID:    "done",
						},
					},
				},
				{
					ID:        "wf-b",
					Name:      "Task flow",
					StatusIDs: []string{"open", "done"},
					Transitions: []jira.JiraConfiguredWorkflowTransition{
						{ID: "b-init", Type: jira.JiraWorkflowTransitionInitial, ToStatusID: "open"},
						{
							ID:            "b-reopen",
							Type:          jira.JiraWorkflowTransitionDirected,
							FromStatusIDs: []string{"done"},
							ToStatusID:    "open",
						},
					},
				},
			},
			RulesComplete: false,
		},
	}
	handler := NewJiraImportHandler(tdb.GetDatabase(), "jira-workflow-config-test-secret-32", "", blobstore.Disabled())
	if err := handler.ensureWorkflowsAndConfigSet(
		context.Background(),
		"job-workflow-config",
		"APP",
		workspaceID,
		map[string]int{"open": openID, "done": doneID},
		map[string]int{"bug": bugID, "task": taskID},
		client,
	); err != nil {
		t.Fatalf("ensureWorkflowsAndConfigSet() error = %v", err)
	}

	workflowIDs := make(map[string]int)
	rows, err := tdb.Query(`
		SELECT jira_id, windshift_id
		FROM jira_import_id_mappings
		WHERE job_id = 'job-workflow-config' AND entity_type = 'workflow'
	`)
	if err != nil {
		t.Fatalf("query workflow mappings: %v", err)
	}
	for rows.Next() {
		var sourceID string
		var workflowID int
		if err := rows.Scan(&sourceID, &workflowID); err != nil {
			t.Fatalf("scan workflow mapping: %v", err)
		}
		workflowIDs[sourceID] = workflowID
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close workflow mappings: %v", err)
	}
	if len(workflowIDs) != 2 || workflowIDs["wf-a"] == workflowIDs["wf-b"] {
		t.Fatalf("workflow mappings = %#v, want two distinct source identities", workflowIDs)
	}

	assertDirectedEdge := func(workflowID, wantFrom, wantTo int) {
		t.Helper()
		var count int
		if err := tdb.QueryRow(`
			SELECT COUNT(*) FROM workflow_transitions
			WHERE workflow_id = ? AND from_status_id = ? AND to_status_id = ?
		`, workflowID, wantFrom, wantTo).Scan(&count); err != nil {
			t.Fatalf("count directed edge: %v", err)
		}
		if count != 1 {
			t.Fatalf("edge %d -> %d count = %d, want 1", wantFrom, wantTo, count)
		}
		var otherCount int
		if err := tdb.QueryRow(`
			SELECT COUNT(*) FROM workflow_transitions
			WHERE workflow_id = ? AND from_status_id IS NOT NULL
		`, workflowID).Scan(&otherCount); err != nil {
			t.Fatalf("count workflow edges: %v", err)
		}
		if otherCount != 1 {
			t.Fatalf("workflow %d directed edge count = %d, want 1", workflowID, otherCount)
		}
	}
	assertDirectedEdge(workflowIDs["wf-a"], openID, doneID)
	assertDirectedEdge(workflowIDs["wf-b"], doneID, openID)

	effectiveWorkflow := func(itemTypeID int) (workflowID int, conditionSetID sql.NullInt64) {
		t.Helper()
		if err := tdb.QueryRow(`
			SELECT COALESCE(csit.workflow_id, cs.workflow_id),
			       COALESCE(csit.condition_set_id, cs.condition_set_id)
			FROM workspace_configuration_sets wcs
			JOIN configuration_sets cs ON cs.id = wcs.configuration_set_id
			JOIN configuration_set_item_types csit ON csit.configuration_set_id = cs.id
			WHERE wcs.workspace_id = ? AND csit.item_type_id = ?
		`, workspaceID, itemTypeID).Scan(&workflowID, &conditionSetID); err != nil {
			t.Fatalf("query effective workflow: %v", err)
		}
		return workflowID, conditionSetID
	}
	bugWorkflowID, bugConditionSetID := effectiveWorkflow(bugID)
	taskWorkflowID, taskConditionSetID := effectiveWorkflow(taskID)
	if bugWorkflowID != workflowIDs["wf-a"] || taskWorkflowID != workflowIDs["wf-b"] {
		t.Fatalf(
			"effective workflows bug=%d task=%d, want bug=%d task=%d",
			bugWorkflowID,
			taskWorkflowID,
			workflowIDs["wf-a"],
			workflowIDs["wf-b"],
		)
	}
	if bugConditionSetID.Valid || taskConditionSetID.Valid {
		t.Fatalf(
			"unidentified Jira rules created review condition sets bug=%#v task=%#v, want none",
			bugConditionSetID,
			taskConditionSetID,
		)
	}
}
