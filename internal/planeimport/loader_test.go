package planeimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
)

// newTestDB builds a fully initialized Windshift schema on a throwaway SQLite
// file and seeds the minimum a Plane import needs: one workspace, two users,
// the three default statuses, and the five default priorities. Seeding reads
// each table's live column list (pragma table_info) so an upstream column
// rename fails loudly with a named column instead of silently skewing tests.
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
	// Statuses and priorities come from Initialize's own default data —
	// re-seeding them by hand would test a fixture, not the product.
	look := func(table, name string) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRow(fmt.Sprintf(`SELECT id FROM %s WHERE name = ? LIMIT 1`, table), name).Scan(&id); err != nil {
			t.Fatalf("default %s %q missing after Initialize: %v", table, name, err)
		}
		return id
	}
	seededIDs = struct {
		oskars, gnarl, toDo, inProg, done int64
	}{oskars, gnarl, look("statuses", "Open"), look("statuses", "In Progress"), look("statuses", "Done")}
	_ = seededIDs.inProg
	return db
}

var seededIDs struct {
	oskars, gnarl, toDo, inProg, done int64
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

// TestLoaderEndToEnd walks the fidelity contract on a real schema: explicit
// numbers with gaps, authors via the alias table, Plane timestamps, story
// points, parent links, comment authorship, idempotent rerun, partial-replay
// rerun, and a number collision refused.
// containsExact reports whether name appears as its own entry. The report is
// operator-facing: a line that buries a priority inside a state list, or
// prefixes it, is a line an operator has to parse by hand.
func containsExact(list []string, name string) bool {
	for _, v := range list {
		if v == name {
			return true
		}
	}
	return false
}

func TestLoaderEndToEnd(t *testing.T) {
	db := newTestDB(t)
	contractPath := writeContract(t,
		`{"type":"meta","meta":{"schema_version":1,"source":"plane","workspace_slug":"solum-designum","project_id":"p-1","project_key":"TEST","dumped_at":"2026-10-26T00:00:00Z"}}`,
		// #1 — Done by Gnarl, 2024.
		`{"type":"issue","issue":{"id":"i-1","sequence_id":1,"name":"first","state_name":"Done","priority":"none","created_at":"2024-01-02T03:04:05Z","updated_at":"2024-01-05T00:00:00Z","created_by_id":"u-gnarl","updated_by_id":"u-gnarl","description_json":"{\"type\":\"doc\",\"content\":[{\"type\":\"paragraph\",\"content\":[{\"type\":\"text\",\"text\":\"body one\"}]}]}"}}`,
		// #2 — In Progress by Oskars (Plane email needs the alias), story points.
		`{"type":"issue","issue":{"id":"i-2","sequence_id":2,"name":"second","state_name":"In Progress","priority":"medium","estimate_point":"8","created_at":"2024-02-02T00:00:00Z","updated_at":"2024-02-03T00:00:00Z","created_by_id":"u-oskars","updated_by_id":"u-oskars","target_date":"2024-03-01T00:00:00Z"}}`,
		// #3 — soft-deleted: number must stay a gap.
		`{"type":"issue","issue":{"id":"i-3","sequence_id":3,"name":"gone","state_name":"Todo","priority":"none","created_at":"2024-02-04T00:00:00Z","updated_at":"2024-02-04T00:00:00Z","deleted_at":"2024-02-05T00:00:00Z","created_by_id":"u-oskars","updated_by_id":"u-oskars"}}`,
		// #4 — child of i-2, Todo.
		`{"type":"issue","issue":{"id":"i-4","sequence_id":4,"name":"child","state_name":"Todo","priority":"low","parent_id":"i-2","parent_sequence_id":2,"created_at":"2024-02-06T00:00:00Z","updated_at":"2024-02-06T00:00:00Z","created_by_id":"u-oskars","updated_by_id":"u-oskars"}}`,
		// #5 — Cancelled maps to Done; the source state stays in mapping metadata.
		`{"type":"issue","issue":{"id":"i-5","sequence_id":5,"name":"cancelled","state_name":"Cancelled","priority":"none","created_at":"2024-02-07T00:00:00Z","updated_at":"2024-02-07T00:00:00Z","created_by_id":"u-gnarl","updated_by_id":"u-gnarl"}}`,
		// #6 — with a comment.
		`{"type":"issue","issue":{"id":"i-6","sequence_id":6,"name":"commented","state_name":"Blocked","priority":"none","created_at":"2024-02-08T00:00:00Z","updated_at":"2024-02-08T00:00:00Z","created_by_id":"u-oskars","updated_by_id":"u-oskars"}}`,
		// comment on #6 by Oskars.
		`{"type":"comment","comment":{"id":"c-1","issue_id":"i-6","comment_json":"","comment_html":"","text":"keep me","created_at":"2024-02-09T10:00:00Z","updated_at":"2024-02-09T10:00:00Z","created_by_id":"u-oskars","access":"INTERNAL"}}`,
		// #7 — child of #6 but the PARENT has a higher sequence id: the
		// measured Plane shape where in-pass linking must defer.
		`{"type":"issue","issue":{"id":"i-7","sequence_id":7,"name":"late-parent-child","state_name":"Todo","priority":"none","parent_id":"i-8","parent_sequence_id":8,"created_at":"2024-02-10T00:00:00Z","updated_at":"2024-02-10T00:00:00Z","created_by_id":"u-oskars","updated_by_id":"u-oskars"}}`,
		`{"type":"issue","issue":{"id":"i-8","sequence_id":8,"name":"late-parent","state_name":"Todo","priority":"none","created_at":"2024-02-11T00:00:00Z","updated_at":"2024-02-11T00:00:00Z","created_by_id":"u-oskars","updated_by_id":"u-oskars"}}`,
		// #9 — a state and a priority the maps do not cover. Both must be
		// reported in their own field, by their own name: the report is the
		// only place an operator learns a value was dropped.
		`{"type":"issue","issue":{"id":"i-9","sequence_id":9,"name":"unmappable","state_name":"Investigating","priority":"squeek","created_at":"2024-02-12T00:00:00Z","updated_at":"2024-02-12T00:00:00Z","created_by_id":"u-oskars","updated_by_id":"u-oskars"}}`,
	)
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

	if report.IssuesImported != 8 {
		t.Errorf("imported = %d, want 8", report.IssuesImported)
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
	if report.MaxNumber != 9 {
		t.Errorf("max number = %d, want 9", report.MaxNumber)
	}
	if !containsExact(report.UnmappedStates, "Investigating") {
		t.Errorf("unmapped states = %v, want the bare name \"Investigating\"", report.UnmappedStates)
	}
	if !containsExact(report.UnmappedPriorities, "squeek") {
		t.Errorf("unmapped priorities = %v, want the bare name \"squeek\"", report.UnmappedPriorities)
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

	// Number parity and gap: 1,2,4,5,6 exist; 3 does not.
	var nums []int
	rows, err := db.Query(`SELECT workspace_item_number FROM items WHERE workspace_id = 1 ORDER BY 1`)
	if err != nil {
		t.Fatalf("read numbers: %v", err)
	}
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan number: %v", err)
		}
		nums = append(nums, n)
	}
	_ = rows.Close()
	if fmt.Sprint(nums) != "[1 2 4 5 6 7 8 9]" {
		t.Fatalf("item numbers = %v, want [1 2 4 5 6 7 8 9]", nums)
	}

	// Next create must land on 9 — the counter continues above the maximum.
	// Allocation is the repository's own MAX+1 path.
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	next, err := mustNumber(t, tx)
	if err != nil {
		t.Fatalf("next number: %v", err)
	}
	_ = tx.Rollback()
	if next != 10 {
		t.Errorf("next allocated number = %d, want 10", next)
	}

	// Per-row fidelity: creator, status, dates, points, parent.
	type itemRow struct {
		number, status int
		creator        sql.NullInt64
		createdAt      time.Time
		story          sql.NullFloat64
		parent         sql.NullInt64
		due            sql.NullString
	}
	var rowsX []itemRow
	rows, err = db.Query(`SELECT workspace_item_number, status_id, creator_id, created_at, story_points, parent_id, COALESCE(due_date,'') FROM items WHERE workspace_id = 1 ORDER BY workspace_item_number`)
	if err != nil {
		t.Fatalf("read items: %v", err)
	}
	for rows.Next() {
		var r itemRow
		if err := rows.Scan(&r.number, &r.status, &r.creator, &r.createdAt, &r.story, &r.parent, &r.due); err != nil {
			t.Fatalf("scan item: %v", err)
		}
		rowsX = append(rowsX, r)
	}
	_ = rows.Close()
	byNumber := map[int]itemRow{}
	for _, r := range rowsX {
		byNumber[r.number] = r
	}
	if id := seededIDs.gnarl; byNumber[1].creator.Int64 != id || byNumber[1].creator.Valid != true {
		t.Errorf("#1 creator = %v, want gnarl %d", byNumber[1].creator, id)
	}
	if want, _ := time.Parse(time.RFC3339, "2024-01-02T03:04:05Z"); !byNumber[1].createdAt.Equal(want) {
		t.Errorf("#1 created_at = %v, want %v (Plane chronology must survive)", byNumber[1].createdAt, want)
	}
	if byNumber[1].status != int(seededIDs.done) {
		t.Errorf("#1 status = %d, want done %d", byNumber[1].status, seededIDs.done)
	}
	// #9 is the unmapped-state row: it imports on whatever row carries
	// is_default (the belt for a status that vanished mid-run), and both
	// unmapped values are reported by name rather than swallowed.
	var defaultStatus int
	if err := db.QueryRow(`SELECT id FROM statuses WHERE is_default = true ORDER BY id LIMIT 1`).Scan(&defaultStatus); err != nil {
		t.Fatalf("read default status: %v", err)
	}
	if byNumber[9].status != defaultStatus {
		t.Errorf("#9 status = %d, want the workspace default %d", byNumber[9].status, defaultStatus)
	}
	if id := seededIDs.oskars; byNumber[2].creator.Int64 != id {
		t.Errorf("#2 creator = %v, want oskars %d (alias resolution failed)", byNumber[2].creator, id)
	}
	if v := byNumber[2].story; !v.Valid || v.Float64 != 8 {
		t.Errorf("#2 story points = %v, want 8", v)
	}
	if !byNumber[2].due.Valid || !strings.HasPrefix(byNumber[2].due.String, "2024-03-01") {
		t.Errorf("#2 due_date = %v, want 2024-03-01 (target_date must map to due_date)", byNumber[2].due)
	}
	if byNumber[2].parent.Valid {
		t.Errorf("#2 parent = %v, want none", byNumber[2].parent)
	}
	// The real parent check: #4's parent id equals #2's row id.
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
	// Late parent: #7 names parent #8 (higher number) — linked in the second pass.
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
	if byNumber[5].status != int(seededIDs.done) {
		t.Errorf("#5 (Cancelled) status = %d, want done %d", byNumber[5].status, seededIDs.done)
	}
	if byNumber[6].status != int(seededIDs.toDo) {
		t.Errorf("#6 (Blocked) status = %d, want to-do %d", byNumber[6].status, seededIDs.toDo)
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

	// Provenance: the mapping row keeps the Plane state name.
	var metaJSON string
	if err := db.QueryRow(`SELECT metadata_json FROM plane_import_mappings WHERE workspace_id=1 AND entity_type='issue' AND external_id='i-5'`).Scan(&metaJSON); err != nil {
		t.Fatalf("read mapping: %v", err)
	}
	if !strings.Contains(metaJSON, "Cancelled") {
		t.Errorf("mapping metadata = %q, must retain the Plane state name", metaJSON)
	}

	// Created facts exist for each imported item with the import provenance.
	var factCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE workspace_id = 1`).Scan(&factCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if factCount != 8 {
		t.Errorf("event facts = %d, want 8 (one created fact per imported item)", factCount)
	}

	// Rerun: everything skipped, nothing duplicated.
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
	if report2.IssuesImported != 0 || report2.IssuesExisting != 8 || report2.CommentsImported != 0 || report2.CommentsExisting != 1 {
		t.Errorf("rerun report = %+v, want all-skipped", report2)
	}

	// Partial replay: delete #3-free item #4 (simulating a row lost between
	// batches) and its mapping, move its number to a scratch row, then rerun:
	// only #4 comes back, at number 4.
	if _, err := db.ExecWrite(`UPDATE items SET workspace_item_number = 100 WHERE workspace_id = 1 AND workspace_item_number = 4`); err != nil {
		t.Fatalf("scratch-move: %v", err)
	}
	if _, err := db.ExecWrite(`DELETE FROM plane_import_mappings WHERE workspace_id = 1 AND entity_type='issue' AND external_id='i-4'`); err != nil {
		t.Fatalf("drop mapping: %v", err)
	}
	if _, err := db.ExecWrite(`UPDATE items SET workspace_item_number = 4, id = id WHERE workspace_id = 1 AND workspace_item_number = 100`); err != nil {
		t.Fatalf("restore scratch: %v", err)
	}
	// The replay inserts a NEW row for i-4 (number 4 is now free again after
	// the restore moved the old row back — make the number genuinely free):
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
	if report3.IssuesImported != 1 || report3.IssuesExisting != 7 {
		t.Errorf("replay report = %+v, want 1 imported 6 existing", report3)
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
