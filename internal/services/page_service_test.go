package services

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"windshift/internal/database"
)

// newPagesTestDB spins up an in-memory SQLite with the minimum schema the
// PageService and PageRepository touch. Avoids database.Initialize() so an
// unrelated module's startup migration cannot break these tests.
func newPagesTestDB(t *testing.T) database.Database {
	t.Helper()
	dsn := fmt.Sprintf("file:pages-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, stmt := range []string{
		`CREATE TABLE workspaces (id INTEGER PRIMARY KEY, name TEXT, key TEXT)`,
		// is_active mirrors the production users column so GrantPermission's
		// principal validation (slice 21) can run against this minimal
		// schema. The production users table has many more columns, but
		// Page revision listings join author display names as well.
		`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, first_name TEXT NOT NULL, last_name TEXT NOT NULL, is_active BOOLEAN DEFAULT true)`,
		`CREATE TABLE groups (id INTEGER PRIMARY KEY, name TEXT, is_active BOOLEAN DEFAULT true)`,
		`CREATE TABLE workspace_roles (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE pages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workspace_id INTEGER NOT NULL,
			parent_id INTEGER,
			title TEXT NOT NULL,
			slug TEXT NOT NULL,
			metadata TEXT NOT NULL DEFAULT '{}',
			content TEXT NOT NULL DEFAULT '',
			content_hash TEXT NOT NULL DEFAULT '',
			excerpt TEXT NOT NULL DEFAULT '',
			created_by INTEGER NOT NULL,
			updated_by INTEGER,
			archived_by INTEGER,
			is_home BOOLEAN NOT NULL DEFAULT FALSE,
			inherit_permissions BOOLEAN NOT NULL DEFAULT TRUE,
			rank TEXT,
			frac_index TEXT,
			path TEXT NOT NULL DEFAULT '/',
			depth INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			archived_at DATETIME
			-- No UNIQUE on slug: it is display-only and nothing resolves a
			-- page by it. Mirrors schema/pages.sql.
		)`,
		`CREATE TABLE page_revisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			page_id INTEGER NOT NULL,
			revision_number INTEGER NOT NULL,
			title TEXT NOT NULL,
			slug TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			content_hash TEXT NOT NULL DEFAULT '',
			excerpt TEXT NOT NULL DEFAULT '',
			parent_id INTEGER,
			path TEXT NOT NULL DEFAULT '/',
			depth INTEGER NOT NULL DEFAULT 0,
			change_summary TEXT NOT NULL DEFAULT '',
			change_type TEXT NOT NULL DEFAULT 'edit',
			created_by INTEGER NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(page_id, revision_number)
		)`,
		`CREATE TABLE page_permissions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			page_id INTEGER NOT NULL,
			principal_type TEXT NOT NULL,
			principal_id INTEGER NOT NULL,
			permission_level TEXT NOT NULL,
			granted_by INTEGER,
			granted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(page_id, principal_type, principal_id, permission_level)
		)`,
		`CREATE TABLE page_chunks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			page_id INTEGER NOT NULL,
			workspace_id INTEGER NOT NULL,
			revision_number INTEGER NOT NULL,
			position INTEGER NOT NULL,
			heading_path TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL,
			token_count INTEGER NOT NULL DEFAULT 0,
			byte_start INTEGER NOT NULL DEFAULT 0,
			byte_end INTEGER NOT NULL DEFAULT 0,
			content_hash TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(page_id, revision_number, position)
		)`,
		// Per-sibling-set unique index for frac_index. Mirrors the
		// production schema so tests exercise the same uniqueness
		// surface — historically this index was missing here, which is
		// why the global-uniqueness bug went unnoticed at the unit
		// level for a while.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_pages_frac_index_scoped
			ON pages(workspace_id, COALESCE(parent_id, -1), frac_index)
			WHERE frac_index IS NOT NULL`,
		`INSERT INTO workspaces (id, name, key) VALUES (1, 'ws', 'WS'), (2, 'ws2', 'WS2')`,
		`INSERT INTO users (id, username, first_name, last_name) VALUES (1, 'alice', 'Alice', 'Author'), (2, 'bob', 'Bob', 'Editor')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	return db
}

func TestPageService_Create_RootSetsPathAndDepth(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	got, err := s.Create(1, CreatePageInput{
		WorkspaceID: 1,
		Title:       "Welcome",
		Content:     "# Hello",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.Path != "/" {
		t.Errorf("root path: want /, got %q", got.Path)
	}
	if got.Depth != 0 {
		t.Errorf("root depth: want 0, got %d", got.Depth)
	}
	if got.Slug != "welcome" {
		t.Errorf("slug: want welcome, got %q", got.Slug)
	}
	if got.ContentHash == "" {
		t.Error("content hash should be set when content is non-empty")
	}
	if !got.InheritPermissions {
		t.Error("inherit_permissions should default to true")
	}
}

func TestPageService_Create_ChildInheritsParentPath(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	root, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Root"})
	if err != nil {
		t.Fatalf("root: %v", err)
	}

	child, err := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &root.ID, Title: "Child"})
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	wantPath := fmt.Sprintf("/%d/", root.ID)
	if child.Path != wantPath {
		t.Errorf("child path: want %q, got %q", wantPath, child.Path)
	}
	if child.Depth != 1 {
		t.Errorf("child depth: want 1, got %d", child.Depth)
	}
}

func TestPageService_Create_RejectsEmptyTitle(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	if _, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "   "}); !errors.Is(err, ErrPageTitleRequired) {
		t.Errorf("want ErrPageTitleRequired, got %v", err)
	}
}

