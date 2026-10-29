package jira

import (
	"strings"
	"testing"
)

// adfNode is a tiny builder for the map[string]interface{} shape Jira returns
// for ADF bodies, so the ScanADF tests stay readable.
func adfNode(nodeType string, children ...interface{}) map[string]interface{} {
	n := map[string]interface{}{"type": nodeType}
	if len(children) > 0 {
		n["content"] = children
	}
	return n
}

func TestScanADF_NilAndPlainStringYieldEmpty(t *testing.T) {
	t.Parallel()
	if got := ScanADF(nil); len(got) != 0 {
		t.Errorf("ScanADF(nil) = %v, want empty", got)
	}
	if got := ScanADF("a plain-string body"); len(got) != 0 {
		t.Errorf("ScanADF(string) = %v, want empty", got)
	}
}

func TestScanADF_Histogram(t *testing.T) {
	t.Parallel()
	// doc > [ paragraph > text, table > tableRow > [tableCell > paragraph, tableCell > paragraph] ]
	doc := adfNode("doc",
		adfNode("paragraph", adfNode("text")),
		adfNode("table",
			adfNode("tableRow",
				adfNode("tableCell", adfNode("paragraph")),
				adfNode("tableCell", adfNode("paragraph")),
			),
		),
	)
	got := ScanADF(doc)
	want := map[string]int{
		"doc":       1,
		"paragraph": 3, // one top-level + one per cell
		"text":      1,
		"table":     1,
		"tableRow":  1,
		"tableCell": 2,
	}
	for typ, n := range want {
		if got[typ] != n {
			t.Errorf("ScanADF count[%q] = %d, want %d (full=%v)", typ, got[typ], n, got)
		}
	}
}

func TestUnsupportedADFNodes_StructuralChildrenNotFlagged(t *testing.T) {
	t.Parallel()
	// A table + task list (with their structural children) plus an unsupported
	// decisionList. Only the decision nodes should be reported as lossy.
	counts := ScanADF(adfNode("doc",
		adfNode("table", adfNode("tableRow", adfNode("tableCell", adfNode("paragraph")))),
		adfNode("taskList", adfNode("taskItem")),
		adfNode("decisionList", adfNode("decisionItem")),
	))
	unsupported := UnsupportedADFNodes(counts)

	for _, supported := range []string{"doc", "table", "tableRow", "tableCell", "taskList", "taskItem", "paragraph"} {
		if _, bad := unsupported[supported]; bad {
			t.Errorf("%q flagged unsupported, but the importer renders it", supported)
		}
	}
	if unsupported["decisionList"] != 1 || unsupported["decisionItem"] != 1 {
		t.Errorf("decisionList/decisionItem should be flagged lossy, got %v", unsupported)
	}
}

func TestFieldTypeSeverity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   WindshiftFieldType
		want Severity
	}{
		{FieldTypeUnmapped, SeverityBlocked},
		{FieldTypeUser, SeverityClean},
		{FieldTypeMultiUser, SeverityClean},
		{FieldTypeAsset, SeverityClean}, // assets now import as CMDB objects
		{FieldTypeText, SeverityClean},  // values are written now (was lossy)
		{FieldTypeSelect, SeverityClean},
		{FieldTypeNumber, SeverityClean},
		{FieldTypeDate, SeverityClean},
		{FieldTypeMilestone, SeverityClean},
		{FieldTypeIteration, SeverityClean},
	}
	for _, c := range cases {
		got, reason := fieldTypeSeverity(c.in)
		if got != c.want {
			t.Errorf("fieldTypeSeverity(%q) = %q, want %q", c.in, got, c.want)
		}
		if reason == "" {
			t.Errorf("fieldTypeSeverity(%q) returned empty reason", c.in)
		}
	}
}

func TestClassifyField_AppendsNotesAndCarriesType(t *testing.T) {
	t.Parallel()
	f := ClassifyField(FieldMappingSuggestion{
		JiraFieldName:      "Story Points",
		JiraFieldType:      "com.atlassian.jira.plugin.system.customfieldtypes:float",
		WindshiftFieldType: FieldTypeNumber,
		Notes:              "Rounded to an integer.",
	}, 7)

	if f.Severity != SeverityClean {
		t.Errorf("severity = %q, want clean", f.Severity)
	}
	if f.Entity != "Custom field: Story Points" {
		t.Errorf("entity = %q", f.Entity)
	}
	if f.JiraType == "" || f.UsageCount != 7 {
		t.Errorf("jiraType/usage not carried: %+v", f)
	}
	if want := "Rounded to an integer."; f.Reason == "" || f.Reason[len(f.Reason)-len(want):] != want {
		t.Errorf("notes not appended to reason: %q", f.Reason)
	}
}

func TestClassifyFieldMarksRawPreservationLossy(t *testing.T) {
	t.Parallel()
	f := ClassifyField(FieldMappingSuggestion{
		JiraFieldName:      "App payload",
		JiraFieldType:      "vendor.app:payload",
		WindshiftFieldType: FieldTypeTextarea,
		PreserveRaw:        true,
	}, 3)

	if f.Severity != SeverityLossy {
		t.Errorf("severity = %q, want lossy", f.Severity)
	}
	if f.UsageCount != 3 {
		t.Errorf("usage count = %d, want 3", f.UsageCount)
	}
}

