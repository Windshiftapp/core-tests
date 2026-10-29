//go:build test

package tests

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func slaImportDocument(calendarSource, metricSource string) map[string]any {
	return map[string]any{
		"calendars": []map[string]any{{
			"source_id": calendarSource,
			"name":      "Imported support hours",
			"timezone":  "UTC",
			"weekly_intervals": map[string]any{
				"monday": []map[string]string{{"start": "00:00", "end": "24:00"}},
			},
		}},
		"metrics": []map[string]any{{
			"source_id":      metricSource,
			"name":           "Imported first response",
			"display_format": "time",
			"is_active":      true,
			"conditions": []map[string]any{{
				"source_id": "cond-1", "phase": "start", "condition_type": "created", "config": map[string]any{},
			}},
			"goals": []map[string]any{{
				"source_id": "goal-1", "position": 0, "jql": "project = SIW",
				"targets": []map[string]any{{
					"source_id": "target-1", "position": 0, "is_fallback": true,
					"target_ms": 3600000, "calendar_source_id": calendarSource,
				}},
			}},
		}},
	}
}

// TestSLACycleImportIsIdempotent configures a metric and cycle through the SLA
// import surface, then re-imports the same document and asserts nothing
// duplicates.
func TestSLACycleImportIsIdempotent(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Import Workspace", shortKey("SIW"))
	itemID := CreateTestItem(t, server, workspaceID, "Import cycle item")

	document := slaImportDocument("jira-cal-1", "jira-sla-1")
	document["cycles"] = []map[string]any{{
		"source_id":          "jira-cycle-1",
		"metric_source_id":   "jira-sla-1",
		"item_id":            itemID,
		"goal_source_id":     "goal-1",
		"calendar_source_id": "jira-cal-1",
		"cycle_no":           1,
		"status":             "ongoing",
		"started_at":         "2026-01-01T00:00:00Z",
		"elapsed_ms":         1000,
		"goal_duration_ms":   3600000,
	}}

	preview := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/import/preview", workspaceID), document)
	AssertStatusCode(t, preview, http.StatusOK)
	var previewResult map[string]any
	DecodeJSON(t, preview, &previewResult)
	_ = preview.Body.Close()
	previewCycles := previewResult["cycles"].(map[string]any)
	if previewCycles["create"].(float64) != 1 || previewCycles["update"].(float64) != 0 {
		t.Fatalf("first preview cycle counts = %#v, want 1 create / 0 update", previewCycles)
	}

	importResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/import", workspaceID), map[string]any{"mode": "merge", "document": document})
	AssertStatusCode(t, importResp, http.StatusOK)
	var result map[string]any
	DecodeJSON(t, importResp, &result)
	_ = importResp.Body.Close()
	counts := result["metrics"].(map[string]any)
	if counts["create"].(float64) != 1 {
		t.Fatalf("first import metrics = %#v, want 1 create", counts)
	}

	var cycleCount int
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM item_sla_cycles WHERE origin = 'import' AND source_id = 'jira-cycle-1'`).Scan(&cycleCount); err != nil {
		t.Fatalf("count imported cycles: %v", err)
	}
	if cycleCount != 1 {
		t.Fatalf("imported cycles = %d, want 1", cycleCount)
	}
	var breachJobs int
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM sla_jobs WHERE kind = 'breach'`).Scan(&breachJobs); err != nil {
		t.Fatalf("count breach jobs: %v", err)
	}
	if breachJobs != 1 {
		var nextDeadline, origin, status string
		_ = server.DB().QueryRow(`SELECT COALESCE(next_deadline_at, ''), origin, status FROM item_sla_cycles WHERE source_id = 'jira-cycle-1'`).Scan(&nextDeadline, &origin, &status)
		t.Fatalf("breach jobs = %d, want 1 for the imported ongoing cycle (deadline=%q origin=%q status=%q)", breachJobs, nextDeadline, origin, status)
	}

	// The goal's JQL is translated to executable QL and the metric is active.
	var ql, status string
	var active bool
	if err := server.DB().QueryRow(`SELECT g.ql_query, g.import_status, m.is_active FROM sla_goals g JOIN sla_metrics m ON m.id = g.metric_id WHERE g.source_id = 'goal-1'`).Scan(&ql, &status, &active); err != nil {
		t.Fatalf("load imported goal: %v", err)
	}
	if !strings.Contains(ql, "1 = 1") {
		t.Fatalf("imported goal ql = %q, want the translated project guard", ql)
	}
	if status != "ready" || !active {
		t.Fatalf("imported goal status/active = %q/%v", status, active)
	}

	// Re-import: update, not duplicate.
	secondPreview := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/import/preview", workspaceID), document)
	AssertStatusCode(t, secondPreview, http.StatusOK)
	var secondPreviewResult map[string]any
	DecodeJSON(t, secondPreview, &secondPreviewResult)
	_ = secondPreview.Body.Close()
	secondPreviewCycles := secondPreviewResult["cycles"].(map[string]any)
	if secondPreviewCycles["create"].(float64) != 0 || secondPreviewCycles["update"].(float64) != 1 {
		t.Fatalf("re-import preview cycle counts = %#v, want 0 create / 1 update", secondPreviewCycles)
	}
	second := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/import", workspaceID), map[string]any{"mode": "merge", "document": document})
	AssertStatusCode(t, second, http.StatusOK)
	var secondResult map[string]any
	DecodeJSON(t, second, &secondResult)
	_ = second.Body.Close()
	secondCounts := secondResult["metrics"].(map[string]any)
	if secondCounts["update"].(float64) != 1 || secondCounts["create"].(float64) != 0 {
		t.Fatalf("re-import metrics = %#v, want 1 update / 0 create", secondCounts)
	}
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM item_sla_cycles WHERE source_id = 'jira-cycle-1'`).Scan(&cycleCount); err != nil {
		t.Fatalf("count cycles after re-import: %v", err)
	}
	if cycleCount != 1 {
		t.Fatalf("cycles after re-import = %d, want 1", cycleCount)
	}
	cycleCounts := secondResult["cycles"].(map[string]any)
	if cycleCounts["create"].(float64) != 0 || cycleCounts["update"].(float64) != 1 {
		t.Fatalf("re-import cycle counts = %#v, want 0 create / 1 update", cycleCounts)
	}
}

// TestSLAImportNeedsAttentionKeepsGoalInactive asserts an untranslated JQL
// clause is preserved and never silently widened.
func TestSLAImportNeedsAttentionKeepsGoalInactive(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Import Attention", shortKey("SIA"))

	document := slaImportDocument("jira-cal-attention", "jira-sla-attention")
	goals := document["metrics"].([]map[string]any)[0]["goals"].([]map[string]any)
	goals[0]["jql"] = "sprint = 42"

	importResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/import", workspaceID), map[string]any{"mode": "merge", "document": document})
	AssertStatusCode(t, importResp, http.StatusOK)
	var result map[string]any
	DecodeJSON(t, importResp, &result)
	_ = importResp.Body.Close()
	if len(result["needs_attention"].([]any)) == 0 {
		t.Fatalf("result needs_attention = %#v, want at least one", result["needs_attention"])
	}

	var ql, status string
	var original *string
	var active bool
	if err := server.DB().QueryRow(`SELECT g.ql_query, g.import_status, g.original_jql, m.is_active FROM sla_goals g JOIN sla_metrics m ON m.id = g.metric_id WHERE g.source_id = 'goal-1'`).Scan(&ql, &status, &original, &active); err != nil {
		t.Fatalf("load needs-attention goal: %v", err)
	}
	if status != "needs_attention" || active {
		t.Fatalf("status/active = %q/%v, want needs_attention/false", status, active)
	}
	if original == nil || *original != "sprint = 42" {
		t.Fatalf("original_jql = %v, want the source expression", original)
	}
}

// TestSLAImportReplaceRemovesStaleImportedConfig asserts replace mode
// reconciles imported metrics that no longer appear in the document.
func TestSLAImportReplaceRemovesStaleImportedConfig(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Import Replace", shortKey("SIR"))

	first := slaImportDocument("jira-cal-replace", "jira-sla-replace")
	firstResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/import", workspaceID), map[string]any{"mode": "merge", "document": first})
	AssertStatusCode(t, firstResp, http.StatusOK)
	_ = firstResp.Body.Close()

	// Replace with an empty document: the imported metric and calendar go away.
	empty := map[string]any{"calendars": []any{}, "metrics": []any{}, "cycles": []any{}}
	replaceResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/import", workspaceID), map[string]any{"mode": "replace", "document": empty})
	AssertStatusCode(t, replaceResp, http.StatusOK)
	_ = replaceResp.Body.Close()

	var metricCount, calendarCount int
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM sla_metrics WHERE source = 'jira'`).Scan(&metricCount); err != nil {
		t.Fatalf("count metrics: %v", err)
	}
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM working_calendars WHERE source = 'jira'`).Scan(&calendarCount); err != nil {
		t.Fatalf("count calendars: %v", err)
	}
	if metricCount != 0 || calendarCount != 0 {
		t.Fatalf("after replace metrics/calendars = %d/%d, want 0/0", metricCount, calendarCount)
	}
}

func TestSLAImportKeepsInvalidQLAndConditionsInactive(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Invalid QL", shortKey("SIQ"))
	invalidQL := slaImportDocument("calendar-invalid-ql", "metric-invalid-ql")
	goals := invalidQL["metrics"].([]map[string]any)[0]["goals"].([]map[string]any)
	goals[0]["ql"] = "priority ="
	response := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/import", workspaceID), map[string]any{"mode": "merge", "document": invalidQL})
	AssertStatusCode(t, response, http.StatusOK)
	var qlResult map[string]any
	DecodeJSON(t, response, &qlResult)
	_ = response.Body.Close()
	if len(qlResult["needs_attention"].([]any)) == 0 {
		t.Fatalf("invalid QL needs_attention = %#v, want a validation issue", qlResult["needs_attention"])
	}
	var active bool
	var goalStatus string
	if err := server.DB().QueryRow(`SELECT m.is_active, g.import_status FROM sla_metrics m JOIN sla_goals g ON g.metric_id = m.id WHERE m.source_id = 'metric-invalid-ql'`).Scan(&active, &goalStatus); err != nil {
		t.Fatalf("load invalid QL metric: %v", err)
	}
	if active || goalStatus != "needs_attention" {
		t.Fatalf("invalid QL metric active/status = %v/%q, want false/needs_attention", active, goalStatus)
	}

	conditionWorkspaceID, _ := CreateTestWorkspace(t, server, "SLA Invalid Condition", shortKey("SIC"))
	invalidCondition := slaImportDocument("calendar-invalid-condition", "metric-invalid-condition")
	conditions := invalidCondition["metrics"].([]map[string]any)[0]["conditions"].([]map[string]any)
	conditions[0]["phase"] = "stop"
	conditions[0]["condition_type"] = "unknown_stop"
	response = MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/import", conditionWorkspaceID), map[string]any{"mode": "merge", "document": invalidCondition})
	AssertStatusCode(t, response, http.StatusOK)
	var conditionResult map[string]any
	DecodeJSON(t, response, &conditionResult)
	_ = response.Body.Close()
	if len(conditionResult["needs_attention"].([]any)) == 0 {
		t.Fatalf("unsupported condition needs_attention = %#v, want a validation issue", conditionResult["needs_attention"])
	}
	if err := server.DB().QueryRow(`SELECT is_active FROM sla_metrics WHERE source_id = 'metric-invalid-condition'`).Scan(&active); err != nil {
		t.Fatalf("load unsupported-condition metric: %v", err)
	}
	if active {
		t.Fatal("metric with unsupported stop condition remained active")
	}
}

func TestSLAImportMapsJiraPriorityAliases(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Priority Aliases", shortKey("SPA"))
	document := slaImportDocument("calendar-aliases", "metric-aliases")
	goal := document["metrics"].([]map[string]any)[0]["goals"].([]map[string]any)[0]
	goal["targets"] = []map[string]any{
		{"source_id": "target-highest", "position": 0, "priority": "Highest", "target_ms": 3600000, "calendar_source_id": "calendar-aliases"},
		{"source_id": "target-lowest", "position": 1, "priority": "Lowest", "target_ms": 7200000, "calendar_source_id": "calendar-aliases"},
	}
	response := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/import", workspaceID), map[string]any{"mode": "merge", "document": document})
	AssertStatusCode(t, response, http.StatusOK)
	var result map[string]any
	DecodeJSON(t, response, &result)
	_ = response.Body.Close()
	if len(result["needs_attention"].([]any)) != 0 {
		t.Fatalf("priority alias needs_attention = %#v, want none", result["needs_attention"])
	}
	for _, expected := range []struct{ sourceID, priority string }{{"target-highest", "Critical"}, {"target-lowest", "Low"}} {
		var ql string
		if err := server.DB().QueryRow(`SELECT g.ql_query FROM sla_goal_targets t JOIN sla_goals g ON g.id = t.goal_id WHERE t.source_id = ?`, expected.sourceID).Scan(&ql); err != nil {
			t.Fatalf("load goal for %s: %v", expected.sourceID, err)
		}
		if !strings.Contains(ql, "priority = '"+expected.priority+"'") {
			t.Fatalf("goal query for %s = %q, want priority = '%s'", expected.sourceID, ql, expected.priority)
		}
	}
}

func TestSLAImportRejectsDuplicateMetricSourceIDsBeforeWrites(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Duplicate IDs", shortKey("SDI"))
	document := slaImportDocument("calendar-duplicate", "metric-duplicate")
	metrics := document["metrics"].([]map[string]any)
	document["metrics"] = append(metrics, metrics[0])

	response := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/import", workspaceID), map[string]any{"mode": "merge", "document": document})
	AssertStatusCode(t, response, http.StatusBadRequest)
	_ = response.Body.Close()

	var count int
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM sla_metrics WHERE workspace_id = ? AND source = 'jira'`, workspaceID).Scan(&count); err != nil {
		t.Fatalf("count metrics after rejected import: %v", err)
	}
	if count != 0 {
		t.Fatalf("persisted imported metrics = %d, want 0", count)
	}
}