func TestPageService_Create_RejectsCrossWorkspaceParent(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	other, err := s.Create(1, CreatePageInput{WorkspaceID: 2, Title: "Other"})
	if err != nil {
		t.Fatalf("seed other workspace page: %v", err)
	}
	if _, err := s.Create(1, CreatePageInput{
		WorkspaceID: 1,
		ParentID:    &other.ID,
		Title:       "Bad child",
	}); !errors.Is(err, ErrPageParentMismatch) {
		t.Errorf("want ErrPageParentMismatch, got %v", err)
	}
}

// Slugs are display-only and carry no uniqueness rule, so two siblings
// sharing a title share a slug. The old behaviour disambiguated the second
// to "notes-2" via a per-write query loop; that loop existed only to satisfy
// constraints nothing read.
func TestPageService_Create_SiblingsMayShareASlug(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	first, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Notes"})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Notes"})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.Slug != "notes" || second.Slug != "notes" {
		t.Errorf("sibling slugs: got (%q, %q), want (notes, notes)", first.Slug, second.Slug)
	}
	if first.ID == second.ID {
		t.Error("distinct pages collapsed to one row")
	}
}

// A title with no alphanumerics still has to produce a non-empty slug; the
// fallback used to live in the removed pickAvailableSlug.
func TestPageService_Create_UnsluggableTitleFallsBackToPage(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	page, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "★★★"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if page.Slug != "page" {
		t.Errorf("unsluggable title: want %q, got %q", "page", page.Slug)
	}
}

func TestPageService_Update_TitleChangeRetargetsSlug(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	page, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Old"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	updated, err := s.Update(2, UpdatePageInput{
		ID:      page.ID,
		Title:   "Brand New",
		Content: "body",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Slug != "brand-new" {
		t.Errorf("slug: want brand-new, got %q", updated.Slug)
	}
	if updated.UpdatedBy == nil || *updated.UpdatedBy != 2 {
		t.Errorf("updated_by: want 2, got %v", updated.UpdatedBy)
	}
}

// Bug-hunt-2 #1: regular Update must not be a vector for flipping
// inherit_permissions. UpdatePageInput no longer carries the field, so
// the service always preserves the existing flag — confirm with a
// page that has inherit=false from the start.
func TestPageService_Update_PreservesInheritPermissions(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	page, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Locked"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Break inheritance through the admin path so we can assert Update
	// preserves the broken state.
	if _, err := s.SetInheritPermissions(1, page.ID, false); err != nil {
		t.Fatalf("set inherit=false: %v", err)
	}

	updated, err := s.Update(1, UpdatePageInput{
		ID:      page.ID,
		Title:   "Locked",
		Content: "fresh body",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.InheritPermissions {
		t.Error("Update must not flip inherit_permissions back to true")
	}
}

// Bug-hunt #2: regular Update used to forward Rank/FracIndex into UpdateTx,
// which wrote SQL NULL for normal title/content saves. Drag-and-drop
// ordering set by Move was silently destroyed on the next edit. Update
// must now leave both columns alone.
func TestPageService_Update_PreservesRankAndFracIndex(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	parent, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	first, err := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &parent.ID, Title: "First"})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &parent.ID, Title: "Second"})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	// Reorder so second precedes first; this populates frac_index for
	// both via the backfill path in resolveSiblingFracIndex.
	if _, err := s.Move(1, second.ID, &parent.ID, nil, &first.ID); err != nil {
		t.Fatalf("reorder: %v", err)
	}

	moved, err := s.GetByID(second.ID)
	if err != nil {
		t.Fatalf("get after move: %v", err)
	}
	if moved.FracIndex == nil || *moved.FracIndex == "" {
		t.Fatalf("expected frac_index populated after reorder, got %v", moved.FracIndex)
	}
	originalFrac := *moved.FracIndex

	if _, err := s.Update(1, UpdatePageInput{ID: second.ID, Title: "Second renamed", Content: "edit"}); err != nil {
		t.Fatalf("update: %v", err)
	}

	after, err := s.GetByID(second.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if after.FracIndex == nil {
		t.Fatalf("frac_index cleared by Update — would silently destroy drag-and-drop ordering")
	}
	if *after.FracIndex != originalFrac {
		t.Errorf("frac_index changed across Update: was %q, now %q", originalFrac, *after.FracIndex)
	}
}

