package xray

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestLiveCloudDefinitionContract is an opt-in read-only contract check against
// a real Xray Cloud tenant. It is skipped in normal suites.
func TestLiveCloudDefinitionContract(t *testing.T) {
	configPath := os.Getenv("JIRA_IMPORT_E2E_CONFIG")
	issueID := os.Getenv("JIRA_XRAY_LIVE_ISSUE_ID")
	if configPath == "" || issueID == "" {
		t.Skip("set JIRA_IMPORT_E2E_CONFIG and JIRA_XRAY_LIVE_ISSUE_ID to run the live Xray definition check")
	}

	configBody, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read live Jira/Xray config: %v", err)
	}
	var config struct {
		Xray struct {
			Region       string `json:"region"`
			ClientID     string `json:"clientId"`
			ClientSecret string `json:"clientSecret"`
		} `json:"xray"`
	}
	if err := json.Unmarshal(configBody, &config); err != nil {
		t.Fatalf("decode live Jira/Xray config: %v", err)
	}
	if config.Xray.ClientID == "" || config.Xray.ClientSecret == "" {
		t.Fatal("live config is missing xray.clientId or xray.clientSecret")
	}

	client, err := NewCloudClient(CloudConfig{
		ClientID:     config.Xray.ClientID,
		ClientSecret: config.Xray.ClientSecret,
		Region:       config.Xray.Region,
	})
	if err != nil {
		t.Fatalf("NewCloudClient() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tests, err := client.GetTests(ctx, []string{issueID})
	if err != nil {
		t.Fatalf("GetTests() error = %v", err)
	}
	if len(tests) != 1 || tests[0].IssueID != issueID {
		t.Fatalf("GetTests() = %#v, want one definition for issue %s", tests, issueID)
	}
	if tests[0].TestTypeName == "" {
		t.Fatal("live Xray definition has no test type")
	}
	if os.Getenv("JIRA_XRAY_LIVE_REQUIRE_STEPS") == "1" && len(tests[0].Steps) == 0 {
		t.Fatalf("live Xray definition for issue %s has no manual steps", issueID)
	}
	t.Logf(
		"loaded Xray definition for issue %s: type=%q steps=%d",
		issueID,
		tests[0].TestTypeName,
		len(tests[0].Steps),
	)
}