func TestSLAImportRejectsMissingCycleSourceIDBeforeWrites(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Missing Source ID", shortKey("SMS"))
	itemID := CreateTestItem(t, server, workspaceID, "Import item")
	document := slaImportDocument("calendar-missing-cycle-id", "metric-missing-cycle-id")
	document["cycles"] = []map[string]any{{
		"source_id": "", "metric_source_id": "metric-missing-cycle-id", "item_id": itemID,
		"calendar_source_id": "calendar-missing-cycle-id", "goal_source_id": "goal-1", "status": "ongoing",
		"started_at": "2026-01-01T00:00:00Z", "goal_duration_ms": 3600000,
	}}
	response := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/import", workspaceID), map[string]any{"mode": "merge", "document": document})
	AssertStatusCode(t, response, http.StatusBadRequest)
	_ = response.Body.Close()
	var calendars, metrics int
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM working_calendars WHERE workspace_id = ? AND source = 'jira'`, workspaceID).Scan(&calendars); err != nil {
		t.Fatalf("count calendars after rejected import: %v", err)
	}
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM sla_metrics WHERE workspace_id = ? AND source = 'jira'`, workspaceID).Scan(&metrics); err != nil {
		t.Fatalf("count metrics after rejected import: %v", err)
	}
	if calendars != 0 || metrics != 0 {
		t.Fatalf("persisted calendars/metrics after rejected import = %d/%d, want 0/0", calendars, metrics)
	}
}