// Bug-hunt #3: the original idx_pages_frac_index was UNIQUE globally,
// but KeyBetween("","") returns the same first key for every sibling
// set — so the first reorder under each independent parent would
// collide on that global UNIQUE. After the migration to a per-
// (workspace_id, parent_id) unique index, two independent sibling
// sets can hold the same frac_index value without conflict.
func TestPageService_Move_TwoSiblingSetsCanShareFracIndex(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	parentA, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "A"})
	if err != nil {
		t.Fatalf("parent A: %v", err)
	}
	parentB, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "B"})
	if err != nil {
		t.Fatalf("parent B: %v", err)
	}

	// Two children per parent; reorder each pair so KeyBetween fires
	// in each sibling set independently and produces a frac_index for
	// the leading child.
	a1, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &parentA.ID, Title: "a1"})
	a2, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &parentA.ID, Title: "a2"})
	b1, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &parentB.ID, Title: "b1"})
	b2, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &parentB.ID, Title: "b2"})

	if _, err := s.Move(1, a2.ID, &parentA.ID, nil, &a1.ID); err != nil {
		t.Fatalf("reorder A: %v", err)
	}
	if _, err := s.Move(1, b2.ID, &parentB.ID, nil, &b1.ID); err != nil {
		t.Fatalf("reorder B: %v", err)
	}

	a2Reloaded, _ := s.GetByID(a2.ID)
	b2Reloaded, _ := s.GetByID(b2.ID)
	if a2Reloaded.FracIndex == nil || b2Reloaded.FracIndex == nil {
		t.Fatalf("expected both leading children to have frac_index, got a2=%v b2=%v",
			a2Reloaded.FracIndex, b2Reloaded.FracIndex)
	}
	// Pre-fix the global UNIQUE(frac_index) would have rejected the
	// second reorder. After the fix both leading children get an
	// equivalent (or identical) starting key without colliding.
	if *a2Reloaded.FracIndex != *b2Reloaded.FracIndex {
		t.Logf("note: independent sibling sets produced distinct keys (a2=%q b2=%q); test still validates the absence of a collision",
			*a2Reloaded.FracIndex, *b2Reloaded.FracIndex)
	}
}

// Bug-hunt #4: a parent-changing Move without prev/next sibling anchors
// used to leave the moved page's old frac_index intact. That meant the
// page carried a key generated for one sibling set into a different one,
// landing visually in an unpredictable position (and risking collision
// on the per-sibling-set uniqueness invariant). It must now be
// appended to the end of the new parent's children.
func TestPageService_Move_CrossParentNoAnchors_AppendsAtEnd(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	srcParent, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Src"})
	if err != nil {
		t.Fatalf("src parent: %v", err)
	}
	dstParent, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Dst"})
	if err != nil {
		t.Fatalf("dst parent: %v", err)
	}

	srcA, err := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &srcParent.ID, Title: "srcA"})
	if err != nil {
		t.Fatalf("srcA: %v", err)
	}
	srcB, err := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &srcParent.ID, Title: "srcB"})
	if err != nil {
		t.Fatalf("srcB: %v", err)
	}
	// Give srcB a frac_index by reordering it ahead of srcA — this is
	// the "old sibling set" key that the bug would carry over.
	if _, err := s.Move(1, srcB.ID, &srcParent.ID, nil, &srcA.ID); err != nil {
		t.Fatalf("reorder src children: %v", err)
	}

	dstA, err := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &dstParent.ID, Title: "dstA"})
	if err != nil {
		t.Fatalf("dstA: %v", err)
	}
	dstB, err := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &dstParent.ID, Title: "dstB"})
	if err != nil {
		t.Fatalf("dstB: %v", err)
	}
	// Populate dstA / dstB keys so the new parent has well-defined endpoints.
	if _, err := s.Move(1, dstB.ID, &dstParent.ID, &dstA.ID, nil); err != nil {
		t.Fatalf("seed dst order: %v", err)
	}

	// Move srcB into Dst without any sibling anchors.
	moved, err := s.Move(1, srcB.ID, &dstParent.ID, nil, nil)
	if err != nil {
		t.Fatalf("cross-parent move: %v", err)
	}
	if moved.ParentID == nil || *moved.ParentID != dstParent.ID {
		t.Fatalf("parent not updated: %+v", moved.ParentID)
	}
	if moved.FracIndex == nil {
		t.Fatal("frac_index nil after cross-parent move — should be appended at end")
	}

	dstAFresh, err := s.GetByID(dstA.ID)
	if err != nil {
		t.Fatalf("reload dstA: %v", err)
	}
	dstBFresh, err := s.GetByID(dstB.ID)
	if err != nil {
		t.Fatalf("reload dstB: %v", err)
	}
	if dstAFresh.FracIndex == nil || dstBFresh.FracIndex == nil {
		t.Fatalf("expected both pre-existing siblings to have keys (dstA=%v dstB=%v)", dstAFresh.FracIndex, dstBFresh.FracIndex)
	}
	// dstB was reordered after dstA, so display order is dstA, dstB.
	// The appended page must sort after both.
	if *moved.FracIndex <= *dstAFresh.FracIndex {
		t.Errorf("appended key %q must sort after dstA %q", *moved.FracIndex, *dstAFresh.FracIndex)
	}
	if *moved.FracIndex <= *dstBFresh.FracIndex {
		t.Errorf("appended key %q must sort after dstB %q", *moved.FracIndex, *dstBFresh.FracIndex)
	}
}

