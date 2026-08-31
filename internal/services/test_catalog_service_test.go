//go:build test

package services

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

type testCatalogFixture struct {
	db                         *testutils.TestDB
	setSvc                     *TestSetService
	templateSvc                *TestRunTemplateService
	workspaceOne, workspaceTwo int
	caseOne, caseTwo           int
}

func newTestCatalogFixture(t *testing.T) testCatalogFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	if _, err := tdb.Exec(`INSERT INTO workspaces (id, name, key, active) VALUES (2, 'Foreign workspace', 'FOREIGN', true)`); err != nil {
		t.Fatalf("seed foreign workspace: %v", err)
	}

	caseSvc := NewTestCaseService(tdb.GetDatabase())
	caseOne, err := caseSvc.Create(data.WorkspaceID, TestCaseCreateRequest{Title: "Workspace one case"})
	if err != nil {
		t.Fatalf("create workspace one case: %v", err)
	}
	caseTwo, err := caseSvc.Create(2, TestCaseCreateRequest{Title: "Workspace two case"})
	if err != nil {
		t.Fatalf("create workspace two case: %v", err)
	}

	return testCatalogFixture{
		db: tdb, setSvc: NewTestSetService(tdb.GetDatabase()),
		templateSvc:  NewTestRunTemplateService(tdb.GetDatabase()),
		workspaceOne: data.WorkspaceID, workspaceTwo: 2,
		caseOne: caseOne.ID, caseTwo: caseTwo.ID,
	}
}

func TestTestSetService_OwnsSanitizationMilestoneAndCaseWorkspaceRules(t *testing.T) {
	f := newTestCatalogFixture(t)
	if _, err := f.db.Exec(`
		INSERT INTO milestones (id, name, status, is_global, workspace_id)
		VALUES (2101, 'Foreign milestone', 'planning', false, 2)
	`); err != nil {
		t.Fatalf("seed foreign milestone: %v", err)
	}

	foreignMilestoneID := 2101
	if _, err := f.setSvc.Create(f.workspaceOne, models.TestSet{
		Name: "Rejected set", MilestoneID: &foreignMilestoneID,
	}); !errors.Is(err, ErrTestSetMilestoneNotFound) {
		t.Fatalf("foreign milestone error = %v, want ErrTestSetMilestoneNotFound", err)
	}

	set, err := f.setSvc.Create(f.workspaceOne, models.TestSet{
		Name:        "<script>bad()</script>Shared set",
		Description: "before<script>bad()</script>after",
	})
	if err != nil {
		t.Fatalf("create set: %v", err)
	}
	if set.Name != "Shared set" || strings.Contains(set.Description, "<script>") {
		t.Fatalf("set was not normalized: %+v", set)
	}

	if err := f.setSvc.AddCase(set.ID, f.caseOne, f.workspaceOne); err != nil {
		t.Fatalf("add same-workspace case: %v", err)
	}
	if err := f.setSvc.AddCase(set.ID, f.caseTwo, f.workspaceOne); !errors.Is(err, ErrTestSetCaseNotFound) {
		t.Fatalf("add foreign case error = %v, want ErrTestSetCaseNotFound", err)
	}
	if err := f.setSvc.RemoveCase(set.ID, f.caseTwo, f.workspaceOne); !errors.Is(err, ErrTestSetCaseNotFound) {
		t.Fatalf("remove foreign case error = %v, want ErrTestSetCaseNotFound", err)
	}

	cases, err := f.setSvc.ListCases(set.ID, f.workspaceOne)
	if err != nil {
		t.Fatalf("list set cases: %v", err)
	}
	if len(cases) != 1 || cases[0].ID != f.caseOne {
		t.Fatalf("set cases = %+v, want only case %d", cases, f.caseOne)
	}
}

func TestTestRunTemplateService_UsesSharedRunLifecycleAndWorkspaceValidation(t *testing.T) {
	f := newTestCatalogFixture(t)
	setOne, err := f.setSvc.Create(f.workspaceOne, models.TestSet{Name: "Execution set"})
	if err != nil {
		t.Fatalf("create execution set: %v", err)
	}
	if err := f.setSvc.AddCase(setOne.ID, f.caseOne, f.workspaceOne); err != nil {
		t.Fatalf("add execution case: %v", err)
	}
	setTwo, err := f.setSvc.Create(f.workspaceTwo, models.TestSet{Name: "Foreign set"})
	if err != nil {
		t.Fatalf("create foreign set: %v", err)
	}

	if _, err := f.templateSvc.Create(f.workspaceOne, models.TestRunTemplate{
		SetID: setTwo.ID, Name: "Rejected template",
	}); !errors.Is(err, ErrTestRunTemplateSetNotFound) {
		t.Fatalf("foreign set error = %v, want ErrTestRunTemplateSetNotFound", err)
	}

	template, err := f.templateSvc.Create(f.workspaceOne, models.TestRunTemplate{
		SetID:       setOne.ID,
		Name:        "<script>bad()</script>Release template",
		Description: "before<script>bad()</script><br/>after",
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if template.Name != "Release template" || strings.Contains(template.Description, "<script>") || !strings.Contains(template.Description, "<br />") {
		t.Fatalf("template was not normalized: %+v", template)
	}

	for execution := 1; execution <= 2; execution++ {
		run, err := f.templateSvc.Execute(template.ID, f.workspaceOne)
		if err != nil {
			t.Fatalf("execute template %d: %v", execution, err)
		}
		wantName := "Release template - Run " + strconv.Itoa(execution)
		if run.Name != wantName || run.TemplateID != template.ID || run.SetID != setOne.ID {
			t.Fatalf("execution %d run = %+v, want name %q template %d set %d", execution, run, wantName, template.ID, setOne.ID)
		}
		var status string
		if err := f.db.QueryRow(`SELECT status FROM test_results WHERE run_id = ? AND test_case_id = ?`, run.ID, f.caseOne).Scan(&status); err != nil {
			t.Fatalf("read execution %d result: %v", execution, err)
		}
		if status != "not_run" {
			t.Fatalf("execution %d result status = %q, want not_run", execution, status)
		}
	}
}
