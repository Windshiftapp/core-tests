//go:build test

package services

import (
	"errors"
	"strings"
	"testing"

	"windshift/internal/gherkin"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// newBDDExecutionFixture seeds one BDD test case in a run with a frozen
// snapshot, one outline example result, and one recorded step result on it.
func newBDDExecutionFixture(t *testing.T) *TestRunService {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	tdb.SeedTestData(t)

	spec := `{"feature_name":"Login","scenario_keyword":"Scenario Outline","scenario_name":"valid credentials","background":[],"steps":[{"keyword":"Given","text":"a browser"},{"keyword":"When","text":"the admin logs in"}],"examples":[{"name":"","header":["role"],"rows":[["admin"]]}]}`
	seedStatements := []string{
		`INSERT INTO test_cases (id, workspace_id, title, format, priority) VALUES (1101, 1, 'valid credentials', 'bdd', 'medium')`,
		`INSERT INTO test_case_bdd (test_case_id, gherkin, feature_name, scenario_keyword, spec) VALUES (1101, 'Feature: Login', 'Login', 'Scenario Outline', '` + spec + `')`,
		`INSERT INTO test_sets (id, workspace_id, name, description) VALUES (1201, 1, 'BDD set', '')`,
		`INSERT INTO set_test_cases (set_id, test_case_id) VALUES (1201, 1101)`,
		`INSERT INTO test_runs (id, workspace_id, set_id, name) VALUES (1301, 1, 1201, 'BDD run')`,
		`INSERT INTO test_results (id, run_id, test_case_id, status) VALUES (1401, 1301, 1101, 'not_run')`,
		`INSERT INTO test_run_case_snapshots (run_id, test_case_id, title, preconditions, gherkin, spec) VALUES (1301, 1101, 'valid credentials', '', 'Feature: Login', '` + spec + `')`,
		`INSERT INTO test_example_results (run_id, test_case_id, example_index, row_values, status, actual_result, notes) VALUES (1301, 1101, 0, '{"role":"admin"}', 'not_run', 'earlier output', 'first pass notes')`,
		`INSERT INTO test_example_step_results (example_result_id, step_number, status, actual_result, notes, item_id)
			SELECT id, 1, 'passed', 'step output', 'original step notes', NULL FROM test_example_results WHERE run_id = 1301`,
	}
	for _, statement := range seedStatements {
		if _, err := tdb.Exec(statement); err != nil {
			t.Fatalf("seed BDD execution fixture: %v", err)
		}
	}
	return NewTestRunService(tdb.GetDatabase())
}

func bddExampleResultID(t *testing.T, service *TestRunService) int {
	t.Helper()
	results, err := service.ListExampleResults(1301, 1)
	if err != nil {
		t.Fatalf("list example results: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("example results = %d, want 1", len(results))
	}
	return results[0].ID
}

func TestTestRunService_UpdateExampleResultMergePatchPreservesUntouchedFields(t *testing.T) {
	service := newBDDExecutionFixture(t)

	notes := "updated notes"
	updated, err := service.UpdateExampleResult(1, 1301, 1101, 0, TestExampleResultUpdateRequest{Notes: &notes})
	if err != nil {
		t.Fatalf("notes-only patch: %v", err)
	}
	if updated.Notes != "updated notes" {
		t.Fatalf("notes = %q, want %q", updated.Notes, "updated notes")
	}
	if updated.Status != "not_run" || updated.ActualResult != "earlier output" {
		t.Fatalf("notes-only patch clobbered status/actual_result: %+v", updated)
	}

	status := "passed"
	updated, err = service.UpdateExampleResult(1, 1301, 1101, 0, TestExampleResultUpdateRequest{Status: &status})
	if err != nil {
		t.Fatalf("status-only patch: %v", err)
	}
	if updated.Status != "passed" {
		t.Fatalf("status = %q, want passed", updated.Status)
	}
	if updated.Notes != "updated notes" || updated.ActualResult != "earlier output" {
		t.Fatalf("status-only patch clobbered notes/actual_result: %+v", updated)
	}

	persisted, err := service.ListExampleResults(1301, 1)
	if err != nil {
		t.Fatalf("reload example results: %v", err)
	}
	if persisted[0].Status != "passed" || persisted[0].Notes != "updated notes" || persisted[0].ActualResult != "earlier output" {
		t.Fatalf("persisted example result = %+v, want patched fields with history intact", persisted[0])
	}
}

func TestTestRunService_UpdateExampleResultRejectsInvalidStatus(t *testing.T) {
	service := newBDDExecutionFixture(t)

	invalid := "exploded"
	if _, err := service.UpdateExampleResult(1, 1301, 1101, 0, TestExampleResultUpdateRequest{Status: &invalid}); !errors.Is(err, ErrInvalidTestResultStatus) {
		t.Fatalf("invalid status error = %v, want ErrInvalidTestResultStatus", err)
	}
}

func TestTestRunService_UpdateExampleResultExampleIndexBounds(t *testing.T) {
	service := newBDDExecutionFixture(t)

	if _, err := service.UpdateExampleResult(1, 1301, 1101, 5, TestExampleResultUpdateRequest{}); err == nil {
		t.Fatal("out-of-range example index accepted")
	}
}

func TestTestRunService_UpdateExampleStepResultMergePatchPreservesUntouchedFields(t *testing.T) {
	service := newBDDExecutionFixture(t)
	exampleResultID := bddExampleResultID(t, service)

	notes := "repro steps attached"
	if err := service.UpdateExampleStepResult(1, 1301, 1101, 0, 1, TestExampleStepResultUpdateRequest{Notes: &notes}); err != nil {
		t.Fatalf("notes-only step patch: %v", err)
	}
	steps, err := service.bddRepo.FindStepResultsForExample(exampleResultID)
	if err != nil {
		t.Fatalf("load step results: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("step results = %d, want 1", len(steps))
	}
	step := steps[0]
	if step.Status != "passed" || step.Notes != "repro steps attached" || step.ItemID != nil {
		t.Fatalf("step result = %+v, want status and item preserved with notes updated", step)
	}

	status := "failed"
	if err := service.UpdateExampleStepResult(1, 1301, 1101, 0, 1, TestExampleStepResultUpdateRequest{Status: &status}); err != nil {
		t.Fatalf("status-only step patch: %v", err)
	}
	steps, err = service.bddRepo.FindStepResultsForExample(exampleResultID)
	if err != nil {
		t.Fatalf("reload step results: %v", err)
	}
	if steps[0].Status != "failed" || steps[0].Notes != "repro steps attached" {
		t.Fatalf("step result = %+v, want status updated and notes preserved", steps[0])
	}
}

func TestTestRunService_UpdateExampleStepResultCreatesAndClearsRows(t *testing.T) {
	service := newBDDExecutionFixture(t)
	exampleResultID := bddExampleResultID(t, service)

	// A status-less first write records the implicit not_run default.
	if err := service.UpdateExampleStepResult(1, 1301, 1101, 0, 2, TestExampleStepResultUpdateRequest{}); err != nil {
		t.Fatalf("first write without status: %v", err)
	}
	steps, err := service.bddRepo.FindStepResultsForExample(exampleResultID)
	if err != nil {
		t.Fatalf("load step results: %v", err)
	}
	if len(steps) != 2 || steps[1].StepNumber != 2 || steps[1].Status != "not_run" {
		t.Fatalf("step results = %+v, want a not_run row for step 2", steps)
	}

	// An explicit null item_id clears the link; an absent one keeps it.
	clear := TestExampleStepResultUpdateRequest{ItemIDSet: true}
	if err := service.UpdateExampleStepResult(1, 1301, 1101, 0, 1, clear); err != nil {
		t.Fatalf("clear item link: %v", err)
	}
	steps, err = service.bddRepo.FindStepResultsForExample(exampleResultID)
	if err != nil {
		t.Fatalf("reload step results: %v", err)
	}
	for _, step := range steps {
		if step.ItemID != nil {
			t.Fatalf("step %d item link = %v, want cleared", step.StepNumber, step.ItemID)
		}
	}

	if err := service.UpdateExampleStepResult(1, 1301, 1101, 0, 99, TestExampleStepResultUpdateRequest{}); err == nil {
		t.Fatal("out-of-bounds step number accepted")
	}
}

func TestTestRunService_UpdateExampleStepResultRejectsForeignItem(t *testing.T) {
	service := newBDDExecutionFixture(t)
	foreignItemID := 424242

	if err := service.UpdateExampleStepResult(1, 1301, 1101, 0, 1, TestExampleStepResultUpdateRequest{
		Status: strPtr("passed"), ItemID: &foreignItemID, ItemIDSet: true,
	}); !errors.Is(err, ErrTestRunItemNotFound) {
		t.Fatalf("foreign item error = %v, want ErrTestRunItemNotFound", err)
	}
}

func TestBuildScenarioSpecAppliesBDDCaseRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		source       string
		wantErrText  string
		wantScenario string
	}{
		{
			name:         "single named scenario builds a spec",
			source:       "Feature: Login\nScenario: valid credentials\nGiven a browser\n",
			wantScenario: "valid credentials",
		},
		{
			name:        "multiple scenarios are rejected",
			source:      "Feature: Login\nScenario: one\nGiven a\nScenario: two\nGiven b\n",
			wantErrText: "exactly one Scenario or Scenario Outline, found 2",
		},
		{
			name:        "unnamed scenario is rejected",
			source:      "Feature: Login\nScenario:\nGiven a\n",
			wantErrText: "the scenario needs a name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			spec, errs := BuildScenarioSpec(tt.source)
			if tt.wantErrText == "" {
				if !errs.Empty() {
					t.Fatalf("BuildScenarioSpec errors = %v, want none", errs.Error())
				}
				if spec.ScenarioName != tt.wantScenario {
					t.Fatalf("scenario name = %q, want %q", spec.ScenarioName, tt.wantScenario)
				}
				return
			}
			if errs == nil || errs.Empty() {
				t.Fatalf("BuildScenarioSpec accepted %q", tt.source)
			}
			if !strings.Contains(errs.Error(), tt.wantErrText) {
				t.Fatalf("errors = %q, want text %q", errs.Error(), tt.wantErrText)
			}
		})
	}
}