// Bug-hunt-2 #4: Move previously checked only the moved page's new
// depth, not the subtree's. A deep chain of descendants reparented
// under a deep parent could land descendants past MaxPageDepth.
func TestPageService_Move_RejectsWhenSubtreeWouldOverflowDepth(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	// Build a chain a → b → c → d as a subtree (4 levels deep). Then
	// build another chain e → f → … long enough that grafting the
	// 4-level subtree under its tail would breach MaxPageDepth=30.
	a, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "a"})
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &a.ID, Title: "b"})
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	c, err := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &b.ID, Title: "c"})
	if err != nil {
		t.Fatalf("c: %v", err)
	}
	if _, err := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &c.ID, Title: "d"}); err != nil {
		t.Fatalf("d: %v", err)
	}
	// a-subtree max depth is 3 (a=0, d=3). page.Depth+offset=3.

	// Build the long chain under root. Take it up to depth 28 — moving
	// a (offset 3) under depth-28 would land d at depth 31, past
	// MaxPageDepth=30.
	parentID := 0
	for i := 0; i < 28; i++ {
		var in CreatePageInput
		in.WorkspaceID = 1
		in.Title = fmt.Sprintf("chain-%d", i)
		if parentID != 0 {
			pid := parentID
			in.ParentID = &pid
		}
		page, cerr := s.Create(1, in)
		if cerr != nil {
			t.Fatalf("chain[%d]: %v", i, cerr)
		}
		parentID = page.ID
	}
	deepLeaf, _ := s.GetByID(parentID)
	// deepLeaf.Depth should be 27 (28 nodes starting at 0).
	if deepLeaf.Depth != 27 {
		t.Fatalf("expected deepLeaf.Depth=27, got %d", deepLeaf.Depth)
	}

	// Moving `a` (whose subtree has max-offset 3) under deepLeaf would
	// land its `d` descendant at depth 28+3=31 (>= MaxPageDepth=30).
	// The move must refuse.
	if _, err := s.Move(1, a.ID, &deepLeaf.ID, nil, nil); !errors.Is(err, ErrPageDepthExceeded) {
		t.Errorf("subtree depth overflow: want ErrPageDepthExceeded, got %v", err)
	}
}

// A move that lands two same-slug pages under one parent used to be
// rejected with a 409 (bug-hunt-2 #5 made the repo's ErrDuplicateEntry
// surface as ErrPageSlugConflict rather than a 500). With slug uniqueness
// dropped there is nothing to collide with, so the move must simply
// succeed and both children keep the slug.
func TestPageService_Move_AllowsDuplicateSiblingSlug(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	parentA, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "ParentA"})
	parentB, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "ParentB"})
	childA, err := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &parentA.ID, Title: "Notes"})
	if err != nil {
		t.Fatalf("a/notes: %v", err)
	}
	childB, err := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &parentB.ID, Title: "Notes"})
	if err != nil {
		t.Fatalf("b/notes: %v", err)
	}

	moved, err := s.Move(1, childB.ID, &parentA.ID, nil, nil)
	if err != nil {
		t.Fatalf("move onto a same-slug sibling: %v", err)
	}
	if moved.ParentID == nil || *moved.ParentID != parentA.ID {
		t.Errorf("parent after move: want %d, got %v", parentA.ID, moved.ParentID)
	}
	if moved.Slug != childA.Slug {
		t.Errorf("slug after move: want %q (unchanged, matching its new sibling), got %q", childA.Slug, moved.Slug)
	}
}

func TestPageService_Move_RejectsSelf(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	page, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Solo"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Move(1, page.ID, &page.ID, nil, nil); !errors.Is(err, ErrPageCycle) {
		t.Errorf("self-move: want ErrPageCycle, got %v", err)
	}
}

func TestPageService_Move_RejectsCycle(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	a, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "A"})
	b, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &a.ID, Title: "B"})
	c, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &b.ID, Title: "C"})

	// Moving A under C (its grandchild) would create a cycle.
	if _, err := s.Move(1, a.ID, &c.ID, nil, nil); !errors.Is(err, ErrPageCycle) {
		t.Errorf("want ErrPageCycle, got %v", err)
	}
}

func TestPageService_Move_UpdatesDescendantPaths(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	a, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "A"})
	b, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &a.ID, Title: "B"})
	c, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &b.ID, Title: "C"})
	otherRoot, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "OtherRoot"})

	// Move B under otherRoot. c (descendant of b) should follow.
	if _, err := s.Move(1, b.ID, &otherRoot.ID, nil, nil); err != nil {
		t.Fatalf("move: %v", err)
	}

	bAfter, err := s.GetByID(b.ID)
	if err != nil {
		t.Fatalf("get b: %v", err)
	}
	wantBPath := fmt.Sprintf("/%d/", otherRoot.ID)
	if bAfter.Path != wantBPath || bAfter.Depth != 1 {
		t.Errorf("b after move: path=%q depth=%d, want path=%q depth=1", bAfter.Path, bAfter.Depth, wantBPath)
	}

	cAfter, err := s.GetByID(c.ID)
	if err != nil {
		t.Fatalf("get c: %v", err)
	}
	wantCPath := fmt.Sprintf("/%d/%d/", otherRoot.ID, b.ID)
	if cAfter.Path != wantCPath || cAfter.Depth != 2 {
		t.Errorf("c after move: path=%q depth=%d, want path=%q depth=2", cAfter.Path, cAfter.Depth, wantCPath)
	}

	// Original root A is no longer an ancestor of B/C.
	aAfter, _ := s.GetByID(a.ID)
	if aAfter.Path != "/" || aAfter.Depth != 0 {
		t.Errorf("a should be unchanged, got path=%q depth=%d", aAfter.Path, aAfter.Depth)
	}
}

