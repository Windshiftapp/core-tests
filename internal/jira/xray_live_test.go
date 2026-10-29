package jira

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestLiveXrayDetectionContract is an opt-in read-only contract check against
// a real Jira/Xray Cloud tenant. It is skipped in normal suites.
func TestLiveXrayDetectionContract(t *testing.T) {
	configPath := os.Getenv("JIRA_IMPORT_E2E_CONFIG")
	projectKey := os.Getenv("JIRA_XRAY_LIVE_PROJECT")
	if configPath == "" || projectKey == "" {
		t.Skip("set JIRA_IMPORT_E2E_CONFIG and JIRA_XRAY_LIVE_PROJECT to run the live Xray contract check")
	}

	configBody, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read live Jira config: %v", err)
	}
	var config struct {
		CloudURL string `json:"cloudUrl"`
		Email    string `json:"email"`
		APIToken string `json:"apiToken"`
	}
	if err := json.Unmarshal(configBody, &config); err != nil {
		t.Fatalf("decode live Jira config: %v", err)
	}

	client, err := NewClient(Config{
		InstanceURL:    config.CloudURL,
		Email:          config.Email,
		APIToken:       config.APIToken,
		DeploymentType: DeploymentCloud,
		Timeout:        30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	detector, ok := client.(XrayTestIssueTypeDetector)
	if !ok {
		t.Fatal("Jira Cloud client does not implement XrayTestIssueTypeDetector")
	}
	lister, ok := client.(XrayTestKeyLister)
	if !ok {
		t.Fatal("Jira Cloud client does not implement XrayTestKeyLister")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	issueTypes, err := client.GetProjectIssueTypes(ctx, projectKey)
	if err != nil {
		t.Fatalf("GetProjectIssueTypes(%q) error = %v", projectKey, err)
	}
	xrayTypeIDs, err := detector.DetectXrayTestIssueTypes(ctx, issueTypes)
	if err != nil {
		t.Fatalf("DetectXrayTestIssueTypes() error = %v", err)
	}
	if len(xrayTypeIDs) == 0 {
		t.Fatalf("no Xray-owned Test issue type detected in project %q", projectKey)
	}

	keys, err := lister.ListXrayTestKeys(ctx, projectKey, xrayTypeIDs, false)
	if err != nil {
		t.Fatalf("ListXrayTestKeys() error = %v", err)
	}
	if len(keys) == 0 {
		t.Fatalf("no Xray Tests found in project %q for issue types %v", projectKey, xrayTypeIDs)
	}
	t.Logf("detected Xray issue types %v and test keys %v", xrayTypeIDs, keys)
}