func TestImportFeatureFileStoresPerScenarioSource(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	tdb.SeedTestData(t)
	service := NewTestCaseService(tdb.GetDatabase())

	source := "Feature: Login\n  Background:\n    Given a browser\n  Scenario: valid credentials\n    Given the login page\n  Scenario Outline: role based access\n    Given the <role> role\n    Examples:\n      | role |\n      | admin |\n"

	created, err := service.ImportFeatureFile(1, nil, source)
	if err != nil {
		t.Fatalf("ImportFeatureFile: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("created %d cases, want 2", len(created))
	}

	wantTitles := []string{"valid credentials", "role based access"}
	for i, wantTitle := range wantTitles {
		stored, err := service.GetBDDContent(created[i].ID)
		if err != nil {
			t.Fatalf("load BDD content for case %d: %v", created[i].ID, err)
		}
		if stored.Gherkin == source {
			t.Fatalf("case %d stored the full feature file as its source", created[i].ID)
		}
		// The stored source must pass the single-scenario rule so the case
		// can be edited without manually deleting the other scenarios.
		spec, errs := BuildScenarioSpec(stored.Gherkin)
		if !errs.Empty() {
			t.Fatalf("stored source for case %d does not validate: %v\nsource:\n%s", created[i].ID, errs.Error(), stored.Gherkin)
		}
		if spec.ScenarioName != wantTitle {
			t.Fatalf("stored source scenario = %q, want %q", spec.ScenarioName, wantTitle)
		}
		if !strings.Contains(stored.Gherkin, wantTitle) {
			t.Fatalf("stored source for case %d lost its scenario:\n%s", created[i].ID, stored.Gherkin)
		}
	}
}

func TestImportFeatureFileRejectsInvalidDocumentsAtomically(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	tdb.SeedTestData(t)
	service := NewTestCaseService(tdb.GetDatabase())

	source := "Feature: Login\nScenario: broken\nAnd cannot be first\n"
	cases, err := service.ImportFeatureFile(1, nil, source)
	if err == nil {
		t.Fatal("ImportFeatureFile accepted a scenario whose first step is And")
	}
	var validationErr *TestBDDValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %T, want *TestBDDValidationError", err)
	}
	if len(cases) != 0 {
		t.Fatalf("created %d cases for an invalid import, want 0", len(cases))
	}
	if _, err := service.GetBDDContent(1); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("BDD content lookup after failed import = %v, want ErrNotFound", err)
	}
}

