package services

import (
	"fmt"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// mentionTestEnv contains test data for mention service tests
type mentionTestEnv struct {
	WorkspaceID int
	ItemID      int
	UserID1     int
	UserID2     int
	UserID3     int
	CommentID   int
}

// createMentionTestDB creates a test database for mention service tests
func createMentionTestDB(t *testing.T) database.Database {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	return tdb.DB
}

// setupMentionTestEnv creates test data for mention service tests
func setupMentionTestEnv(t *testing.T, db database.Database) mentionTestEnv {
	t.Helper()

	// Create workspace
	workspaceID := testutils.InsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal, created_at, updated_at)
		VALUES ('Mention Test Workspace', 'MNT', 'Test workspace', TRUE, FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)

	// Create test users
	userID1 := testutils.InsertID(t, db, `
		INSERT INTO users (username, first_name, last_name, email, is_active, created_at, updated_at)
		VALUES ('johndoe', 'John', 'Doe', 'john@example.com', TRUE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)

	userID2 := testutils.InsertID(t, db, `
		INSERT INTO users (username, first_name, last_name, email, is_active, created_at, updated_at)
		VALUES ('janedoe', 'Jane', 'Doe', 'jane@example.com', TRUE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)

	userID3 := testutils.InsertID(t, db, `
		INSERT INTO users (username, first_name, last_name, email, is_active, created_at, updated_at)
		VALUES ('inactive_user', 'Inactive', 'User', 'inactive@example.com', FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)

	// Get a status ID for the item
	var statusID int
	err := db.QueryRow("SELECT id FROM statuses LIMIT 1").Scan(&statusID)
	if err != nil {
		t.Fatalf("Failed to get status ID: %v", err)
	}

	// Create test item through the production create path
	createdItemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: workspaceID,
		Title:       "Test Item",
		Description: "Item description",
		StatusID:    &statusID,
	})
	if err != nil {
		t.Fatalf("Failed to create item: %v", err)
	}
	itemID := int(createdItemID)

	// Create test comment
	commentID := testutils.InsertID(t, db, `
		INSERT INTO comments (item_id, author_id, content, created_at, updated_at)
		VALUES (?, ?, 'Test comment', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, itemID, userID1)

	return mentionTestEnv{
		WorkspaceID: workspaceID,
		ItemID:      itemID,
		UserID1:     userID1,
		UserID2:     userID2,
		UserID3:     userID3,
		CommentID:   commentID,
	}
}

func TestMentionService_ExtractMentionRefs(t *testing.T) {
	db := createMentionTestDB(t)

	service := NewMentionService(db, nil, nil)

	t.Run("ExtractUsername", func(t *testing.T) {
		content := "Hello @johndoe, how are you?"
		refs := service.ExtractMentionRefs(content)

		if len(refs) != 1 {
			t.Fatalf("Expected 1 ref, got %d", len(refs))
		}
		if refs[0].UserID != 0 || refs[0].Identifier != "johndoe" {
			t.Errorf("Expected legacy ref 'johndoe', got %+v", refs[0])
		}
	})

	t.Run("ExtractQuotedDisplayName", func(t *testing.T) {
		content := "Hello @\"John Doe\", how are you?"
		refs := service.ExtractMentionRefs(content)

		if len(refs) != 1 {
			t.Fatalf("Expected 1 ref, got %d", len(refs))
		}
		if refs[0].UserID != 0 || refs[0].Identifier != "John Doe" {
			t.Errorf("Expected legacy ref 'John Doe', got %+v", refs[0])
		}
	})

	t.Run("ExtractIdToken", func(t *testing.T) {
		content := "Hello @{John Doe#42}, how are you?"
		refs := service.ExtractMentionRefs(content)

		if len(refs) != 1 {
			t.Fatalf("Expected 1 ref, got %d", len(refs))
		}
		if refs[0].UserID != 42 {
			t.Errorf("Expected user id 42, got %d", refs[0].UserID)
		}
		if refs[0].Identifier != "John Doe" {
			t.Errorf("Expected display name 'John Doe', got %q", refs[0].Identifier)
		}
	})

	t.Run("ExtractIdTokenWithHashInName", func(t *testing.T) {
		content := "Hi @{Ticket #1 Owner#7}!"
		refs := service.ExtractMentionRefs(content)

		if len(refs) != 1 {
			t.Fatalf("Expected 1 ref, got %d", len(refs))
		}
		if refs[0].UserID != 7 || refs[0].Identifier != "Ticket #1 Owner" {
			t.Errorf("Expected id 7 and name 'Ticket #1 Owner', got %+v", refs[0])
		}
	})

	t.Run("DeduplicatesById", func(t *testing.T) {
		content := "@{John Doe#42} and @{J. Doe#42}"
		refs := service.ExtractMentionRefs(content)

		if len(refs) != 1 {
			t.Fatalf("Expected 1 ref (deduplicated by id), got %d", len(refs))
		}
	})

	t.Run("ExtractMultipleMentions", func(t *testing.T) {
		content := "Hello @johndoe and @janedoe!"
		refs := service.ExtractMentionRefs(content)

		if len(refs) != 2 {
			t.Fatalf("Expected 2 refs, got %d", len(refs))
		}
	})

	t.Run("DeduplicateMentions", func(t *testing.T) {
		content := "Hello @johndoe, @johndoe again!"
		refs := service.ExtractMentionRefs(content)

		if len(refs) != 1 {
			t.Fatalf("Expected 1 ref (deduplicated), got %d", len(refs))
		}
	})

	t.Run("MixedFormats", func(t *testing.T) {
		content := "Hello @johndoe and @\"Jane Doe\"!"
		refs := service.ExtractMentionRefs(content)

		if len(refs) != 2 {
			t.Fatalf("Expected 2 refs, got %d", len(refs))
		}
	})

	t.Run("NoMentions", func(t *testing.T) {
		content := "Hello, how are you?"
		refs := service.ExtractMentionRefs(content)

		if len(refs) != 0 {
			t.Errorf("Expected 0 refs, got %d", len(refs))
		}
	})

	t.Run("EmptyContent", func(t *testing.T) {
		refs := service.ExtractMentionRefs("")

		if len(refs) != 0 {
			t.Errorf("Expected 0 refs, got %d", len(refs))
		}
	})

	t.Run("UsernameWithDots", func(t *testing.T) {
		refs := service.ExtractMentionRefs("Hello @john.doe.jr!")

		if len(refs) != 1 {
			t.Fatalf("Expected 1 ref, got %d", len(refs))
		}
		if refs[0].Identifier != "john.doe.jr" {
			t.Errorf("Expected 'john.doe.jr', got '%s'", refs[0].Identifier)
		}
	})

	t.Run("UsernameWithUnderscores", func(t *testing.T) {
		refs := service.ExtractMentionRefs("Hello @john_doe!")

		if len(refs) != 1 {
			t.Fatalf("Expected 1 ref, got %d", len(refs))
		}
		if refs[0].Identifier != "john_doe" {
			t.Errorf("Expected 'john_doe', got '%s'", refs[0].Identifier)
		}
	})

	t.Run("UsernameWithDashes", func(t *testing.T) {
		refs := service.ExtractMentionRefs("Hello @john-doe!")

		if len(refs) != 1 {
			t.Fatalf("Expected 1 ref, got %d", len(refs))
		}
		if refs[0].Identifier != "john-doe" {
			t.Errorf("Expected 'john-doe', got '%s'", refs[0].Identifier)
		}
	})
}

// ensureDuplicateDisplayNameUsers creates (or reuses) two active users that
// share the display name "Dup Person" and returns their ids. Idempotent so the
// id and ambiguity subtests stay order-independent.
func ensureDuplicateDisplayNameUsers(t *testing.T, db database.Database) (int, int) {
	t.Helper()

	ensure := func(username, email string) int {
		var id int
		err := db.QueryRow(`SELECT id FROM users WHERE username = ?`, username).Scan(&id)
		if err == nil {
			return id
		}
		return testutils.InsertID(t, db, `
			INSERT INTO users (username, first_name, last_name, email, is_active, created_at, updated_at)
			VALUES (?, 'Dup', 'Person', ?, TRUE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, username, email)
	}

	return ensure("dup.person.a", "dup.person.a@example.com"), ensure("dup.person.b", "dup.person.b@example.com")
}

func TestMentionService_ProcessMentions(t *testing.T) {
	db := createMentionTestDB(t)

	service := NewMentionService(db, nil, nil)
	env := setupMentionTestEnv(t, db)

	t.Run("CreatesMention", func(t *testing.T) {
		params := ProcessMentionsParams{
			SourceType:  "comment",
			SourceID:    env.CommentID,
			Content:     "Hello @johndoe!",
			ItemID:      env.ItemID,
			WorkspaceID: env.WorkspaceID,
			ActorUserID: env.UserID2, // Jane mentions John
		}

		err := service.ProcessMentions(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify mention was created
		var count int
		err = db.QueryRow(`
			SELECT COUNT(*) FROM mentions
			WHERE source_type = ? AND source_id = ? AND mentioned_user_id = ?
		`, "comment", env.CommentID, env.UserID1).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query mentions: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 mention, got %d", count)
		}
	})

	t.Run("SkipsSelfMention", func(t *testing.T) {
		// Clean up previous test
		db.Exec("DELETE FROM mentions")

		params := ProcessMentionsParams{
			SourceType:  "comment",
			SourceID:    env.CommentID + 100, // Different source
			Content:     "Hello @johndoe!",
			ItemID:      env.ItemID,
			WorkspaceID: env.WorkspaceID,
			ActorUserID: env.UserID1, // John mentions himself
		}

		err := service.ProcessMentions(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify self-mention was not created
		var count int
		err = db.QueryRow(`
			SELECT COUNT(*) FROM mentions
			WHERE source_type = ? AND source_id = ? AND mentioned_user_id = ?
		`, "comment", env.CommentID+100, env.UserID1).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query mentions: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected 0 mentions (self-mention skipped), got %d", count)
		}
	})

	t.Run("SkipsInactiveUser", func(t *testing.T) {
		// Clean up previous test
		db.Exec("DELETE FROM mentions")

		params := ProcessMentionsParams{
			SourceType:  "comment",
			SourceID:    env.CommentID + 200, // Different source
			Content:     "Hello @inactive_user!",
			ItemID:      env.ItemID,
			WorkspaceID: env.WorkspaceID,
			ActorUserID: env.UserID1,
		}

		err := service.ProcessMentions(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify inactive user mention was not created
		var count int
		err = db.QueryRow(`
			SELECT COUNT(*) FROM mentions
			WHERE source_type = ? AND source_id = ? AND mentioned_user_id = ?
		`, "comment", env.CommentID+200, env.UserID3).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query mentions: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected 0 mentions (inactive user skipped), got %d", count)
		}
	})

	t.Run("SkipsUnknownUser", func(t *testing.T) {
		// Clean up previous test
		db.Exec("DELETE FROM mentions")

		params := ProcessMentionsParams{
			SourceType:  "comment",
			SourceID:    env.CommentID + 300, // Different source
			Content:     "Hello @nonexistentuser!",
			ItemID:      env.ItemID,
			WorkspaceID: env.WorkspaceID,
			ActorUserID: env.UserID1,
		}

		err := service.ProcessMentions(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify no mentions were created
		var count int
		err = db.QueryRow(`
			SELECT COUNT(*) FROM mentions WHERE source_type = ? AND source_id = ?
		`, "comment", env.CommentID+300).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query mentions: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected 0 mentions, got %d", count)
		}
	})

	t.Run("RemovesMentionOnUpdate", func(t *testing.T) {
		// Clean up
		db.Exec("DELETE FROM mentions")

		sourceID := env.CommentID + 400

		// First, create a mention
		params := ProcessMentionsParams{
			SourceType:  "comment",
			SourceID:    sourceID,
			Content:     "Hello @johndoe!",
			ItemID:      env.ItemID,
			WorkspaceID: env.WorkspaceID,
			ActorUserID: env.UserID2,
		}
		err := service.ProcessMentions(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify mention exists
		var count int
		err = db.QueryRow(`SELECT COUNT(*) FROM mentions WHERE source_id = ?`, sourceID).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query mentions: %v", err)
		}
		if count != 1 {
			t.Fatalf("Expected 1 mention, got %d", count)
		}

		// Now update with content that has no mentions
		params.Content = "Hello everyone!"
		err = service.ProcessMentions(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify mention was removed
		err = db.QueryRow(`SELECT COUNT(*) FROM mentions WHERE source_id = ?`, sourceID).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query mentions: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected 0 mentions after update, got %d", count)
		}
	})

	t.Run("HandlesDisplayNameMention", func(t *testing.T) {
		// Clean up
		db.Exec("DELETE FROM mentions")

		sourceID := env.CommentID + 500

		params := ProcessMentionsParams{
			SourceType:  "comment",
			SourceID:    sourceID,
			Content:     "Hello @\"John Doe\"!",
			ItemID:      env.ItemID,
			WorkspaceID: env.WorkspaceID,
			ActorUserID: env.UserID2,
		}

		err := service.ProcessMentions(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify mention was created (matching by display name)
		var count int
		err = db.QueryRow(`
			SELECT COUNT(*) FROM mentions
			WHERE source_type = ? AND source_id = ? AND mentioned_user_id = ?
		`, "comment", sourceID, env.UserID1).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query mentions: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 mention from display name, got %d", count)
		}
	})

	t.Run("ResolvesEmbeddedIdWhenDisplayNameIsDuplicated", func(t *testing.T) {
		db.Exec("DELETE FROM mentions")

		dupA, _ := ensureDuplicateDisplayNameUsers(t, db)
		sourceID := env.CommentID + 800

		params := ProcessMentionsParams{
			SourceType:  "comment",
			SourceID:    sourceID,
			Content:     fmt.Sprintf("Hello @{Dup Person#%d}!", dupA),
			ItemID:      env.ItemID,
			WorkspaceID: env.WorkspaceID,
			ActorUserID: env.UserID2,
		}

		if err := service.ProcessMentions(params); err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		var count int
		if err := db.QueryRow(`
			SELECT COUNT(*) FROM mentions
			WHERE source_type = ? AND source_id = ? AND mentioned_user_id = ?
		`, "comment", sourceID, dupA).Scan(&count); err != nil {
			t.Fatalf("Failed to query mentions: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected the id-embedded user to be mentioned exactly once, got %d", count)
		}
	})

	t.Run("SkipsAmbiguousDisplayNameMention", func(t *testing.T) {
		db.Exec("DELETE FROM mentions")

		ensureDuplicateDisplayNameUsers(t, db)
		sourceID := env.CommentID + 900

		params := ProcessMentionsParams{
			SourceType:  "comment",
			SourceID:    sourceID,
			Content:     "Hello @\"Dup Person\"!",
			ItemID:      env.ItemID,
			WorkspaceID: env.WorkspaceID,
			ActorUserID: env.UserID2,
		}

		if err := service.ProcessMentions(params); err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		var count int
		if err := db.QueryRow(`
			SELECT COUNT(*) FROM mentions WHERE source_type = ? AND source_id = ?
		`, "comment", sourceID).Scan(&count); err != nil {
			t.Fatalf("Failed to query mentions: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected no mention for an ambiguous display name, got %d", count)
		}
	})

	t.Run("SkipsIdTokenForInactiveUser", func(t *testing.T) {
		db.Exec("DELETE FROM mentions")

		sourceID := env.CommentID + 1000
		params := ProcessMentionsParams{
			SourceType:  "comment",
			SourceID:    sourceID,
			Content:     fmt.Sprintf("Hello @{Inactive User#%d}!", env.UserID3),
			ItemID:      env.ItemID,
			WorkspaceID: env.WorkspaceID,
			ActorUserID: env.UserID2,
		}

		if err := service.ProcessMentions(params); err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM mentions WHERE source_id = ?`, sourceID).Scan(&count); err != nil {
			t.Fatalf("Failed to query mentions: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected no mention for an inactive user, got %d", count)
		}
	})

	t.Run("SkipsIdTokenForUnknownUser", func(t *testing.T) {
		db.Exec("DELETE FROM mentions")

		sourceID := env.CommentID + 1100
		params := ProcessMentionsParams{
			SourceType:  "comment",
			SourceID:    sourceID,
			Content:     "Hello @{Nobody Here#99999}!",
			ItemID:      env.ItemID,
			WorkspaceID: env.WorkspaceID,
			ActorUserID: env.UserID2,
		}

		if err := service.ProcessMentions(params); err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM mentions WHERE source_id = ?`, sourceID).Scan(&count); err != nil {
			t.Fatalf("Failed to query mentions: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected no mention for an unknown id, got %d", count)
		}
	})
}

func TestMentionService_DeleteMentionsForSource(t *testing.T) {
	db := createMentionTestDB(t)

	service := NewMentionService(db, nil, nil)
	env := setupMentionTestEnv(t, db)

	t.Run("DeletesAllMentionsForSource", func(t *testing.T) {
		sourceID := env.CommentID + 600

		// Create some mentions
		params := ProcessMentionsParams{
			SourceType:  "comment",
			SourceID:    sourceID,
			Content:     "Hello @johndoe and @janedoe!",
			ItemID:      env.ItemID,
			WorkspaceID: env.WorkspaceID,
			ActorUserID: env.UserID3, // Third user mentioning others (UserID3 is inactive so won't be mentioned back)
		}

		// Note: UserID3 is inactive, but they can still mention others
		// We need to make this work differently - use a fourth user or change the test
		// Actually, let's use the janedoe user to mention johndoe
		params.ActorUserID = env.UserID2 // Jane mentioning John only
		params.Content = "Hello @johndoe!"

		err := service.ProcessMentions(params)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify mentions exist
		var count int
		err = db.QueryRow(`SELECT COUNT(*) FROM mentions WHERE source_type = ? AND source_id = ?`, "comment", sourceID).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query mentions: %v", err)
		}
		if count == 0 {
			t.Fatal("Expected at least 1 mention")
		}

		// Delete all mentions for this source
		err = service.DeleteMentionsForSource("comment", sourceID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify mentions were deleted
		err = db.QueryRow(`SELECT COUNT(*) FROM mentions WHERE source_type = ? AND source_id = ?`, "comment", sourceID).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query mentions: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected 0 mentions after delete, got %d", count)
		}
	})

	t.Run("DoesNotDeleteOtherSources", func(t *testing.T) {
		// Clean up
		db.Exec("DELETE FROM mentions")

		sourceID1 := env.CommentID + 700
		sourceID2 := env.CommentID + 701

		// Create mentions for two different sources
		params1 := ProcessMentionsParams{
			SourceType:  "comment",
			SourceID:    sourceID1,
			Content:     "Hello @johndoe!",
			ItemID:      env.ItemID,
			WorkspaceID: env.WorkspaceID,
			ActorUserID: env.UserID2,
		}
		err := service.ProcessMentions(params1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		params2 := ProcessMentionsParams{
			SourceType:  "comment",
			SourceID:    sourceID2,
			Content:     "Hello @johndoe!",
			ItemID:      env.ItemID,
			WorkspaceID: env.WorkspaceID,
			ActorUserID: env.UserID2,
		}
		err = service.ProcessMentions(params2)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Delete mentions for first source only
		err = service.DeleteMentionsForSource("comment", sourceID1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify second source still has mentions
		var count int
		err = db.QueryRow(`SELECT COUNT(*) FROM mentions WHERE source_type = ? AND source_id = ?`, "comment", sourceID2).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query mentions: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 mention for other source, got %d", count)
		}
	})

	t.Run("HandlesNonExistentSource", func(t *testing.T) {
		err := service.DeleteMentionsForSource("comment", 99999)
		if err != nil {
			t.Errorf("Expected no error for non-existent source, got: %v", err)
		}
	})
}

func TestMentionPattern(t *testing.T) {
	t.Run("MatchesSimpleUsername", func(t *testing.T) {
		matches := MentionPattern.FindAllStringSubmatch("@johndoe", -1)
		if len(matches) != 1 {
			t.Fatalf("Expected 1 match, got %d", len(matches))
		}
		if matches[0][3] != "johndoe" {
			t.Errorf("Expected 'johndoe', got '%s'", matches[0][3])
		}
	})

	t.Run("MatchesQuotedName", func(t *testing.T) {
		matches := MentionPattern.FindAllStringSubmatch("@\"John Doe\"", -1)
		if len(matches) != 1 {
			t.Fatalf("Expected 1 match, got %d", len(matches))
		}
		if matches[0][4] != "John Doe" {
			t.Errorf("Expected 'John Doe', got '%s'", matches[0][4])
		}
	})

	t.Run("MatchesAtStartOfString", func(t *testing.T) {
		matches := MentionPattern.FindAllStringSubmatch("@johndoe hello", -1)
		if len(matches) != 1 {
			t.Fatalf("Expected 1 match, got %d", len(matches))
		}
	})

	t.Run("MatchesAtEndOfString", func(t *testing.T) {
		matches := MentionPattern.FindAllStringSubmatch("hello @johndoe", -1)
		if len(matches) != 1 {
			t.Fatalf("Expected 1 match, got %d", len(matches))
		}
	})

	t.Run("MatchesInMiddleOfString", func(t *testing.T) {
		matches := MentionPattern.FindAllStringSubmatch("hello @johndoe world", -1)
		if len(matches) != 1 {
			t.Fatalf("Expected 1 match, got %d", len(matches))
		}
	})

	t.Run("DoesNotMatchEmail", func(t *testing.T) {
		content := "contact me at email@example.com"
		matches := MentionPattern.FindAllStringSubmatch(content, -1)
		if len(matches) != 0 {
			t.Fatalf("Expected 0 matches for email address, got %d", len(matches))
		}
	})

	t.Run("DoesNotMatchEmailWithDottedLocalPart", func(t *testing.T) {
		content := "contact user.name@example.com"
		matches := MentionPattern.FindAllStringSubmatch(content, -1)
		if len(matches) != 0 {
			t.Fatalf("Expected 0 matches for email address, got %d", len(matches))
		}
	})

	t.Run("DoesNotMatchEmailWithPlusTag", func(t *testing.T) {
		// user+tag@example.com — the + is not in [a-zA-Z0-9.] so @example.com
		// would start after a non-alnum char, but the local part "user+tag" means
		// the char before @ is 'g' which IS alphanumeric → should not match.
		// Actually '+' is not in [a-zA-Z0-9.], so the char before @ is 'g'
		// Wait: "user+tag@example.com" → char before @ is 'g' → alphanumeric → no match
		content := "contact user+tag@example.com"
		matches := MentionPattern.FindAllStringSubmatch(content, -1)
		if len(matches) != 0 {
			t.Fatalf("Expected 0 matches for email with plus tag, got %d", len(matches))
		}
	})

	t.Run("MatchesMentionInParentheses", func(t *testing.T) {
		content := "(@johndoe)"
		matches := MentionPattern.FindAllStringSubmatch(content, -1)
		if len(matches) != 1 {
			t.Fatalf("Expected 1 match, got %d", len(matches))
		}
		if matches[0][3] != "johndoe" {
			t.Errorf("Expected 'johndoe', got '%s'", matches[0][3])
		}
	})

	t.Run("MatchesIdToken", func(t *testing.T) {
		matches := MentionPattern.FindAllStringSubmatch("hello @{John Doe#42} world", -1)
		if len(matches) != 1 {
			t.Fatalf("Expected 1 match, got %d", len(matches))
		}
		if matches[0][1] != "John Doe" {
			t.Errorf("Expected 'John Doe', got '%s'", matches[0][1])
		}
		if matches[0][2] != "42" {
			t.Errorf("Expected id '42', got '%s'", matches[0][2])
		}
	})

	t.Run("MatchesIdTokenAtStart", func(t *testing.T) {
		matches := MentionPattern.FindAllStringSubmatch("@{John Doe#7} hello", -1)
		if len(matches) != 1 {
			t.Fatalf("Expected 1 match, got %d", len(matches))
		}
	})

	t.Run("DoesNotMatchEmptyIdToken", func(t *testing.T) {
		matches := MentionPattern.FindAllStringSubmatch("hello @{John Doe} world", -1)
		if len(matches) != 0 {
			t.Fatalf("Expected 0 matches without an id, got %d", len(matches))
		}
	})

	t.Run("MatchesMentionAfterNewline", func(t *testing.T) {
		content := "hello\n@johndoe"
		matches := MentionPattern.FindAllStringSubmatch(content, -1)
		if len(matches) != 1 {
			t.Fatalf("Expected 1 match, got %d", len(matches))
		}
		if matches[0][3] != "johndoe" {
			t.Errorf("Expected 'johndoe', got '%s'", matches[0][3])
		}
	})
}
