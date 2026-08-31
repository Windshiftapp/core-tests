//go:build test

package services_test

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"windshift/internal/database"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
	"windshift/internal/validation"
)

// commentTestEnv contains test data for comment service tests
type commentTestEnv struct {
	WorkspaceID int
	ItemID      int
	UserID      int
}

// createCommentTestDB creates a test database for comment service tests
func createCommentTestDB(t *testing.T) database.Database {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	return tdb.GetDatabase()
}

// setupCommentTestEnv creates test data for comment service tests using the factory
func setupCommentTestEnv(t *testing.T, db database.Database) commentTestEnv {
	t.Helper()
	f := factory.NewTestFactory(db)
	env, err := f.CreateFullTestEnv()
	if err != nil {
		t.Fatalf("Failed to create test env: %v", err)
	}
	return commentTestEnv{
		WorkspaceID: env.WorkspaceID,
		ItemID:      env.ItemID,
		UserID:      env.UserID,
	}
}

func TestCommentService_Create(t *testing.T) {
	db := createCommentTestDB(t)

	service := services.NewCommentService(db)
	env := setupCommentTestEnv(t, db)

	t.Run("Success", func(t *testing.T) {
		params := services.CreateCommentParams{
			ItemID:      env.ItemID,
			AuthorID:    env.UserID,
			Content:     "This is a test comment",
			IsPrivate:   false,
			ActorUserID: env.UserID,
		}

		result, err := service.Create(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if result.CommentID == 0 {
			t.Error("Expected non-zero comment ID")
		}

		// Verify comment was created
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM comments WHERE id = ?", result.CommentID).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to verify comment creation: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 comment, got %d", count)
		}
	})

	t.Run("RejectsWhitespaceOnlySource", func(t *testing.T) {
		_, err := service.Create(services.CreateCommentParams{
			ItemID:      env.ItemID,
			AuthorID:    env.UserID,
			Content:     " \n",
			ActorUserID: env.UserID,
		})
		var validationErr *validation.ValidationError
		if !errors.As(err, &validationErr) || validationErr.Field != "content" || validationErr.Message != "content is required" {
			t.Fatalf("error = %v, want required content ValidationError", err)
		}
	})

	t.Run("PreservesRawHTMLSource", func(t *testing.T) {
		params := services.CreateCommentParams{
			ItemID:      env.ItemID,
			AuthorID:    env.UserID,
			Content:     "<script>alert('xss')</script>Safe content",
			IsPrivate:   false,
			ActorUserID: env.UserID,
		}

		result, err := service.Create(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		var content string
		err = db.QueryRow("SELECT content FROM comments WHERE id = ?", result.CommentID).Scan(&content)
		if err != nil {
			t.Fatalf("Failed to fetch comment: %v", err)
		}

		if content != params.Content {
			t.Errorf("stored content = %q, want exact source %q", content, params.Content)
		}
	})

	t.Run("PrivateComment", func(t *testing.T) {
		params := services.CreateCommentParams{
			ItemID:      env.ItemID,
			AuthorID:    env.UserID,
			Content:     "This is a private note",
			IsPrivate:   true,
			ActorUserID: env.UserID,
		}

		result, err := service.Create(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify is_private flag
		var isPrivate bool
		err = db.QueryRow("SELECT is_private FROM comments WHERE id = ?", result.CommentID).Scan(&isPrivate)
		if err != nil {
			t.Fatalf("Failed to fetch comment: %v", err)
		}
		if !isPrivate {
			t.Error("Expected comment to be private")
		}
	})

	t.Run("PreservesDangerousMarkdownSource", func(t *testing.T) {
		params := services.CreateCommentParams{
			ItemID:      env.ItemID,
			AuthorID:    env.UserID,
			Content:     "[Click me](javascript:alert(document.cookie))",
			IsPrivate:   false,
			ActorUserID: env.UserID,
		}

		result, err := service.Create(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		var content string
		err = db.QueryRow("SELECT content FROM comments WHERE id = ?", result.CommentID).Scan(&content)
		if err != nil {
			t.Fatalf("Failed to fetch comment: %v", err)
		}

		if content != params.Content {
			t.Errorf("stored content = %q, want exact source %q", content, params.Content)
		}
	})

	t.Run("PreservesDataURISource", func(t *testing.T) {
		params := services.CreateCommentParams{
			ItemID:      env.ItemID,
			AuthorID:    env.UserID,
			Content:     "![img](data:text/html,<script>alert(1)</script>)",
			IsPrivate:   false,
			ActorUserID: env.UserID,
		}

		result, err := service.Create(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		var content string
		err = db.QueryRow("SELECT content FROM comments WHERE id = ?", result.CommentID).Scan(&content)
		if err != nil {
			t.Fatalf("Failed to fetch comment: %v", err)
		}

		if content != params.Content {
			t.Errorf("stored content = %q, want exact source %q", content, params.Content)
		}
	})

	t.Run("ItemNotFound", func(t *testing.T) {
		params := services.CreateCommentParams{
			ItemID:      99999,
			AuthorID:    env.UserID,
			Content:     "Comment on non-existent item",
			IsPrivate:   false,
			ActorUserID: env.UserID,
		}

		_, err := service.Create(params)
		if err == nil {
			t.Error("Expected error for non-existent item")
		}
	})
}

func TestCommentService_Get(t *testing.T) {
	db := createCommentTestDB(t)

	service := services.NewCommentService(db)
	env := setupCommentTestEnv(t, db)

	// Create a comment to retrieve
	params := services.CreateCommentParams{
		ItemID:      env.ItemID,
		AuthorID:    env.UserID,
		Content:     "Comment to retrieve",
		IsPrivate:   false,
		ActorUserID: env.UserID,
	}
	created, err := service.Create(params)
	if err != nil {
		t.Fatalf("Failed to create test comment: %v", err)
	}

	t.Run("Success", func(t *testing.T) {
		comment, err := service.Get(int(created.CommentID))
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if comment.ID != int(created.CommentID) {
			t.Errorf("Expected comment ID %d, got %d", created.CommentID, comment.ID)
		}
		if comment.Content != "Comment to retrieve" {
			t.Errorf("Expected content 'Comment to retrieve', got '%s'", comment.Content)
		}
		if comment.AuthorName == "" {
			t.Error("Expected author name to be populated")
		}
		if comment.WorkspaceID != env.WorkspaceID {
			t.Errorf("Expected workspace ID %d, got %d", env.WorkspaceID, comment.WorkspaceID)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		_, err := service.Get(99999)
		if err == nil {
			t.Error("Expected error for non-existent comment")
		}
	})
}

func TestCommentService_Update(t *testing.T) {
	db := createCommentTestDB(t)

	service := services.NewCommentService(db)
	env := setupCommentTestEnv(t, db)

	// Create a comment to update
	params := services.CreateCommentParams{
		ItemID:      env.ItemID,
		AuthorID:    env.UserID,
		Content:     "Original content",
		IsPrivate:   false,
		ActorUserID: env.UserID,
	}
	created, err := service.Create(params)
	if err != nil {
		t.Fatalf("Failed to create test comment: %v", err)
	}

	t.Run("Success", func(t *testing.T) {
		comment, err := service.Update(int(created.CommentID), "Updated content", env.UserID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if comment.Content != "Updated content" {
			t.Errorf("Expected content 'Updated content', got '%s'", comment.Content)
		}
	})

	t.Run("PreservesRawHTMLSource", func(t *testing.T) {
		source := "<b>Bold</b> text"
		comment, err := service.Update(int(created.CommentID), source, env.UserID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if comment.Content != source {
			t.Errorf("updated content = %q, want exact source %q", comment.Content, source)
		}
	})

	t.Run("PreservesDangerousMarkdownSource", func(t *testing.T) {
		source := "[evil](javascript:alert(1))"
		comment, err := service.Update(int(created.CommentID), source, env.UserID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if comment.Content != source {
			t.Errorf("updated content = %q, want exact source %q", comment.Content, source)
		}
	})

	t.Run("PreservesDataURISource", func(t *testing.T) {
		source := "![x](data:text/html,payload)"
		comment, err := service.Update(int(created.CommentID), source, env.UserID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if comment.Content != source {
			t.Errorf("updated content = %q, want exact source %q", comment.Content, source)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		_, err := service.Update(99999, "New content", env.UserID)
		if err == nil {
			t.Error("Expected error for non-existent comment")
		}
	})
}

func TestCommentService_ImportedWritesBoundSource(t *testing.T) {
	db := createCommentTestDB(t)
	service := services.NewCommentService(db)
	env := setupCommentTestEnv(t, db)
	source := strings.Repeat("x", validation.MarkdownMaxBytes-1) + "øtrailing"
	want := strings.Repeat("x", validation.MarkdownMaxBytes-1)

	assertStored := func(commentID int, wantContent string) {
		t.Helper()
		var content string
		if err := db.QueryRow("SELECT content FROM comments WHERE id = ?", commentID).Scan(&content); err != nil {
			t.Fatalf("load imported comment: %v", err)
		}
		if content != wantContent {
			t.Fatalf("stored content length = %d, want %d", len(content), len(wantContent))
		}
		if !utf8.ValidString(content) {
			t.Fatal("stored content is not valid UTF-8")
		}
	}

	t.Run("CreateImported", func(t *testing.T) {
		invalidSource := "before" + string([]byte{0xff}) + "after"
		created, err := service.CreateImported(services.CreateCommentParams{
			ItemID:      env.ItemID,
			AuthorID:    env.UserID,
			ActorUserID: env.UserID,
			Content:     invalidSource,
		})
		if err != nil {
			t.Fatalf("create imported comment: %v", err)
		}
		assertStored(int(created.CommentID), "before\uFFFDafter")
	})

	t.Run("UpdateImported", func(t *testing.T) {
		created, err := service.Create(services.CreateCommentParams{
			ItemID:      env.ItemID,
			AuthorID:    env.UserID,
			ActorUserID: env.UserID,
			Content:     "before import update",
		})
		if err != nil {
			t.Fatalf("create seed comment: %v", err)
		}
		commentID := int(created.CommentID)
		if err := service.UpdateImported(services.UpdateImportedCommentParams{
			CommentID: commentID,
			ItemID:    env.ItemID,
			AuthorID:  env.UserID,
			Content:   source,
		}); err != nil {
			t.Fatalf("update imported comment: %v", err)
		}
		assertStored(commentID, want)
	})

	t.Run("CreateInTx", func(t *testing.T) {
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		commentID, err := service.CreateInTx(t.Context(), tx, env.ItemID, env.UserID, source, time.Now())
		if err != nil {
			t.Fatalf("create imported comment in transaction: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		assertStored(int(commentID), want)
	})

	t.Run("UpdateContentInTx", func(t *testing.T) {
		created, err := service.Create(services.CreateCommentParams{
			ItemID:      env.ItemID,
			AuthorID:    env.UserID,
			ActorUserID: env.UserID,
			Content:     "before transactional import update",
		})
		if err != nil {
			t.Fatalf("create seed comment: %v", err)
		}
		commentID := int(created.CommentID)
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		if err := service.UpdateContentInTx(t.Context(), tx, commentID, source, time.Now()); err != nil {
			t.Fatalf("update imported comment in transaction: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		assertStored(commentID, want)
	})
}

func TestCommentService_Delete(t *testing.T) {
	db := createCommentTestDB(t)

	service := services.NewCommentService(db)
	env := setupCommentTestEnv(t, db)

	t.Run("Success", func(t *testing.T) {
		// Create a comment to delete
		params := services.CreateCommentParams{
			ItemID:      env.ItemID,
			AuthorID:    env.UserID,
			Content:     "Comment to delete",
			IsPrivate:   false,
			ActorUserID: env.UserID,
		}
		created, err := service.Create(params)
		if err != nil {
			t.Fatalf("Failed to create test comment: %v", err)
		}

		err = service.Delete(int(created.CommentID))
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify comment was deleted
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM comments WHERE id = ?", created.CommentID).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to verify deletion: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected 0 comments, got %d", count)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		err := service.Delete(99999)
		if err == nil {
			t.Error("Expected error for non-existent comment")
		}
	})
}

func TestCommentService_GetByItemID(t *testing.T) {
	db := createCommentTestDB(t)

	service := services.NewCommentService(db)
	env := setupCommentTestEnv(t, db)

	// Create multiple comments
	for i := 1; i <= 3; i++ {
		params := services.CreateCommentParams{
			ItemID:      env.ItemID,
			AuthorID:    env.UserID,
			Content:     "Comment " + string(rune('0'+i)),
			IsPrivate:   false,
			ActorUserID: env.UserID,
		}
		_, err := service.Create(params)
		if err != nil {
			t.Fatalf("Failed to create test comment: %v", err)
		}
	}

	t.Run("ReturnsAllComments", func(t *testing.T) {
		comments, err := service.GetByItemID(env.ItemID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(comments) != 3 {
			t.Errorf("Expected 3 comments, got %d", len(comments))
		}
	})

	t.Run("EmptyForNoComments", func(t *testing.T) {
		// Create a new item without comments via the production create path
		f := factory.NewTestFactory(db)
		newItemID, err := f.CreateItem(factory.CreateItemOpts{
			WorkspaceID: env.WorkspaceID,
			Title:       "Item without comments",
		})
		if err != nil {
			t.Fatalf("Failed to create item: %v", err)
		}

		comments, err := service.GetByItemID(newItemID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(comments) != 0 {
			t.Errorf("Expected 0 comments, got %d", len(comments))
		}
	})
}

func TestCommentService_GetWorkspaceIDForComment(t *testing.T) {
	db := createCommentTestDB(t)

	service := services.NewCommentService(db)
	env := setupCommentTestEnv(t, db)

	// Create a comment
	params := services.CreateCommentParams{
		ItemID:      env.ItemID,
		AuthorID:    env.UserID,
		Content:     "Test comment",
		IsPrivate:   false,
		ActorUserID: env.UserID,
	}
	created, err := service.Create(params)
	if err != nil {
		t.Fatalf("Failed to create test comment: %v", err)
	}

	t.Run("Success", func(t *testing.T) {
		workspaceID, err := service.GetWorkspaceIDForComment(int(created.CommentID))
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if workspaceID != env.WorkspaceID {
			t.Errorf("Expected workspace ID %d, got %d", env.WorkspaceID, workspaceID)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		_, err := service.GetWorkspaceIDForComment(99999)
		if err == nil {
			t.Error("Expected error for non-existent comment")
		}
	})
}

func TestCommentService_GetAuthorID(t *testing.T) {
	db := createCommentTestDB(t)

	service := services.NewCommentService(db)
	env := setupCommentTestEnv(t, db)

	// Create a comment
	params := services.CreateCommentParams{
		ItemID:      env.ItemID,
		AuthorID:    env.UserID,
		Content:     "Test comment",
		IsPrivate:   false,
		ActorUserID: env.UserID,
	}
	created, err := service.Create(params)
	if err != nil {
		t.Fatalf("Failed to create test comment: %v", err)
	}

	t.Run("Success", func(t *testing.T) {
		authorID, err := service.GetAuthorID(int(created.CommentID))
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if authorID == nil {
			t.Fatalf("Expected non-nil author ID for internally-authored comment")
		}
		if *authorID != env.UserID {
			t.Errorf("Expected author ID %d, got %d", env.UserID, *authorID)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		_, err := service.GetAuthorID(99999)
		if err == nil {
			t.Error("Expected error for non-existent comment")
		}
	})
}

// mockEmailReplyHandler implements services.EmailReplyHandler for testing.
type mockEmailReplyHandler struct {
	calls []services.HandleCommentParams
	err   error
}

func (m *mockEmailReplyHandler) HandleCommentCreated(p services.HandleCommentParams) error {
	m.calls = append(m.calls, p)
	return m.err
}

func TestCommentService_CreateWithPortalCustomerID(t *testing.T) {
	db := createCommentTestDB(t)
	service := services.NewCommentService(db)
	env := setupCommentTestEnv(t, db)

	// Create a portal customer
	pcID := testutils.InsertID(t, db, `
		INSERT INTO portal_customers (name, email) VALUES ('Portal User', 'portal@example.com')
	`)

	t.Run("PortalCustomerWithoutLinkedUser", func(t *testing.T) {
		params := services.CreateCommentParams{
			ItemID:           env.ItemID,
			AuthorID:         0,
			PortalCustomerID: &pcID,
			Content:          "Comment from portal customer",
			ActorUserID:      0,
		}

		result, err := service.Create(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if result.CommentID == 0 {
			t.Error("Expected non-zero comment ID")
		}

		// Verify portal_customer_id is set
		var portalCustomerID int
		err = db.QueryRow("SELECT portal_customer_id FROM comments WHERE id = ?", result.CommentID).Scan(&portalCustomerID)
		if err != nil {
			t.Fatalf("Failed to query comment: %v", err)
		}
		if portalCustomerID != pcID {
			t.Errorf("Expected portal_customer_id %d, got %d", pcID, portalCustomerID)
		}
	})

	t.Run("PortalCustomerWithLinkedUser", func(t *testing.T) {
		params := services.CreateCommentParams{
			ItemID:           env.ItemID,
			AuthorID:         env.UserID,
			PortalCustomerID: &pcID,
			Content:          "Comment from linked portal customer",
			ActorUserID:      env.UserID,
		}

		result, err := service.Create(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// When AuthorID is set, the internal user path is used (author_id column)
		var authorID int
		err = db.QueryRow("SELECT author_id FROM comments WHERE id = ?", result.CommentID).Scan(&authorID)
		if err != nil {
			t.Fatalf("Failed to query comment: %v", err)
		}
		if authorID != env.UserID {
			t.Errorf("Expected author_id %d, got %d", env.UserID, authorID)
		}
	})
}

func TestCommentService_CreateCallsEmailReplyService(t *testing.T) {
	db := createCommentTestDB(t)
	service := services.NewCommentService(db)
	env := setupCommentTestEnv(t, db)

	mock := &mockEmailReplyHandler{}
	service.SetEmailReplyService(mock)

	t.Run("InternalUserComment", func(t *testing.T) {
		mock.calls = nil
		params := services.CreateCommentParams{
			ItemID:      env.ItemID,
			AuthorID:    env.UserID,
			Content:     "Internal reply",
			ActorUserID: env.UserID,
		}

		_, err := service.Create(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(mock.calls) != 1 {
			t.Fatalf("Expected 1 call to HandleCommentCreated, got %d", len(mock.calls))
		}
		call := mock.calls[0]
		if call.AuthorID != env.UserID {
			t.Errorf("Expected AuthorID %d, got %d", env.UserID, call.AuthorID)
		}
		if call.ItemID != env.ItemID {
			t.Errorf("Expected ItemID %d, got %d", env.ItemID, call.ItemID)
		}
		if call.PortalCustomerID != nil {
			t.Error("Expected PortalCustomerID to be nil for internal user")
		}
	})

	t.Run("PortalCustomerComment", func(t *testing.T) {
		mock.calls = nil

		// Create portal customer for this test
		pcID := testutils.InsertID(t, db, `
			INSERT INTO portal_customers (name, email) VALUES ('Another Customer', 'another@example.com')
		`)

		params := services.CreateCommentParams{
			ItemID:           env.ItemID,
			AuthorID:         0,
			PortalCustomerID: &pcID,
			Content:          "Customer comment",
			ActorUserID:      0,
		}

		_, err := service.Create(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(mock.calls) != 1 {
			t.Fatalf("Expected 1 call to HandleCommentCreated, got %d", len(mock.calls))
		}
		call := mock.calls[0]
		if call.PortalCustomerID == nil {
			t.Fatal("Expected PortalCustomerID to be set")
		}
		if *call.PortalCustomerID != pcID {
			t.Errorf("Expected PortalCustomerID %d, got %d", pcID, *call.PortalCustomerID)
		}
	})
}

// TestCommentService_Create_AutoSubscribesCommenter verifies that posting a
// comment auto-subscribes the actor as an item watcher (so they're notified of
// later replies), and that this is skipped for portal customers (ActorUserID 0)
// and when notifications are suppressed.
func TestCommentService_Create_AutoSubscribesCommenter(t *testing.T) {
	db := createCommentTestDB(t)
	env := setupCommentTestEnv(t, db)

	actConfig := services.DefaultActivityTrackerConfig()
	actConfig.FlushInterval = 1 * time.Hour
	actConfig.ImmediateFlushActivity = false
	tracker, err := services.NewActivityTracker(db, actConfig)
	if err != nil {
		t.Fatalf("Failed to create activity tracker: %v", err)
	}
	t.Cleanup(func() { tracker.Close() })

	service := services.NewCommentService(db)
	service.SetActivityTracker(tracker)

	watchActive := func(userID, itemID int) bool {
		t.Helper()
		var active bool
		err := db.QueryRow(
			"SELECT is_active FROM item_watches WHERE user_id = ? AND item_id = ?",
			userID, itemID,
		).Scan(&active)
		if err != nil {
			return false
		}
		return active
	}

	t.Run("SubscribesCommenter", func(t *testing.T) {
		if watchActive(env.UserID, env.ItemID) {
			t.Fatal("Precondition failed: user already watching the item")
		}
		_, err := service.Create(services.CreateCommentParams{
			ItemID:      env.ItemID,
			AuthorID:    env.UserID,
			Content:     "First comment subscribes me",
			ActorUserID: env.UserID,
		})
		if err != nil {
			t.Fatalf("Create returned error: %v", err)
		}
		if !watchActive(env.UserID, env.ItemID) {
			t.Error("Expected commenter to be auto-subscribed as an active watcher")
		}
	})

	t.Run("SuppressNotificationsSkipsSubscribe", func(t *testing.T) {
		// Unwatch first so we can detect that suppressed comments don't re-add it.
		if err := repository.NewItemRepository(db).Unwatch(env.UserID, env.ItemID); err != nil {
			t.Fatalf("Unwatch failed: %v", err)
		}
		_, err := service.Create(services.CreateCommentParams{
			ItemID:                env.ItemID,
			AuthorID:              env.UserID,
			Content:               "Plugin/import comment",
			ActorUserID:           env.UserID,
			SuppressNotifications: true,
		})
		if err != nil {
			t.Fatalf("Create returned error: %v", err)
		}
		if watchActive(env.UserID, env.ItemID) {
			t.Error("Expected suppressed comment NOT to auto-subscribe the commenter")
		}
	})

	t.Run("PortalCustomerNotSubscribed", func(t *testing.T) {
		// ActorUserID 0 (portal customer) has no user row to watch with.
		_, err := service.Create(services.CreateCommentParams{
			ItemID:      env.ItemID,
			AuthorID:    env.UserID,
			Content:     "Portal-style comment",
			ActorUserID: 0,
		})
		if err != nil {
			t.Fatalf("Create returned error: %v", err)
		}
		if watchActive(0, env.ItemID) {
			t.Error("Expected no watch row for ActorUserID 0")
		}
	})
}

// Remove unused import warning
var _ = time.Now