func TestImportFeatureFileRejectsForeignFolder(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	tdb.SeedTestData(t)
	service := NewTestCaseService(tdb.GetDatabase())

	foreignFolder := 999
	if _, err := service.ImportFeatureFile(1, &foreignFolder, "Feature: F\nScenario: S\nGiven a\n"); err == nil {
		t.Fatal("ImportFeatureFile accepted a folder from outside the workspace")
	}
}

func TestScenarioSpecBackgroundStepsInExecutionOrder(t *testing.T) {
	t.Parallel()

	doc, errs := gherkin.Parse("Feature: F\nBackground:\nGiven seeded\n\nScenario: S\nWhen acting\nThen done\n")
	if !errs.Empty() {
		t.Fatalf("Parse errors: %v", errs.Error())
	}
	spec := doc.ScenarioSpecFrom(&doc.Scenarios[0])
	steps := gherkin.ExecutionSteps(&spec, nil)
	if len(steps) != 3 || steps[0].Text != "seeded" || steps[2].Text != "done" {
		t.Fatalf("execution steps = %+v, want background first then scenario steps", steps)
	}
}

func TestExampleRowBoundsCountsBackgroundThenScenarioSteps(t *testing.T) {
	t.Parallel()

	spec := &gherkin.ScenarioSpec{
		Background: []gherkin.Step{{Keyword: "Given", Text: "seeded"}},
		Steps:      []gherkin.Step{{Keyword: "When", Text: "acting"}, {Keyword: "Then", Text: "done"}},
	}
	if got := exampleRowBounds(spec); got != 3 {
		t.Fatalf("exampleRowBounds = %d, want 3", got)
	}
}
