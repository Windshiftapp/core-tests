package services

import (
	"testing"

	"windshift/internal/models"
)

func TestPageService_Restore_UnarchivesPageAndRebuildsChunks(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)
	page, _ := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Recover", Content: "recoverable content"})
	revs, _ := s.ListRevisions(page.ID, 0, 0)
	if len(revs) == 0 {
		t.Fatal("expected create revision")
	}
	if err := s.Archive(1, page.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	archived, _ := s.GetByID(page.ID)
	if archived.ArchivedAt == nil {
		t.Fatal("page should be archived before restore")
	}
	if got := listChunksForPage(t, db, page.ID); len(got) != 0 {
		t.Fatalf("archive should remove chunks, got %d", len(got))
	}

	restored, err := s.Restore(1, page.ID, revs[0].ID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored.ArchivedAt != nil || restored.ArchivedBy != nil {
		t.Fatalf("restore should clear archive metadata, got archived_at=%v archived_by=%v", restored.ArchivedAt, restored.ArchivedBy)
	}
	if got := listChunksForPage(t, db, page.ID); len(got) == 0 {
		t.Fatal("restore should rebuild chunks")
	}
}

// TestPageService_Archive_ParentWithAlreadyArchivedChild guards the fix for
// the misleading 404 when archiving a page whose subtree already contains an
// archived descendant. The subtree authorization refuses admin on archived
// pages (they're frozen to all ops but view/restore), so before the fix the
// callback saw the archived child, returned ErrPageNotFound, and the parent
// archive 404'd. ArchiveChecked now scopes both authorization and the write to
// the not-yet-archived rows.
func TestPageService_Archive_ParentWithAlreadyArchivedChild(t *testing.T) {
	db := newPagesTestDB(t)
	s := NewPageService(db)

	parent, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := s.Create(1, CreatePageInput{WorkspaceID: 1, Title: "Child", ParentID: &parent.ID})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Archive just the child first.
	if err := s.Archive(1, child.ID); err != nil {
		t.Fatalf("archive child: %v", err)
	}
	childRevsBefore, _ := s.ListRevisions(child.ID, 0, 0)

	// Mimic the HTTP handler's subtree authorization, which maps "no admin on
	// an archived page" to ErrPageNotFound.
	authorize := func(subtree []models.Page) error {
		for i := range subtree {
			if subtree[i].ArchivedAt != nil {
				return ErrPageNotFound
			}
		}
		return nil
	}
	if err := s.ArchiveChecked(1, parent.ID, authorize); err != nil {
		t.Fatalf("archiving a parent with an already-archived child should succeed, got: %v", err)
	}

	if got, _ := s.GetByID(parent.ID); got.ArchivedAt == nil {
		t.Error("parent should be archived")
	}
	if got, _ := s.GetByID(child.ID); got.ArchivedAt == nil {
		t.Error("child should remain archived")
	}

	// The already-archived child must not be re-touched: no extra
	// "archived with subtree" revision.
	childRevsAfter, _ := s.ListRevisions(child.ID, 0, 0)
	if len(childRevsAfter) != len(childRevsBefore) {
		t.Errorf("already-archived child should not gain an archive revision: before=%d after=%d",
			len(childRevsBefore), len(childRevsAfter))
	}
}

func TestPagePermission_ArchivedPage_RestoreAllowedForWorkspaceAdminOnly(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Recoverable"})
	if err := env.pages.Archive(env.users["bob"], page.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	can, err := env.auth.Can(env.users["bob"], 1, page.ID, PageOpRestore)
	if err != nil || !can {
		t.Fatalf("workspace admin should restore archived page: can=%v err=%v", can, err)
	}
	can, err = env.auth.Can(env.users["alice"], 1, page.ID, PageOpRestore)
	if err != nil {
		t.Fatalf("alice restore: %v", err)
	}
	if can {
		t.Fatal("editor must not restore archived page")
	}
}