func TestClassifyFieldMarksJiraDateTimeEditingLossy(t *testing.T) {
	t.Parallel()

	suggestion := MapJiraFieldToWindshift(JiraCustomField{
		ID:   "customfield_77",
		Name: "Deployment time",
		Schema: &JiraFieldSchema{
			Type:   "datetime",
			Custom: "com.atlassian.jira.plugin.system.customfieldtypes:datetime",
		},
	})
	finding := ClassifyField(suggestion, 4)
	if finding.Severity != SeverityLossy {
		t.Fatalf("datetime severity = %q, want lossy (%#v)", finding.Severity, finding)
	}
	if !strings.Contains(finding.Reason, "calendar-date") {
		t.Fatalf("datetime reason = %q, want explicit calendar-date loss", finding.Reason)
	}
}

func TestClassifyFieldTreatsJiraRankAsNativeOrdering(t *testing.T) {
	t.Parallel()
	f := ClassifyField(FieldMappingSuggestion{
		JiraFieldName:      "Rank",
		JiraFieldType:      "com.pyxis.greenhopper.jira:gh-lexo-rank",
		WindshiftFieldType: FieldTypeUnmapped,
		CanMap:             false,
	}, 12)

	if f.Severity != SeverityClean {
		t.Fatalf("Rank severity = %q, want clean", f.Severity)
	}
	if !strings.Contains(f.Reason, "fractional indexes") {
		t.Fatalf("Rank reason = %q, want fractional-index import contract", f.Reason)
	}
}

func TestScoreFindings_Boundaries(t *testing.T) {
	t.Parallel()
	if score, by := ScoreFindings(nil); score != 100 {
		t.Errorf("empty findings score = %d, want 100 (by=%v)", score, by)
	}
	// bySeverity must always be initialised for all three keys.
	_, by := ScoreFindings(nil)
	for _, sev := range []Severity{SeverityClean, SeverityLossy, SeverityBlocked} {
		if _, ok := by[sev]; !ok {
			t.Errorf("bySeverity missing key %q", sev)
		}
	}

	allClean := []Finding{{Severity: SeverityClean, UsageCount: 3}, {Severity: SeverityClean, UsageCount: 10}}
	if score, _ := ScoreFindings(allClean); score != 100 {
		t.Errorf("all-clean score = %d, want 100", score)
	}
	allBlocked := []Finding{{Severity: SeverityBlocked, UsageCount: 5}}
	if score, _ := ScoreFindings(allBlocked); score != 0 {
		t.Errorf("all-blocked score = %d, want 0", score)
	}
	oneLossy := []Finding{{Severity: SeverityLossy, UsageCount: 4}}
	if score, _ := ScoreFindings(oneLossy); score != 50 {
		t.Errorf("single-lossy score = %d, want 50", score)
	}
}

func TestScoreFindings_UsageWeighting(t *testing.T) {
	t.Parallel()
	// A blocked concept touched by one issue barely dents an otherwise-clean
	// instance; the same concept touched by thousands dominates.
	light := ScoreFindingsScore([]Finding{
		{Severity: SeverityClean, UsageCount: 1000},
		{Severity: SeverityBlocked, UsageCount: 1},
	})
	heavy := ScoreFindingsScore([]Finding{
		{Severity: SeverityClean, UsageCount: 1},
		{Severity: SeverityBlocked, UsageCount: 1000},
	})
	if light <= heavy {
		t.Errorf("usage weighting wrong: light=%d should be >> heavy=%d", light, heavy)
	}
	if light < 99 {
		t.Errorf("a single lightly-used blocked finding should barely dent the score, got %d", light)
	}
	if heavy > 1 {
		t.Errorf("a heavily-used blocked finding should dominate, got %d", heavy)
	}
}

func TestScoreFindings_BySeverityCountsDistinctFindings(t *testing.T) {
	t.Parallel()
	_, by := ScoreFindings([]Finding{
		{Severity: SeverityClean, UsageCount: 1},
		{Severity: SeverityClean, UsageCount: 999},
		{Severity: SeverityLossy, UsageCount: 1},
		{Severity: SeverityBlocked, UsageCount: 1},
	})
	if by[SeverityClean] != 2 || by[SeverityLossy] != 1 || by[SeverityBlocked] != 1 {
		t.Errorf("bySeverity tally = %v, want clean:2 lossy:1 blocked:1", by)
	}
}

func TestScoreFindings_SchemaOnlyFloorsUsageAtOne(t *testing.T) {
	t.Parallel()
	// A finding with UsageCount 0 (schema-only) still registers, weighted as 1.
	if score, _ := ScoreFindings([]Finding{{Severity: SeverityLossy, UsageCount: 0}}); score != 50 {
		t.Errorf("schema-only lossy score = %d, want 50", score)
	}
}

// ScoreFindingsScore is a tiny test helper returning just the score.
func ScoreFindingsScore(f []Finding) int {
	s, _ := ScoreFindings(f)
	return s
}
