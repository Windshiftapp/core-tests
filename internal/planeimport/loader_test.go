package planeimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
)

// newTestDB builds a fully initialized Windshift schema on a throwaway SQLite
// file and seeds the minimum a Plane import needs: one workspace and two
// users. Statuses, categories, priorities, and the default workflow come from
// Initialize's own default data — re-seeding them by hand would test a
// fixture, not the product, and the vocabulary pass is a test of exactly what
// Initialize does NOT have: no Backlog, no Blocked, no Cancelled, no Urgent.
func newTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDBWithPoolSizes(filepath.Join(t.TempDir(), "import.db"), 1, 1)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize schema: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	seed(t, db, "workspaces", map[string]any{"key": "TEST", "name": "Test", "is_personal": false, "active": true})

	oskars := seed(t, db, "users", map[string]any{
		"email": "oskars_germovs@inbox.lv", "username": "oskars_germovs",
		"first_name": "Oskars", "last_name": "Germovs", "is_active": true,
	})
	gnarl := seed(t, db, "users", map[string]any{
		"email": "gnarl@solum-designum.eu", "username": "gnarl",
		"first_name": "Gnarl", "last_name": "", "is_active": true,
		"is_agent": true,
	})
	look := func(table, name string) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRow(fmt.Sprintf(`SELECT id FROM %s WHERE name = ? LIMIT 1`, table), name).Scan(&id); err != nil {
			t.Fatalf("default %s %q missing after Initialize: %v", table, name, err)
		}
		return id
	}
	seededIDs = struct {
		oskars, gnarl, open, inProg, done int64
	}{oskars, gnarl, look("statuses", "Open"), look("statuses", "In Progress"), look("statuses", "Done")}
	return db
}

var seededIDs struct {
	oskars, gnarl, open, inProg, done int64
}

// seed inserts one row into table with the given column values, defaulting any
// NOT NULL column the caller omitted, and returns the new row id.
func seed(t *testing.T, db database.Database, table string, values map[string]any) int64 {
	t.Helper()
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		t.Fatalf("pragma %s: %v", table, err)
	}
	type col struct {
		name       string
		typ        string
		notNull    bool
		hasDefault bool
		pk         bool
	}
	var cols []col
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			_ = rows.Close()
			t.Fatalf("scan pragma %s: %v", table, err)
		}
		cols = append(cols, col{name: name, typ: colType, notNull: notNull == 1, hasDefault: dflt.Valid, pk: pk == 1})
	}
	_ = rows.Close()

	var names []string
	var holders []string
	var args []any
	provided := map[string]bool{}
	for k := range values {
		provided[strings.ToLower(k)] = true
	}
	for _, c := range cols {
		key := c.name
		if v, ok := values[key]; ok {
			names = append(names, quoteIdent(c.name))
			holders = append(holders, "?")
			args = append(args, v)
			continue
		}
		if provided[c.name] || c.pk || !c.notNull || c.hasDefault {
			continue
		}
		// NOT NULL without default and not provided: give it the honest
		// zero for its type so the seed cannot mask a real schema change.
		names = append(names, quoteIdent(c.name))
		holders = append(holders, "?")
		args = append(args, zeroFor(c.typ))
	}
	var id int64
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING id",
		table, strings.Join(names, ", "), strings.Join(holders, ", "))
	if err := db.QueryRow(q, args...).Scan(&id); err != nil {
		t.Fatalf("seed %s: %v", table, err)
	}
	return id
}

func quoteIdent(s string) string { return `"` + s + `"` }

func zeroFor(typ string) any {
	u := strings.ToUpper(typ)
	switch {
	case strings.Contains(u, "INT"):
		return 0
	case strings.Contains(u, "REAL"), strings.Contains(u, "FLOA"), strings.Contains(u, "DOUB"):
		return 0.0
	default:
		return ""
	}
}

func writeContract(t *testing.T, meta string, records ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "contract.jsonl")
	body := meta + "\n" + strings.Join(records, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write contract: %v", err)
	}
	return path
}

func writeUsers(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "users.json")
	users := []UserRow{
		{ID: "u-oskars", Email: "hakerx1@gmail.com", FirstName: "Oskars", LastName: "Germovs"},
		{ID: "u-gnarl", Email: "gnarl@solum-designum.eu", FirstName: "Gnarl"},
	}
	data, _ := json.Marshal(users)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write users: %v", err)
	}
	return path
}

// containsExact reports whether name appears as its own entry. The report is
// operator-facing: a line that buries a value inside another list, or
// prefixes it, is a line an operator has to parse by hand.
func containsExact(list []string, name string) bool {
	for _, v := range list {
		if v == name {
			return true
		}
	}
	return false
}