// Move with prev/next sibling IDs assigns a frac_index that puts the moved
// page between the named neighbors in display order, backfilling NULL keys
// for the rest of the sibling group on first use.
func TestPageService_Move_ReordersBetweenSiblings(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	// Three root pages in title order: A, B, C — all have NULL frac_index.
	a, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "A"})
	b, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "B"})
	c, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "C"})

	// Move C between A and B: prev=A, next=B.
	if _, err := s.Move(1, c.ID, nil, &a.ID, &b.ID); err != nil {
		t.Fatalf("reorder C between A and B: %v", err)
	}

	roots, err := s.ListChildren(1, nil)
	if err != nil {
		t.Fatalf("list root children: %v", err)
	}
	gotOrder := []int{}
	for _, p := range roots {
		gotOrder = append(gotOrder, p.ID)
	}
	wantOrder := []int{a.ID, c.ID, b.ID}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Errorf("display order after reorder: got %v, want %v", gotOrder, wantOrder)
	}

	// All three siblings must have non-NULL frac_index now (backfill ran).
	for _, p := range roots {
		if p.FracIndex == nil || *p.FracIndex == "" {
			t.Errorf("page %d should have frac_index after backfill, got nil/empty", p.ID)
		}
	}
}

// Move with sibling IDs but cross-parent: page becomes a child of newParent
// and lands between the named siblings.
func TestPageService_Move_ReparentsAndReordersInOnePass(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	root, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Root"})
	x, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &root.ID, Title: "X"})
	y, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &root.ID, Title: "Y"})
	visitor, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Visitor"})

	// Move Visitor under Root, between X and Y.
	if _, err := s.Move(1, visitor.ID, &root.ID, &x.ID, &y.ID); err != nil {
		t.Fatalf("reparent + reorder: %v", err)
	}

	kids, err := s.ListChildren(1, &root.ID)
	if err != nil {
		t.Fatalf("list root children: %v", err)
	}
	got := []int{}
	for _, k := range kids {
		got = append(got, k.ID)
	}
	want := []int{x.ID, visitor.ID, y.ID}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("children after reparent: got %v, want %v", got, want)
	}
}

// Move at the start of the list (prev=nil, next=first sibling) places the
// moved page before all current siblings.
func TestPageService_Move_PlacesAtListStart(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	a, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "A"})
	b, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "B"})

	// Move B before A: prev=nil, next=A.
	if _, err := s.Move(1, b.ID, nil, nil, &a.ID); err != nil {
		t.Fatalf("move B to start: %v", err)
	}

	roots, err := s.ListChildren(1, nil)
	if err != nil {
		t.Fatalf("list roots: %v", err)
	}
	got := []int{}
	for _, p := range roots {
		got = append(got, p.ID)
	}
	want := []int{b.ID, a.ID}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order after move-to-start: got %v, want %v", got, want)
	}
}

func TestPageService_Archive_CascadesToSubtree(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	a, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "A", Content: "# A\n\nroot body"})
	b, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &a.ID, Title: "B", Content: "# B\n\nchild body"})
	c, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &b.ID, Title: "C", Content: "# C\n\ngrandchild body"})
	other, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Other", Content: "# Other\n\nunrelated"})

	if err := s.Archive(1, a.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	for _, id := range []int{a.ID, b.ID, c.ID} {
		page, err := s.GetByID(id)
		if err != nil {
			t.Fatalf("get %d: %v", id, err)
		}
		if page.ArchivedAt == nil {
			t.Errorf("page %d should be archived", id)
		}
		if got := listChunksForPage(t, db, id); len(got) != 0 {
			t.Errorf("page %d: expected chunks dropped on subtree archive, got %d", id, len(got))
		}
	}

	// Other root must remain live and keep its chunks.
	otherAfter, _ := s.GetByID(other.ID)
	if otherAfter.ArchivedAt != nil {
		t.Errorf("unrelated page %d should not be archived", other.ID)
	}
	if got := listChunksForPage(t, db, other.ID); len(got) == 0 {
		t.Errorf("unrelated page %d: chunks should be untouched, got 0", other.ID)
	}
}