func TestSLAImportRejectsCycleItemFromAnotherWorkspace(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Import Owner", shortKey("SIO"))
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "SLA Import Other", shortKey("SIX"))
	otherItemID := CreateTestItem(t, server, otherWorkspaceID, "Other workspace item")
	var otherWorkspaceKey string
	var otherItemNumber int
	if err := server.DB().QueryRow(`SELECT w.key, i.workspace_item_number FROM items i JOIN workspaces w ON w.id = i.workspace_id WHERE i.id = ?`, otherItemID).Scan(&otherWorkspaceKey, &otherItemNumber); err != nil {
		t.Fatalf("load foreign item key: %v", err)
	}
	document := slaImportDocument("calendar-owner", "metric-owner")
	document["cycles"] = []map[string]any{
		{
			"source_id": "cycle-cross-workspace-id", "metric_source_id": "metric-owner", "item_id": otherItemID,
			"calendar_source_id": "calendar-owner", "goal_source_id": "goal-1", "status": "ongoing",
			"started_at": "2026-01-01T00:00:00Z", "goal_duration_ms": 3600000,
		},
		{
			"source_id": "cycle-cross-workspace-key", "metric_source_id": "metric-owner", "item_key": fmt.Sprintf("%s-%d", otherWorkspaceKey, otherItemNumber),
			"calendar_source_id": "calendar-owner", "goal_source_id": "goal-1", "status": "ongoing",
			"started_at": "2026-01-01T00:00:00Z", "goal_duration_ms": 3600000,
		},
	}

	preview := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/import/preview", workspaceID), document)
	AssertStatusCode(t, preview, http.StatusOK)
	var previewResult map[string]any
	DecodeJSON(t, preview, &previewResult)
	_ = preview.Body.Close()
	if previewResult["cycles"].(map[string]any)["skip"].(float64) != 2 || len(previewResult["needs_attention"].([]any)) != 2 {
		t.Fatalf("cross-workspace preview = %#v, want two skipped cycles and two issues", previewResult)
	}

	response := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/import", workspaceID), map[string]any{"mode": "merge", "document": document})
	AssertStatusCode(t, response, http.StatusOK)
	_ = response.Body.Close()
	var count int
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM item_sla_cycles WHERE source_id IN ('cycle-cross-workspace-id', 'cycle-cross-workspace-key')`).Scan(&count); err != nil {
		t.Fatalf("count cross-workspace cycles: %v", err)
	}
	if count != 0 {
		t.Fatalf("cross-workspace cycles persisted = %d, want 0", count)
	}
}

func TestSLAImportPreservesPausedCycleFactsAndRawPayloads(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Imported Facts", shortKey("SIF"))
	itemID := CreateTestItem(t, server, workspaceID, "Imported paused item")
	document := slaImportDocument("calendar-facts", "metric-facts")
	calendar := document["calendars"].([]map[string]any)[0]
	calendar["source_payload"] = map[string]any{"calendar_marker": "kept"}
	metric := document["metrics"].([]map[string]any)[0]
	metric["source_payload"] = map[string]any{"metric_marker": "kept"}
	condition := metric["conditions"].([]map[string]any)[0]
	condition["source_payload"] = map[string]any{"condition_marker": "kept"}
	goal := metric["goals"].([]map[string]any)[0]
	goal["source_payload"] = map[string]any{"goal_marker": "kept"}
	target := goal["targets"].([]map[string]any)[0]
	target["source_payload"] = map[string]any{"target_marker": "kept"}
	pausedAt := "2026-01-01T01:00:00Z"
	remainingAtPause := int64(1234)
	remaining := int64(2345)
	withinCalendarHours := false
	breached := true
	document["cycles"] = []map[string]any{{
		"source_id": "cycle-facts", "source_payload": map[string]any{"cycle_marker": "kept"},
		"metric_source_id": "metric-facts", "item_id": itemID, "goal_source_id": "goal-1",
		"calendar_source_id": "calendar-facts", "cycle_no": 1, "status": "ongoing",
		"started_at": "2026-01-01T00:00:00Z", "elapsed_ms": 1000, "goal_duration_ms": 3600000,
		"remaining_ms": remaining, "remaining_at_pause_ms": remainingAtPause,
		"within_calendar_hours": withinCalendarHours, "breached": breached,
		"paused": true, "pause_started_at": pausedAt,
	}}

	response := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/import", workspaceID), map[string]any{"mode": "merge", "document": document})
	AssertStatusCode(t, response, http.StatusOK)
	_ = response.Body.Close()

	var storedRemaining, storedPauseRemaining int64
	var storedWithinHours, storedBreached bool
	var nextDeadline sql.NullTime
	var querySnapshot, cyclePayload string
	if err := server.DB().QueryRow(`SELECT remaining_ms, remaining_at_pause_ms, within_calendar_hours, breached, next_deadline_at, goal_query_snapshot, source_payload FROM item_sla_cycles WHERE source_id = 'cycle-facts'`).Scan(&storedRemaining, &storedPauseRemaining, &storedWithinHours, &storedBreached, &nextDeadline, &querySnapshot, &cyclePayload); err != nil {
		t.Fatalf("load imported cycle facts: %v", err)
	}
	var goalQuery string
	if err := server.DB().QueryRow(`SELECT ql_query FROM sla_goals WHERE source_id = 'goal-1'`).Scan(&goalQuery); err != nil {
		t.Fatalf("load imported goal query: %v", err)
	}
	if storedRemaining != remaining || storedPauseRemaining != remainingAtPause || storedWithinHours || !storedBreached || nextDeadline.Valid || querySnapshot != goalQuery || !strings.Contains(cyclePayload, "cycle_marker") {
		t.Fatalf("imported cycle facts = remaining:%d pause:%d within:%v breached:%v deadline:%v query:%q payload:%q", storedRemaining, storedPauseRemaining, storedWithinHours, storedBreached, nextDeadline, querySnapshot, cyclePayload)
	}
	var jobCount int
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM sla_jobs j JOIN item_sla_cycles c ON c.id = j.cycle_id WHERE c.source_id = 'cycle-facts'`).Scan(&jobCount); err != nil {
		t.Fatalf("count imported cycle jobs: %v", err)
	}
	if jobCount != 0 {
		t.Fatalf("paused imported cycle jobs = %d, want 0", jobCount)
	}
	for _, table := range []struct{ name, column string }{
		{"working_calendars", "calendar_marker"}, {"sla_metrics", "metric_marker"},
		{"sla_metric_conditions", "condition_marker"}, {"sla_goals", "goal_marker"},
		{"sla_goal_targets", "target_marker"},
	} {
		var payload string
		query := fmt.Sprintf("SELECT source_payload FROM %s LIMIT 1", table.name)
		if err := server.DB().QueryRow(query).Scan(&payload); err != nil {
			t.Fatalf("load %s payload: %v", table.name, err)
		}
		if !strings.Contains(payload, table.column) {
			t.Fatalf("%s source payload = %q, want %q", table.name, payload, table.column)
		}
	}
}