// statusID looks a status up BY NAME — after the fidelity pass the source's
// state names are the lookup keys, not any builtin mapping.
func statusID(t *testing.T, db database.Database, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT id FROM statuses WHERE name = ? LIMIT 1`, name).Scan(&id); err != nil {
		t.Fatalf("status %q missing after import: %v", name, err)
	}
	return id
}

// statusCategoryBuiltin reports which status-category builtin_key a status
// lands under: the placement the source's GROUP field chose.
func statusCategoryBuiltin(t *testing.T, db database.Database, name string) (string, bool) {
	t.Helper()
	var builtin string
	var completed bool
	err := db.QueryRow(`
		SELECT c.builtin_key, c.is_completed FROM statuses s
		JOIN status_categories c ON c.id = s.category_id
		WHERE s.name = ? LIMIT 1`, name).Scan(&builtin, &completed)
	if err != nil {
		t.Fatalf("category of status %q: %v", name, err)
	}
	return builtin, completed
}

// fidelityContract is the v2 shape the dumper emits: state/label vocabulary
// records, per-issue state_group + label_ids. The state set mirrors the live
// VECTIS project (including Cancelled and Triage with zero open issues, and
// Blocked whose group is started — the measured fact a name-based map gets
// wrong); labels mirror the real catalog's used rows.
func fidelityContract() (string, []string) {
	meta := `{"type":"meta","meta":{"schema_version":2,"source":"plane","workspace_slug":"solum-designum","project_id":"p-1","project_key":"TEST","dumped_at":"2026-10-26T00:00:00Z"}}`
	return meta, []string{
		`{"type":"state","state":{"id":"s-1","name":"Todo","group":"unstarted","color":"10B981"}}`,
		`{"type":"state","state":{"id":"s-2","name":"In Progress","group":"started","color":"#3B82F6"}}`,
		`{"type":"state","state":{"id":"s-3","name":"Blocked","group":"started","color":"#F97316"}}`,
		`{"type":"state","state":{"id":"s-4","name":"Cancelled","group":"cancelled","color":"#6B7280"}}`,
		`{"type":"state","state":{"id":"s-5","name":"Done","group":"completed","color":"#22C55E"}}`,
		`{"type":"state","state":{"id":"s-6","name":"Triage","group":"triage","color":"#F59E0B"}}`,
		`{"type":"label","label":{"id":"l-1","name":"ready-for-agent","color":"#10B981"}}`,
		`{"type":"label","label":{"id":"l-2","name":"blocker","color":"b60205"}}`,
		// #1 — Done by Gnarl, 2024, carries a label.
		`{"type":"issue","issue":{"id":"i-1","sequence_id":1,"name":"first","state_name":"Done","state_group":"completed","label_ids":["l-1"],"priority":"none","created_at":"2024-01-02T03:04:05Z","updated_at":"2024-01-05T00:00:00Z","created_by_id":"u-gnarl","updated_by_id":"u-gnarl","description_json":"{\"type\":\"doc\",\"content\":[{\"type\":\"paragraph\",\"content\":[{\"type\":\"text\",\"text\":\"body one\"}]}]}"}}`,
		// #2 — In Progress by Oskars (Plane email needs the alias), story points.
		`{"type":"issue","issue":{"id":"i-2","sequence_id":2,"name":"second","state_name":"In Progress","state_group":"started","priority":"medium","estimate_point":"8","created_at":"2024-02-02T00:00:00Z","updated_at":"2024-02-03T00:00:00Z","created_by_id":"u-oskars","updated_by_id":"u-oskars","target_date":"2024-03-01T00:00:00Z"}}`,
		// #3 — soft-deleted: number must stay a gap.
		`{"type":"issue","issue":{"id":"i-3","sequence_id":3,"name":"gone","state_name":"Todo","state_group":"unstarted","priority":"none","created_at":"2024-02-04T00:00:00Z","updated_at":"2024-02-04T00:00:00Z","deleted_at":"2024-02-05T00:00:00Z","created_by_id":"u-oskars","updated_by_id":"u-oskars"}}`,
		// #4 — child of i-2, Todo, labelled blocker.
		`{"type":"issue","issue":{"id":"i-4","sequence_id":4,"name":"child","state_name":"Todo","state_group":"unstarted","label_ids":["l-2"],"priority":"low","parent_id":"i-2","parent_sequence_id":2,"created_at":"2024-02-06T00:00:00Z","updated_at":"2024-02-06T00:00:00Z","created_by_id":"u-oskars","updated_by_id":"u-oskars"}}`,
		// #5 — Cancelled: its own status under its own category, not folded into Done.
		`{"type":"issue","issue":{"id":"i-5","sequence_id":5,"name":"cancelled","state_name":"Cancelled","state_group":"cancelled","priority":"none","created_at":"2024-02-07T00:00:00Z","updated_at":"2024-02-07T00:00:00Z","created_by_id":"u-gnarl","updated_by_id":"u-gnarl"}}`,
		// #6 — Blocked (group started) with a comment and a label.
		`{"type":"issue","issue":{"id":"i-6","sequence_id":6,"name":"commented","state_name":"Blocked","state_group":"started","label_ids":["l-2"],"priority":"none","created_at":"2024-02-08T00:00:00Z","updated_at":"2024-02-08T00:00:00Z","created_by_id":"u-oskars","updated_by_id":"u-oskars"}}`,
		`{"type":"comment","comment":{"id":"c-1","issue_id":"i-6","comment_json":"","comment_html":"","text":"keep me","created_at":"2024-02-09T10:00:00Z","updated_at":"2024-02-09T10:00:00Z","created_by_id":"u-oskars","access":"INTERNAL"}}`,
		// #7 — child of #6 but the PARENT has a higher sequence id: the
		// measured Plane shape where in-pass linking must defer.
		`{"type":"issue","issue":{"id":"i-7","sequence_id":7,"name":"late-parent-child","state_name":"Todo","state_group":"unstarted","priority":"none","parent_id":"i-8","parent_sequence_id":8,"created_at":"2024-02-10T00:00:00Z","updated_at":"2024-02-10T00:00:00Z","created_by_id":"u-oskars","updated_by_id":"u-oskars"}}`,
		`{"type":"issue","issue":{"id":"i-8","sequence_id":8,"name":"late-parent","state_name":"Todo","state_group":"unstarted","priority":"none","created_at":"2024-02-11T00:00:00Z","updated_at":"2024-02-11T00:00:00Z","created_by_id":"u-oskars","updated_by_id":"u-oskars"}}`,
		// #9 — a state GROUP and a priority neither map covers. The status is
		// still created (1:1), but on a to_do GUESS that must be reported by
		// name; the priority lands on NULL and is reported the same way.
		`{"type":"issue","issue":{"id":"i-9","sequence_id":9,"name":"unmappable","state_name":"Investigating","state_group":"investigating","priority":"squeek","created_at":"2024-02-12T00:00:00Z","updated_at":"2024-02-12T00:00:00Z","created_by_id":"u-oskars","updated_by_id":"u-oskars"}}`,
		// #10 — urgent: the rung above Critical on Plane's scale, which
		// Windshift does not seed. Fidelity creates the row; dropping to NULL
		// or folding into Critical would both misstate the source.
		`{"type":"issue","issue":{"id":"i-10","sequence_id":10,"name":"urgent-ticket","state_name":"Todo","state_group":"unstarted","priority":"urgent","created_at":"2024-02-13T00:00:00Z","updated_at":"2024-02-13T00:00:00Z","created_by_id":"u-oskars","updated_by_id":"u-oskars"}}`,
	}
}

// TestLoaderEndToEnd walks the fidelity contract on a real schema: the source
// state vocabulary lands 1:1 (statuses created under the category the GROUP
// implies, Cancelled given its own completed category, Triage present with no
// issues on it), urgent gets its priority row, labels are created and linked,
// new statuses are wired into the default workflow, explicit numbers keep
// their gaps, authors resolve through aliases, Plane timestamps survive, and
// reruns CONVERGE untouched rows while deferring to edited ones.
func TestLoaderEndToEnd(t *testing.T) {
	db := newTestDB(t)
	meta, records := fidelityContract()
	contractPath := writeContract(t, meta, records...)
	usersPath := writeUsers(t)

	newLoader := func(dry bool) *Loader {
		return NewLoader(db, Options{
			WorkspaceKey: "TEST", ContractPath: contractPath, UsersPath: usersPath,
			Aliases: map[string]string{"hakerx1@gmail.com": "oskars_germovs@inbox.lv"},
			DryRun:  dry, BatchSize: 3,
		})
	}

	l := newLoader(false)
	if err := l.LoadContract(); err != nil {
		t.Fatalf("load contract: %v", err)
	}
	if err := l.Resolve(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	report, err := l.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if report.IssuesImported != 9 {
		t.Errorf("imported = %d, want 9", report.IssuesImported)
	}
	if report.ParentsLinkedLate != 1 {
		t.Errorf("late-linked parents = %d, want 1 (child #7 has parent #8)", report.ParentsLinkedLate)
	}
	if report.IssuesSoftDeleted != 1 {
		t.Errorf("soft-deleted skipped = %d, want 1", report.IssuesSoftDeleted)
	}
	if report.CommentsImported != 1 {
		t.Errorf("comments imported = %d, want 1", report.CommentsImported)
	}
	if report.MaxNumber != 10 {
		t.Errorf("max number = %d, want 10", report.MaxNumber)
	}
	if !containsExact(report.UnmappedStates, "Investigating") {
		t.Errorf("unmapped states = %v, want the bare name \"Investigating\"", report.UnmappedStates)
	}
	if containsExact(report.UnmappedStates, "Blocked") {
		t.Errorf("unmapped states = %v, must NOT list Blocked: its group (started) is known and decides placement", report.UnmappedStates)
	}
	if !containsExact(report.UnmappedPriorities, "squeek") {
		t.Errorf("unmapped priorities = %v, want the bare name \"squeek\"", report.UnmappedPriorities)
	}
	if fmt.Sprint(report.CreatedStatuses) != "[Blocked Cancelled Investigating Todo Triage]" {
		t.Errorf("created statuses = %v, want the five names Windshift lacked (Done/In Progress are ADOPTED by name, not duplicated)", report.CreatedStatuses)
	}
	if fmt.Sprint(report.CreatedCategories) != "[Cancelled]" {
		t.Errorf("created categories = %v, want [Cancelled] — the one group Windshift does not seed", report.CreatedCategories)
	}
	if fmt.Sprint(report.CreatedPriorities) != "[urgent]" {
		t.Errorf("created priorities = %v, want [urgent]", report.CreatedPriorities)
	}
	if len(report.CreatedLabels) != 2 || !containsExact(report.CreatedLabels, "ready-for-agent") || !containsExact(report.CreatedLabels, "blocker") {
		t.Errorf("created labels = %v, want ready-for-agent and blocker", report.CreatedLabels)
	}
	if report.TransitionsAdded != 35 {
		t.Errorf("transitions added = %d, want 35 (one from-all entry + six exits per created status)", report.TransitionsAdded)
	}
	if report.LabelLinksAdded != 3 {
		t.Errorf("label links added = %d, want 3", report.LabelLinksAdded)
	}
	if report.IssuesConverged != 0 || report.IssuesLeftEdited != 0 {
		t.Errorf("first-run convergence counters = %d/%d, want 0/0 (nothing was pre-existing)", report.IssuesConverged, report.IssuesLeftEdited)
	}
	if len(report.NumberGaps) != 1 || report.NumberGaps[0] != 3 {
		t.Errorf("gaps = %v, want [3]", report.NumberGaps)
	}
	if len(report.UnmappedUsers) != 0 {
		t.Errorf("unmapped users = %v, want none (alias must resolve hakerx1)", report.UnmappedUsers)
	}
	if len(report.Collisions) != 0 {
		t.Errorf("collisions = %v, want none", report.Collisions)
	}
	// Vocabulary landed in the database, not just in the report.
	var statusCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM statuses`).Scan(&statusCount); err != nil {
		t.Fatalf("count statuses: %v", err)
	}
	if statusCount != 8 {
		t.Errorf("statuses = %d rows, want 8 (3 seeded + 5 created; Done/In Progress adopted, never duplicated)", statusCount)
	}
	// Adopted statuses are the seeded rows themselves.
	type itemProbe struct {
		number  int
		status  int64
		creator sql.NullInt64
		prio    sql.NullInt64
		created time.Time
		updated time.Time
		story   sql.NullFloat64
		parent  sql.NullInt64
		due     sql.NullString
	}
	rows, err := db.Query(`SELECT workspace_item_number, status_id, creator_id, priority_id, created_at, updated_at, story_points, parent_id, COALESCE(due_date,'') FROM items WHERE workspace_id = 1 ORDER BY workspace_item_number`)
	if err != nil {
		t.Fatalf("read items: %v", err)
	}
	byNumber := map[int]itemProbe{}
	for rows.Next() {
		var r itemProbe
		if err := rows.Scan(&r.number, &r.status, &r.creator, &r.prio, &r.created, &r.updated, &r.story, &r.parent, &r.due); err != nil {
			_ = rows.Close()
			t.Fatalf("scan item: %v", err)
		}
		byNumber[r.number] = r
	}
	_ = rows.Close()
	var nums []int
	for n := range byNumber {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	if fmt.Sprint(nums) != "[1 2 4 5 6 7 8 9 10]" {
		t.Fatalf("item numbers = %v, want [1 2 4 5 6 7 8 9 10]", nums)
	}

	// Next create must continue above the maximum.
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	next, err := mustNumber(t, tx)
	if err != nil {
		t.Fatalf("next number: %v", err)
	}
	_ = tx.Rollback()
	if next != 11 {
		t.Errorf("next allocated number = %d, want 11", next)
	}

	if id := seededIDs.gnarl; byNumber[1].creator.Int64 != id || !byNumber[1].creator.Valid {
		t.Errorf("#1 creator = %v, want gnarl %d", byNumber[1].creator, id)
	}
	if want, _ := time.Parse(time.RFC3339, "2024-01-02T03:04:05Z"); !byNumber[1].created.Equal(want) {
		t.Errorf("#1 created_at = %v, want %v (Plane chronology must survive)", byNumber[1].created, want)
	}
	if want, _ := time.Parse(time.RFC3339, "2024-01-05T00:00:00Z"); !byNumber[1].updated.Equal(want) {
		t.Errorf("#1 updated_at = %v, want the contract value %v", byNumber[1].updated, want)
	}
	if byNumber[1].status != seededIDs.done {
		t.Errorf("#1 status = %d, want the SEEDED Done row %d (adoption, not a second Done)", byNumber[1].status, seededIDs.done)
	}
	if byNumber[2].status != seededIDs.inProg {
		t.Errorf("#2 status = %d, want the seeded In Progress row %d", byNumber[2].status, seededIDs.inProg)
	}
	// Cancelled: its own status, under a category Plane's group names and
	// Windshift's burndown must treat as completed.
	cancelID := statusID(t, db, "Cancelled")
	if byNumber[5].status != cancelID {
		t.Errorf("#5 status = %d, want Cancelled %d — abandoned work is not Done", byNumber[5].status, cancelID)
	}
	if builtin, completed := statusCategoryBuiltin(t, db, "Cancelled"); builtin != "cancelled" || !completed {
		t.Errorf("Cancelled category = (%s, completed=%v), want (cancelled, true)", builtin, completed)
	}
	// Blocked: the GROUP (started) decides — in_progress — even though every
	// name-based map would file it under to_do. This is the measured VECTIS
	// fact the collapse used to get wrong.
	blockedID := statusID(t, db, "Blocked")
	if byNumber[6].status != blockedID {
		t.Errorf("#6 status = %d, want Blocked %d", byNumber[6].status, blockedID)
	}
	if builtin, _ := statusCategoryBuiltin(t, db, "Blocked"); builtin != "in_progress" {
		t.Errorf("Blocked category = %s, want in_progress (group started decides, not the name)", builtin)
	}
	// Todo (unstarted) and Triage (triage) both land under to_do; Triage has
	// no issues on it and must still exist — the vocabulary is the project's,
	// not just the states with traffic.
	if builtin, _ := statusCategoryBuiltin(t, db, "Todo"); builtin != "to_do" {
		t.Errorf("Todo category = %s, want to_do", builtin)
	}
	_ = statusID(t, db, "Triage")
	if builtin, _ := statusCategoryBuiltin(t, db, "Investigating"); builtin != "to_do" {
		t.Errorf("Investigating category = %s, want the reported to_do fallback", builtin)
	}
	// Urgent: created ABOVE Critical (sort 0 < 1), and #10 actually carries it.
	var urgentID int64
	var urgentSort int
	if err := db.QueryRow(`SELECT id, sort_order FROM priorities WHERE name = 'Urgent' LIMIT 1`).Scan(&urgentID, &urgentSort); err != nil {
		t.Fatalf("Urgent priority missing: %v", err)
	}
	if urgentSort != 0 {
		t.Errorf("Urgent sort_order = %d, want 0 (above the seeded Critical at 1)", urgentSort)
	}
	if byNumber[10].prio.Int64 != urgentID || !byNumber[10].prio.Valid {
		t.Errorf("#10 priority = %v, want Urgent %d (urgent must not fall to NULL or Critical)", byNumber[10].prio, urgentID)
	}
	// Labels: created with the source's colours normalised to the #-form the
	// destination columns carry, and linked to exactly the items that had them.
	type labelProbe struct {
		name, color string
	}
	var labels []labelProbe
	rows, err = db.Query(`SELECT name, color FROM labels ORDER BY name`)
	if err != nil {
		t.Fatalf("read labels: %v", err)
	}
	for rows.Next() {
		var r labelProbe
		if err := rows.Scan(&r.name, &r.color); err != nil {
			_ = rows.Close()
			t.Fatalf("scan label: %v", err)
		}
		labels = append(labels, r)
	}
	_ = rows.Close()
	if fmt.Sprint(labels) != "[{blocker #b60205} {ready-for-agent #10B981}]" {
		t.Errorf("labels = %v, want exactly blocker(#b60205) + ready-for-agent(#10B981)", labels)
	}
	var readyColor, blockerColor string
	if err := db.QueryRow(`SELECT color FROM labels WHERE name = 'ready-for-agent'`).Scan(&readyColor); err != nil {
		t.Fatalf("ready color: %v", err)
	}
	if readyColor != "#10B981" {
		t.Errorf("ready-for-agent color = %q, want #10B981", readyColor)
	}
	if err := db.QueryRow(`SELECT color FROM labels WHERE LOWER(name) = 'blocker'`).Scan(&blockerColor); err != nil {
		t.Fatalf("blocker color: %v", err)
	}
	if blockerColor != "#b60205" {
		t.Errorf("blocker color = %q, want #b60205 (bare-hex source colour normalised, not invented)", blockerColor)
	}
	type linkProbe struct {
		n    int
		name string
	}
	var links []linkProbe
	rows, err = db.Query(`
		SELECT i.workspace_item_number, l.name
		FROM items i JOIN item_labels il ON il.item_id = i.id
		JOIN labels l ON l.id = il.label_id
		WHERE i.workspace_id = 1 ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("read links: %v", err)
	}
	for rows.Next() {
		var r linkProbe
		if err := rows.Scan(&r.n, &r.name); err != nil {
			_ = rows.Close()
			t.Fatalf("scan link: %v", err)
		}
		links = append(links, r)
	}
	_ = rows.Close()
	if fmt.Sprint(links) != "[{1 ready-for-agent} {4 blocker} {6 blocker}]" {
		t.Errorf("label links = %v, want [{1 ready-for-agent} {4 blocker} {6 blocker}]", links)
	}

	// New statuses are wired into the workflow the workspace actually
	// resolves: each gets a from-all entry (so anything may move ONTO it) and
	// explicit exits (so its items may move OFF it). Without this they would
	// be islands — items land there and nothing may move them again.
	var entries int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workflow_transitions WHERE from_all_statuses = true`).Scan(&entries); err != nil {
		t.Fatalf("count from-all transitions: %v", err)
	}
	if entries != 5 {
		t.Errorf("from-all entries = %d, want 5 (one per created status)", entries)
	}
	var islands int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM statuses s
		WHERE s.name IN ('Todo','Blocked','Cancelled','Triage','Investigating')
		  AND NOT EXISTS (SELECT 1 FROM workflow_transitions wt WHERE wt.workflow_id = 1 AND wt.to_status_id = s.id AND wt.from_all_statuses = true)
		  AND NOT EXISTS (SELECT 1 FROM workflow_transitions wt WHERE wt.workflow_id = 1 AND wt.from_status_id = s.id)`).Scan(&islands); err != nil {
		t.Fatalf("check islands: %v", err)
	}
	if islands != 0 {
		t.Errorf("island statuses = %d, want 0", islands)
	}

	// Per-row fidelity that predates this rework and must survive it.
	if id := seededIDs.oskars; byNumber[2].creator.Int64 != id {
		t.Errorf("#2 creator = %v, want oskars %d (alias resolution failed)", byNumber[2].creator, id)
	}
	if v := byNumber[2].story; !v.Valid || v.Float64 != 8 {
		t.Errorf("#2 story points = %v, want 8", v)
	}
	if !byNumber[2].due.Valid || !strings.HasPrefix(byNumber[2].due.String, "2024-03-01") {
		t.Errorf("#2 due_date = %v, want 2024-03-01 (target_date must map to due_date)", byNumber[2].due)
	}
	var childParent sql.NullInt64
	var parentID int64
	if err := db.QueryRow(`SELECT parent_id FROM items WHERE workspace_id = 1 AND workspace_item_number = 4`).Scan(&childParent); err != nil {
		t.Fatalf("read child parent: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM items WHERE workspace_id = 1 AND workspace_item_number = 2`).Scan(&parentID); err != nil {
		t.Fatalf("read parent id: %v", err)
	}
	if !childParent.Valid || childParent.Int64 != parentID {
		t.Errorf("#4 parent = %v, want %d (parent link lost)", childParent, parentID)
	}
	var lateParent sql.NullInt64
	var lateParentID int64
	if err := db.QueryRow(`SELECT parent_id FROM items WHERE workspace_id = 1 AND workspace_item_number = 7`).Scan(&lateParent); err != nil {
		t.Fatalf("read late child parent: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM items WHERE workspace_id = 1 AND workspace_item_number = 8`).Scan(&lateParentID); err != nil {
		t.Fatalf("read late parent id: %v", err)
	}
	if !lateParent.Valid || lateParent.Int64 != lateParentID {
		t.Errorf("#7 parent = %v, want %d (second-pass parent link failed)", lateParent, lateParentID)
	}

	// Comment fidelity: author and timestamp from the contract.
	var cAuthor sql.NullInt64
	var cCreated time.Time
	var cContent string
	if err := db.QueryRow(`SELECT author_id, created_at, content FROM comments WHERE item_id = (SELECT id FROM items WHERE workspace_id=1 AND workspace_item_number=6)`).Scan(&cAuthor, &cCreated, &cContent); err != nil {
		t.Fatalf("read comment: %v", err)
	}
	if cAuthor.Int64 != seededIDs.oskars {
		t.Errorf("comment author = %v, want oskars %d", cAuthor, seededIDs.oskars)
	}
	if want, _ := time.Parse(time.RFC3339, "2024-02-09T10:00:00Z"); !cCreated.Equal(want) {
		t.Errorf("comment created_at = %v, want %v", cCreated, want)
	}
	if cContent != "keep me" {
		t.Errorf("comment content = %q, want %q", cContent, "keep me")
	}

	// Provenance: mapping metadata keeps the source state AND the resolved
	// label names — the row's own history, whatever later runs do to it.
	var metaJSON string
	if err := db.QueryRow(`SELECT metadata_json FROM plane_import_mappings WHERE workspace_id=1 AND entity_type='issue' AND external_id='i-5'`).Scan(&metaJSON); err != nil {
		t.Fatalf("read mapping: %v", err)
	}
	if !strings.Contains(metaJSON, "Cancelled") {
		t.Errorf("mapping metadata = %q, must retain the Plane state name", metaJSON)
	}
	if err := db.QueryRow(`SELECT metadata_json FROM plane_import_mappings WHERE workspace_id=1 AND entity_type='issue' AND external_id='i-1'`).Scan(&metaJSON); err != nil {
		t.Fatalf("read mapping i-1: %v", err)
	}
	if !strings.Contains(metaJSON, "ready-for-agent") {
		t.Errorf("mapping metadata i-1 = %q, must carry the resolved label names", metaJSON)
	}

	// Created facts exist for each imported item with the import provenance.
	var factCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE workspace_id = 1`).Scan(&factCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if factCount != 9 {
		t.Errorf("event facts = %d, want 9 (one created fact per imported item)", factCount)
	}
	// Rerun on unchanged data: everything skipped, everything already correct,
	// so convergence has nothing to do — and it invents nothing.
	l2 := newLoader(false)
	if err := l2.LoadContract(); err != nil {
		t.Fatalf("rerun load: %v", err)
	}
	if err := l2.Resolve(); err != nil {
		t.Fatalf("rerun resolve: %v", err)
	}
	report2, err := l2.Run(context.Background())
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if report2.IssuesImported != 0 || report2.IssuesExisting != 9 || report2.CommentsImported != 0 || report2.CommentsExisting != 1 {
		t.Errorf("rerun report = %+v, want all-skipped", report2)
	}
	if report2.IssuesConverged != 0 || report2.IssuesLeftEdited != 0 || report2.LabelLinksAdded != 0 || report2.TransitionsAdded != 0 {
		t.Errorf("rerun repair counters = %+v, want 0 converged / 0 deferred / 0 links / 0 transitions on unchanged data", report2)
	}
	if len(report2.CreatedStatuses)+len(report2.CreatedCategories)+len(report2.CreatedPriorities)+len(report2.CreatedLabels) != 0 {
		t.Errorf("rerun created vocabulary = %v/%v/%v/%v, want nothing recreated", report2.CreatedStatuses, report2.CreatedCategories, report2.CreatedPriorities, report2.CreatedLabels)
	}

	// Convergence, proved two ways. (A) Simulate the v1 collapse AFTER the
	// fact: knock #1 off Done onto the Open row without moving updated_at —
	// exactly the state an old import leaves behind. (B) Edit #2 through a
	// write that DOES move updated_at — the operator's later decision. The
	// rerun must repair A and defer to B.
	if _, err := db.ExecWrite(`UPDATE items SET status_id = ? WHERE workspace_id = 1 AND workspace_item_number = 1`, seededIDs.open); err != nil {
		t.Fatalf("collapse #1: %v", err)
	}
	postEdit := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := db.ExecWrite(`UPDATE items SET status_id = ?, updated_at = ? WHERE workspace_id = 1 AND workspace_item_number = 2`, seededIDs.open, postEdit); err != nil {
		t.Fatalf("post-import edit #2: %v", err)
	}
	l2b := newLoader(false)
	if err := l2b.LoadContract(); err != nil {
		t.Fatalf("converge load: %v", err)
	}
	if err := l2b.Resolve(); err != nil {
		t.Fatalf("converge resolve: %v", err)
	}
	report2b, err := l2b.Run(context.Background())
	if err != nil {
		t.Fatalf("converge run: %v", err)
	}
	if report2b.IssuesConverged != 1 {
		t.Errorf("converged = %d, want 1 (untouched #1 repaired onto Done)", report2b.IssuesConverged)
	}
	if report2b.IssuesLeftEdited != 1 {
		t.Errorf("left alone (edited) = %d, want 1 (post-import edit on #2 outranks the import)", report2b.IssuesLeftEdited)
	}
	var afterStatus int64
	var afterUpdated time.Time
	if err := db.QueryRow(`SELECT status_id, updated_at FROM items WHERE workspace_id = 1 AND workspace_item_number = 1`).Scan(&afterStatus, &afterUpdated); err != nil {
		t.Fatalf("read #1 after converge: %v", err)
	}
	if afterStatus != seededIDs.done {
		t.Errorf("#1 status after converge = %d, want Done %d", afterStatus, seededIDs.done)
	}
	if want, _ := time.Parse(time.RFC3339, "2024-01-05T00:00:00Z"); !afterUpdated.Equal(want) {
		t.Errorf("#1 updated_at after converge = %v, want the untouched contract value %v (convergence must not stamp time)", afterUpdated, want)
	}
	if err := db.QueryRow(`SELECT status_id FROM items WHERE workspace_id = 1 AND workspace_item_number = 2`).Scan(&afterStatus); err != nil {
		t.Fatalf("read #2 after converge: %v", err)
	}
	if afterStatus != seededIDs.open {
		t.Errorf("#2 status after converge = %d, want the operator's Open %d left alone", afterStatus, seededIDs.open)
	}

	// Partial replay: drop #4's row and mapping (a batch lost mid-write), then
	// rerun: only #4 comes back, at number 4, with its label re-linked.
	if _, err := db.ExecWrite(`DELETE FROM plane_import_mappings WHERE workspace_id = 1 AND entity_type='issue' AND external_id='i-4'`); err != nil {
		t.Fatalf("drop mapping: %v", err)
	}
	if _, err := db.ExecWrite(`DELETE FROM items WHERE workspace_id = 1 AND workspace_item_number = 4`); err != nil {
		t.Fatalf("clear number: %v", err)
	}
	l3 := newLoader(false)
	if err := l3.LoadContract(); err != nil {
		t.Fatalf("replay load: %v", err)
	}
	if err := l3.Resolve(); err != nil {
		t.Fatalf("replay resolve: %v", err)
	}
	report3, err := l3.Run(context.Background())
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if report3.IssuesImported != 1 || report3.IssuesExisting != 8 {
		t.Errorf("replay report = %+v, want 1 imported 8 existing", report3)
	}
	if report3.LabelLinksAdded != 1 {
		t.Errorf("replay label links = %d, want 1 (the re-imported #4 carries blocker)", report3.LabelLinksAdded)
	}
	if report3.IssuesLeftEdited != 1 {
		t.Errorf("replay deferred = %d, want 1 (#2 is still operator-edited)", report3.IssuesLeftEdited)
	}
	var replayNumber int
	if err := db.QueryRow(`SELECT i.workspace_item_number FROM plane_import_mappings m JOIN items i ON i.id = m.windshift_id WHERE m.workspace_id=1 AND m.external_id='i-4'`).Scan(&replayNumber); err != nil {
		t.Fatalf("read replay number: %v", err)
	}
	if replayNumber != 4 {
		t.Errorf("replayed #4 landed on number %d, want 4", replayNumber)
	}

	// Collision: a number claimed by a row the importer does not own aborts.
	if _, err := db.ExecWrite(`DELETE FROM plane_import_mappings WHERE workspace_id = 1 AND entity_type='issue' AND external_id='i-4'`); err != nil {
		t.Fatalf("drop mapping for collision test: %v", err)
	}
	l4 := newLoader(false)
	if err := l4.LoadContract(); err != nil {
		t.Fatalf("collision load: %v", err)
	}
	if err := l4.Resolve(); err != nil {
		t.Fatalf("collision resolve: %v", err)
	}
	if _, err := l4.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Errorf("collision run error = %v, want a collision refusal", err)
	}
}

// TestDryRunVocabularyRollsBack proves the dry-run vocabulary pass does what
// it claims: it WRITES against the live schema (so schema errors surface on a
// dry run, not only on the real one), then rolls everything back. After it,
// the database is byte-for-byte what it was before: still three statuses, no
// cancelled category, no Urgent, no labels, no transitions added — while the
// report names everything the real run would create.
func TestDryRunVocabularyRollsBack(t *testing.T) {
	db := newTestDB(t)
	meta, records := fidelityContract()
	contractPath := writeContract(t, meta, records...)

	l := NewLoader(db, Options{
		WorkspaceKey: "TEST", ContractPath: contractPath, UsersPath: writeUsers(t),
		Aliases: map[string]string{"hakerx1@gmail.com": "oskars_germovs@inbox.lv"},
		DryRun:  true, BatchSize: 3,
	})
	if err := l.LoadContract(); err != nil {
		t.Fatalf("load contract: %v", err)
	}
	if err := l.Resolve(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	report, err := l.Run(context.Background())
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !report.DryRun {
		t.Errorf("report.DryRun = %v, want true", report.DryRun)
	}
	if fmt.Sprint(report.CreatedStatuses) != "[Blocked Cancelled Investigating Todo Triage]" {
		t.Errorf("would-create statuses = %v, want the five the live pass creates", report.CreatedStatuses)
	}
	if fmt.Sprint(report.CreatedCategories) != "[Cancelled]" || fmt.Sprint(report.CreatedPriorities) != "[urgent]" {
		t.Errorf("would-create vocab = %v / %v, want [Cancelled] and [urgent]", report.CreatedCategories, report.CreatedPriorities)
	}
	if report.TransitionsAdded != 35 {
		t.Errorf("would-add transitions = %d, want 35", report.TransitionsAdded)
	}
	var statuses, categories, priorities, labels, transitions, items int
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM statuses), (SELECT COUNT(*) FROM status_categories),
		(SELECT COUNT(*) FROM priorities), (SELECT COUNT(*) FROM labels),
		(SELECT COUNT(*) FROM workflow_transitions), (SELECT COUNT(*) FROM items)`).
		Scan(&statuses, &categories, &priorities, &labels, &transitions, &items); err != nil {
		t.Fatalf("read catalog counts: %v", err)
	}
	if statuses != 3 || categories != 3 || priorities != 4 || labels != 0 || transitions != 4 || items != 0 {
		t.Errorf("after dry run catalog = statuses:%d categories:%d priorities:%d labels:%d transitions:%d items:%d — want the untouched seed 3/3/4/0/4/0 (a dry run that persists is not a dry run)",
			statuses, categories, priorities, labels, transitions, items)
	}
}

func mustNumber(t *testing.T, tx database.Tx) (int, error) {
	t.Helper()
	// Read the repository's allocator through raw SQL — MAX(items) vs
	// MAX(reservations)+1 semantics, the same rule the live allocator runs.
	var maxItem sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(workspace_item_number) FROM items WHERE workspace_id = 1`).Scan(&maxItem); err != nil {
		return 0, err
	}
	n := int64(0)
	if maxItem.Valid {
		n = maxItem.Int64
	}
	return int(n + 1), nil
}