func TestPageService_ListTree_FiltersArchived(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	live, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Live"})
	archived, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Bye"})
	if err := s.Archive(1, archived.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	visible, err := s.ListTree(1, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(visible) != 1 || visible[0].ID != live.ID {
		t.Errorf("expected only live page in tree, got %+v", visible)
	}

	full, err := s.ListTree(1, true)
	if err != nil {
		t.Fatalf("list w/ archived: %v", err)
	}
	if len(full) != 2 {
		t.Errorf("expected 2 pages with archived included, got %d", len(full))
	}
}

func TestPageService_Create_WritesFirstRevisionAndChunks(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	page, err := s.Create(1, CreatePageInput{
		WorkspaceID: 1,
		Title:       "Onboarding",
		Content:     "# Welcome\n\nThis is the intro.\n\n## Setup\n\nFollow these steps.",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	revs, err := s.ListRevisions(page.ID, 0, 0)
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	if len(revs) != 1 {
		t.Fatalf("expected 1 revision after create, got %d", len(revs))
	}
	if revs[0].ChangeType != "create" || revs[0].RevisionNumber != 1 {
		t.Errorf("first revision: change_type=%q rev=%d, want change_type=create rev=1", revs[0].ChangeType, revs[0].RevisionNumber)
	}

	chunks := listChunksForPage(t, db, page.ID)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks (2 headings), got %d", len(chunks))
	}
	if chunks[0].RevisionNumber != 1 {
		t.Errorf("chunk revision_number: want 1, got %d", chunks[0].RevisionNumber)
	}
	if chunks[0].HeadingPath == "" {
		t.Error("first chunk should carry the heading_path of the first heading section")
	}
}

func TestPageService_Update_BumpsRevisionAndRebuildsChunks(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	page, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Doc", Content: "# A\n\nbody"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := s.Update(2, UpdatePageInput{ID: page.ID, Title: "Doc", Content: "# A\n\nrewritten body"}); err != nil {
		t.Fatalf("update: %v", err)
	}

	revs, err := s.ListRevisions(page.ID, 0, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("expected 2 revisions, got %d", len(revs))
	}
	if revs[0].RevisionNumber != 2 || revs[0].ChangeType != "edit" {
		t.Errorf("newest revision: rev=%d type=%q, want 2/edit", revs[0].RevisionNumber, revs[0].ChangeType)
	}

	chunks := listChunksForPage(t, db, page.ID)
	for _, c := range chunks {
		if c.RevisionNumber != 2 {
			t.Errorf("chunk revision_number: want 2, got %d", c.RevisionNumber)
		}
		if !strings.Contains(c.Content, "rewritten") {
			t.Errorf("chunk content should reflect latest update, got %q", c.Content)
		}
	}
}

func TestPageService_Restore_ProducesRestoreRevision(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	page, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Doc", Content: "original"})
	if _, err := s.Update(2, UpdatePageInput{ID: page.ID, Title: "Doc", Content: "second pass"}); err != nil {
		t.Fatalf("update: %v", err)
	}

	revs, _ := s.ListRevisions(page.ID, 0, 0)
	// revs[0] = rev 2 (edit), revs[1] = rev 1 (create with "original" content)
	var rev1 int
	for _, r := range revs {
		if r.RevisionNumber == 1 {
			rev1 = r.ID
			break
		}
	}
	if rev1 == 0 {
		t.Fatalf("revision 1 not found in %+v", revs)
	}

	restored, err := s.Restore(1, page.ID, rev1)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored.Content != "original" {
		t.Errorf("restored content: want %q, got %q", "original", restored.Content)
	}

	revs2, _ := s.ListRevisions(page.ID, 0, 0)
	if len(revs2) != 3 {
		t.Fatalf("expected 3 revisions after restore, got %d", len(revs2))
	}
	if revs2[0].ChangeType != "restore" {
		t.Errorf("newest revision should be 'restore', got %q", revs2[0].ChangeType)
	}
}

func TestPageService_Restore_RejectsCrossPageRevision(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	a, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "A", Content: "a"})
	b, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "B", Content: "b"})

	bRevs, _ := s.ListRevisions(b.ID, 0, 0)
	if len(bRevs) == 0 {
		t.Fatalf("no revisions on B")
	}
	if _, err := s.Restore(1, a.ID, bRevs[0].ID); !errors.Is(err, ErrPageRevisionMismatch) {
		t.Errorf("want ErrPageRevisionMismatch, got %v", err)
	}
}

func TestPageService_Archive_RemovesChunks(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	page, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Old", Content: "stuff to forget"})

	if got := listChunksForPage(t, db, page.ID); len(got) == 0 {
		t.Fatalf("expected chunks before archive")
	}
	if err := s.Archive(1, page.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if got := listChunksForPage(t, db, page.ID); len(got) != 0 {
		t.Errorf("expected chunks removed on archive, got %d", len(got))
	}

	revs, _ := s.ListRevisions(page.ID, 0, 0)
	if len(revs) == 0 || revs[0].ChangeType != "archive" {
		t.Errorf("newest revision should be 'archive', got %+v", revs)
	}
}

func TestPageService_Archive_RemovesSubtreeChunks(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	root, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Root", Content: "# Root\n\nroot body to index"})
	child, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &root.ID, Title: "Child", Content: "# Child\n\nchild body to index"})
	grand, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &child.ID, Title: "Grand", Content: "# Grand\n\ngrandchild body to index"})
	sibling, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Sibling", Content: "# Sibling\n\nsibling body to index"})

	for _, id := range []int{root.ID, child.ID, grand.ID, sibling.ID} {
		if got := listChunksForPage(t, db, id); len(got) == 0 {
			t.Fatalf("expected chunks before archive for page %d", id)
		}
	}

	if err := s.Archive(1, root.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	for _, id := range []int{root.ID, child.ID, grand.ID} {
		if got := listChunksForPage(t, db, id); len(got) != 0 {
			t.Errorf("page %d: expected chunks dropped on subtree archive, got %d", id, len(got))
		}
	}
	if got := listChunksForPage(t, db, sibling.ID); len(got) == 0 {
		t.Errorf("sibling page %d: chunks should be untouched after unrelated archive", sibling.ID)
	}
}

