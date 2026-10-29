package handlers

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"windshift/internal/blobstore"
	"windshift/internal/jira"
)

// TestLiveJiraCustomFieldFidelity is an opt-in, read-only contract check
// against the Jira project configured in the ignored local .env file. It
// verifies definition visibility and performs the same complete-scope choice
// label preflight used by execution, but never creates an import job or writes
// Windshift/Jira data.
func TestLiveJiraCustomFieldFidelity(t *testing.T) {
	instanceURL := os.Getenv("JIRA_INSTANCE_URL")
	email := os.Getenv("JIRA_EMAIL")
	apiToken := os.Getenv("JIRA_API_TOKEN")
	projectKey := os.Getenv("JIRA_PROJECT_KEY")
	if configPath := os.Getenv("JIRA_IMPORT_E2E_CONFIG"); configPath != "" {
		configBody, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("read live Jira config: %v", err)
		}
		var config struct {
			CloudURL    string   `json:"cloudUrl"`
			Email       string   `json:"email"`
			APIToken    string   `json:"apiToken"`
			ProjectKeys []string `json:"projectKeys"`
		}
		if err := json.Unmarshal(configBody, &config); err != nil {
			t.Fatalf("decode live Jira config: %v", err)
		}
		instanceURL = config.CloudURL
		email = config.Email
		apiToken = config.APIToken
		if projectKey == "" && len(config.ProjectKeys) > 0 {
			projectKey = config.ProjectKeys[0]
		}
	}
	if instanceURL == "" || email == "" || apiToken == "" || projectKey == "" {
		t.Skip("set JIRA_IMPORT_E2E_CONFIG or the JIRA_INSTANCE_URL/JIRA_EMAIL/JIRA_API_TOKEN/JIRA_PROJECT_KEY variables")
	}

	client, err := jira.NewClient(jira.Config{
		InstanceURL:    instanceURL,
		Email:          email,
		APIToken:       apiToken,
		DeploymentType: jira.DeploymentCloud,
		Timeout:        30 * time.Second,
	})
	if err != nil {
		t.Fatalf("create Jira client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	project, err := client.GetProject(ctx, projectKey)
	if err != nil {
		t.Fatalf("get Jira project %q: %v", projectKey, err)
	}
	fields, err := client.GetProjectFields(ctx, []string{project.ID})
	if err != nil {
		t.Fatalf("get project custom fields: %v", err)
	}
	suggestions := jira.SuggestFieldMappings(fields)
	if len(suggestions) != len(fields) {
		t.Fatalf("suggestions = %d, project fields = %d", len(suggestions), len(fields))
	}

	fieldsByID := make(map[string]jira.JiraCustomField, len(fields))
	for _, field := range fields {
		fieldsByID[field.ID] = field
	}
	unknownCount := 0
	rawPreservationCount := 0
	requestParticipantsFound := false
	choiceMappings := make([]CustomFieldMapping, 0)
	for _, suggestion := range suggestions {
		field := fieldsByID[suggestion.JiraFieldID]
		if !jira.IsKnownFieldType(field) {
			unknownCount++
		}
		if suggestion.PreserveRaw {
			rawPreservationCount++
		}
		if suggestion.JiraFieldName == "Request participants" {
			requestParticipantsFound = true
			if suggestion.WindshiftFieldType != jira.FieldTypeMultiUser {
				t.Fatalf("request participants maps to %q, want multi_user", suggestion.WindshiftFieldType)
			}
		}
		if suggestion.CanMap &&
			(suggestion.WindshiftFieldType == jira.FieldTypeSelect ||
				suggestion.WindshiftFieldType == jira.FieldTypeMultiselect) {
			choiceMappings = append(choiceMappings, CustomFieldMapping{
				JiraID:        suggestion.JiraFieldID,
				JiraName:      suggestion.JiraFieldName,
				JiraType:      suggestion.JiraFieldType,
				WindshiftType: string(suggestion.WindshiftFieldType),
				CanMap:        true,
				Action:        "create",
			})
		}
	}

	handler := NewJiraImportHandler(nil, "live-jira-field-read-only-secret", "", blobstore.Disabled())
	issueKeysByProject, _, _, choiceLabels := handler.preflightJiraCustomFields(
		ctx,
		"",
		client,
		[]string{projectKey},
		false,
		choiceMappings,
	)
	populatedChoiceFields := 0
	for _, labels := range choiceLabels {
		if len(labels) > 0 {
			populatedChoiceFields++
		}
	}
	configuredContexts := 0
	configuredOptions := 0
	configuredDefaults := 0
	configurationClient, ok := client.(jira.CustomFieldConfigurationClient)
	if !ok {
		t.Fatal("live Jira client does not expose custom-field configuration")
	}
	for _, mapping := range choiceMappings {
		configuration, configErr := configurationClient.GetCustomFieldConfiguration(ctx, mapping.JiraID, true)
		if configErr != nil {
			t.Fatalf("get configuration for %s (%s): %v", mapping.JiraID, mapping.JiraName, configErr)
		}
		configuredContexts += len(configuration.Contexts)
		for _, fieldContext := range configuration.Contexts {
			configuredOptions += len(fieldContext.Options)
			configuredDefaults += len(fieldContext.Defaults)
		}
	}

	watchedIssues := 0
	importableWatcherIdentities := 0
	watcherReadFailures := 0
	keys := issueKeysByProject[projectKey]
	if len(keys) > 20 {
		keys = keys[:20]
	}
	if len(keys) > 0 {
		issues, fetchErr := client.BulkFetchIssues(ctx, jira.BulkFetchRequest{
			IssueIdsOrKeys: keys,
			Fields:         []string{"watches", "votes", "security"},
		})
		if fetchErr != nil {
			t.Fatalf("fetch watcher/vote/security sample: %v", fetchErr)
		}
		watcherClient, watcherCapable := client.(jira.IssueWatchersClient)
		if !watcherCapable {
			t.Fatal("live Jira client does not expose issue watchers")
		}
		for _, issue := range issues.Issues {
			if issue.Fields.Watches == nil || issue.Fields.Watches.WatchCount == 0 {
				continue
			}
			watchedIssues++
			watchers, watcherErr := watcherClient.GetIssueWatchers(ctx, issue.Key)
			if watcherErr != nil {
				watcherReadFailures++
				continue
			}
			importableWatcherIdentities += len(watchers.Watchers)
		}
	}
	t.Logf(
		"project %s: %d issues, %d fields, %d unknown plugin types retained, %d raw-preserved fields, %d/%d choice fields populated, %d contexts/%d configured options/%d defaults, request participants present=%t, watcher sample=%d watched/%d identities/%d unavailable",
		projectKey,
		len(issueKeysByProject[projectKey]),
		len(fields),
		unknownCount,
		rawPreservationCount,
		populatedChoiceFields,
		len(choiceMappings),
		configuredContexts,
		configuredOptions,
		configuredDefaults,
		requestParticipantsFound,
		watchedIssues,
		importableWatcherIdentities,
		watcherReadFailures,
	)
}