// listChunksForPage reads page_chunks directly for assertions, decoupled
// from the service surface so test failures point at the chunk pipeline.
type chunkRow struct {
	Position       int
	RevisionNumber int
	HeadingPath    string
	Content        string
}

func listChunksForPage(t *testing.T, db database.Database, pageID int) []chunkRow {
	t.Helper()
	rows, err := db.Query(`
		SELECT position, revision_number, heading_path, content
		FROM page_chunks WHERE page_id = ? ORDER BY position
	`, pageID)
	if err != nil {
		t.Fatalf("query chunks: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []chunkRow
	for rows.Next() {
		var c chunkRow
		if err := rows.Scan(&c.Position, &c.RevisionNumber, &c.HeadingPath, &c.Content); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, c)
	}
	return out
}

func TestChunkPageMarkdown_BreaksOnHeadings(t *testing.T) {
	md := "# Top\n\nintro\n\n## Mid\n\nmiddle\n\n### Deep\n\nbottom\n"
	specs := chunkPageMarkdown(md)
	if len(specs) < 3 {
		t.Fatalf("expected at least 3 chunks for 3 headings, got %d", len(specs))
	}
	if specs[0].HeadingPath != "Top" {
		t.Errorf("first chunk heading_path: want Top, got %q", specs[0].HeadingPath)
	}
	if specs[1].HeadingPath != "Top > Mid" {
		t.Errorf("second chunk heading_path: want Top > Mid, got %q", specs[1].HeadingPath)
	}
	if specs[2].HeadingPath != "Top > Mid > Deep" {
		t.Errorf("third chunk heading_path: want Top > Mid > Deep, got %q", specs[2].HeadingPath)
	}
}

func TestChunkPageMarkdown_SplitsOversizeSections(t *testing.T) {
	body := strings.Repeat("paragraph one. ", 200) + "\n\n" + strings.Repeat("paragraph two. ", 200)
	md := "# Big\n\n" + body
	specs := chunkPageMarkdown(md)
	if len(specs) < 2 {
		t.Fatalf("expected oversize section to split, got %d", len(specs))
	}
	for _, s := range specs {
		if len(s.Content) > pageChunkMaxBytes+pageChunkMinBytes {
			t.Errorf("chunk exceeds max+min slack: %d bytes", len(s.Content))
		}
	}
}

func TestChunkPageMarkdown_PreservesUTF8AtByteLimit(t *testing.T) {
	md := "*" + strings.Repeat("a", pageChunkMaxBytes-2) + "é\n**bold text**\nlast italic line*"
	specs := chunkPageMarkdown(md)
	for i, spec := range specs {
		if !utf8.ValidString(spec.Content) {
			t.Errorf("chunk %d contains invalid UTF-8: %q", i, spec.Content)
		}
	}
}

func TestPageService_GrantPermission_PersistsAndRecordsRevision(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	page, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "T", Content: "c"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	row, err := s.GrantPermission(1, page.ID, "user", 2, "edit")
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if row.PageID != page.ID || row.PrincipalID != 2 || row.PermissionLevel != "edit" {
		t.Errorf("granted row mismatch: %+v", row)
	}

	// Audit revision should record the change.
	revs, _ := s.ListRevisions(page.ID, 0, 0)
	found := false
	for _, r := range revs {
		if r.ChangeType == "permissions" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a 'permissions' revision after grant; got revs=%+v", revs)
	}
}

func TestPageService_GrantPermission_RejectsBadPrincipalAndLevel(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	page, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "T"})

	if _, err := s.GrantPermission(1, page.ID, "team", 5, "edit"); !errors.Is(err, ErrPageInvalidPrincipal) {
		t.Errorf("bad principal: want ErrPageInvalidPrincipal, got %v", err)
	}
	if _, err := s.GrantPermission(1, page.ID, "user", 5, "owner"); !errors.Is(err, ErrPageInvalidLevel) {
		t.Errorf("bad level: want ErrPageInvalidLevel, got %v", err)
	}
}

func TestPageService_GrantPermission_DuplicateReturnsError(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	page, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "T"})

	if _, err := s.GrantPermission(1, page.ID, "user", 2, "edit"); err != nil {
		t.Fatalf("first grant: %v", err)
	}
	if _, err := s.GrantPermission(1, page.ID, "user", 2, "edit"); !errors.Is(err, ErrPagePermissionDuplicate) {
		t.Errorf("duplicate grant: want ErrPagePermissionDuplicate, got %v", err)
	}
}

func TestPageService_RevokePermission_RemovesRow(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	page, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "T"})
	row, _ := s.GrantPermission(1, page.ID, "user", 2, "edit")

	if err := s.RevokePermission(1, page.ID, row.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	acl, _ := s.ListOwnACL(page.ID)
	if len(acl) != 0 {
		t.Errorf("expected empty ACL after revoke, got %d", len(acl))
	}
}

func TestPageService_RevokePermission_RejectsCrossPageID(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	a, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "A"})
	b, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "B"})
	rowA, _ := s.GrantPermission(1, a.ID, "user", 2, "edit")

	if err := s.RevokePermission(1, b.ID, rowA.ID); !errors.Is(err, ErrPageNotFound) {
		t.Errorf("cross-page revoke: want ErrPageNotFound, got %v", err)
	}
	// The row should still exist under page A.
	acl, _ := s.ListOwnACL(a.ID)
	if len(acl) != 1 {
		t.Errorf("row should remain on page A, got %d", len(acl))
	}
}

func TestPageService_SetInheritPermissions_TogglesAndRecords(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	page, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "T"})
	if !page.InheritPermissions {
		t.Fatal("page should inherit by default")
	}

	updated, err := s.SetInheritPermissions(1, page.ID, false)
	if err != nil {
		t.Fatalf("set inherit=false: %v", err)
	}
	if updated.InheritPermissions {
		t.Error("expected inherit_permissions=false after flip")
	}

	// Revision recorded with change_type=permissions.
	revs, _ := s.ListRevisions(page.ID, 0, 0)
	if len(revs) == 0 || revs[0].ChangeType != "permissions" {
		t.Errorf("expected newest revision to be 'permissions', got %+v", revs)
	}
}

func TestPageService_SetInheritPermissions_NoopWhenUnchanged(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	page, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "T"})

	// Already inherit=true; calling with true should be a no-op.
	before, _ := s.ListRevisions(page.ID, 0, 0)
	if _, err := s.SetInheritPermissions(1, page.ID, true); err != nil {
		t.Fatalf("noop set: %v", err)
	}
	after, _ := s.ListRevisions(page.ID, 0, 0)
	if len(after) != len(before) {
		t.Errorf("no-op set inheritance should not add a revision; before=%d after=%d", len(before), len(after))
	}
}

func TestBuildPageTree_AssemblesNestedNodes(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	a, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "A"})
	b, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &a.ID, Title: "B"})
	_, _ = s.Create(1, CreatePageInput{WorkspaceID: 1, ParentID: &b.ID, Title: "C"})

	flat, err := s.ListTree(1, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	roots := BuildPageTree(flat)
	if len(roots) != 1 || roots[0].ID != a.ID {
		t.Fatalf("expected single root A, got %+v", roots)
	}
	if len(roots[0].Children) != 1 || roots[0].Children[0].ID != b.ID {
		t.Fatalf("expected A→B, got %+v", roots[0].Children)
	}
	if len(roots[0].Children[0].Children) != 1 {
		t.Fatalf("expected B→C, got %+v", roots[0].Children[0].Children)
	}
}

func TestPageService_SearchByKeyword_MatchesContentAndEscapesLikeMetacharacters(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	for _, title := range []string{"foo_bar", "fooxbar", "foobar", "100% awesome", "back\\slash"} {
		if _, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: title}); err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
	}
	if _, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Operations", Content: "deploy_keyword appears only in the body"}); err != nil {
		t.Fatalf("create content-search page: %v", err)
	}

	contentMatch, err := s.SearchByKeyword(1, "deploy_keyword", 20)
	if err != nil {
		t.Fatalf("search content: %v", err)
	}
	if len(contentMatch) != 1 || contentMatch[0].Title != "Operations" {
		t.Fatalf("content search: want Operations, got %+v", contentMatch)
	}

	// Underscore must match literally, not act as a single-character wildcard.
	got, err := s.SearchByKeyword(1, "foo_bar", 20)
	if err != nil {
		t.Fatalf("search underscore: %v", err)
	}
	if len(got) != 1 || got[0].Title != "foo_bar" {
		titles := make([]string, 0, len(got))
		for _, p := range got {
			titles = append(titles, p.Title)
		}
		t.Fatalf("foo_bar search: want exactly [foo_bar], got %v", titles)
	}

	// Percent must match literally.
	gotPct, err := s.SearchByKeyword(1, "100%", 20)
	if err != nil {
		t.Fatalf("search percent: %v", err)
	}
	if len(gotPct) != 1 || gotPct[0].Title != "100% awesome" {
		t.Fatalf("100%% search: got %+v", gotPct)
	}

	// Backslash must not break the ESCAPE clause.
	gotBS, err := s.SearchByKeyword(1, `back\slash`, 20)
	if err != nil {
		t.Fatalf("search backslash: %v", err)
	}
	if len(gotBS) != 1 || gotBS[0].Title != "back\\slash" {
		t.Fatalf("backslash search: got %+v", gotBS)
	}
}
