//go:build test

package services_test

import (
	"errors"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

type reactionEnv struct {
	db        database.Database
	service   *services.CommentService
	commentID int
	alice     int
	bob       int
}

// newReactionEnv builds a workspace item with one comment by alice and a
// second user, bob, whose empty display name exercises the username fallback.
func newReactionEnv(t *testing.T) reactionEnv {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	db := tdb.GetDatabase()

	f := factory.NewTestFactory(db)
	alice, err := f.CreateUser(&factory.CreateUserOpts{
		Email: "alice@example.com", Username: "alice", FirstName: "Alice", LastName: "Doe",
	})
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := f.CreateUser(&factory.CreateUserOpts{
		Email: "bob@example.com", Username: "bob", FirstName: " ", LastName: " ",
	})
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}
	workspaceID, err := f.CreateWorkspace(factory.CreateWorkspaceOpts{Name: "Reactions", CreatorID: alice})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	itemID, err := f.CreateItem(factory.CreateItemOpts{WorkspaceID: workspaceID, CreatorID: &alice})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	commentID, err := f.CreateComment(factory.CreateCommentOpts{ItemID: itemID, AuthorID: alice, Content: "Please review"})
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}

	return reactionEnv{db: db, service: services.NewCommentService(db), commentID: commentID, alice: alice, bob: bob}
}

func TestCommentReactionsGroupInKeyOrderAndAreIdempotent(t *testing.T) {
	env := newReactionEnv(t)

	for _, step := range []struct {
		user     int
		reaction string
	}{{env.bob, "eyes"}, {env.alice, "thumbs_up"}, {env.bob, "thumbs_up"}, {env.bob, "thumbs_up"}} {
		if _, err := env.service.AddReaction(env.commentID, step.user, step.reaction); err != nil {
			t.Fatalf("AddReaction(%d, %q) error = %v", step.user, step.reaction, err)
		}
	}

	got, err := env.service.ReactionsForComment(env.commentID)
	if err != nil {
		t.Fatalf("ReactionsForComment error = %v", err)
	}
	if len(got) != 2 || got[0].Reaction != "thumbs_up" || got[1].Reaction != "eyes" {
		t.Fatalf("reactions = %+v, want thumbs_up then eyes", got)
	}
	if got[0].Count != 2 || len(got[0].UserIDs) != 2 ||
		got[0].UserIDs[0] != env.alice || got[0].UserNames[0] != "Alice Doe" ||
		got[0].UserIDs[1] != env.bob || got[0].UserNames[1] != "bob" {
		t.Fatalf("thumbs_up = %+v, want Alice Doe then bob once each", got[0])
	}
	if got[1].Count != 1 || got[1].UserIDs[0] != env.bob {
		t.Fatalf("eyes = %+v, want bob once", got[1])
	}
}

func TestCommentReactionsRemoveIsIdempotentAndAttachSkipsApprovalRows(t *testing.T) {
	env := newReactionEnv(t)
	if _, err := env.service.AddReaction(env.commentID, env.alice, "thumbs_up"); err != nil {
		t.Fatalf("AddReaction error = %v", err)
	}
	if _, err := env.service.AddReaction(env.commentID, env.bob, "thumbs_up"); err != nil {
		t.Fatalf("AddReaction error = %v", err)
	}
	if _, err := env.service.AddReaction(env.commentID, env.bob, "eyes"); err != nil {
		t.Fatalf("AddReaction error = %v", err)
	}

	if err := env.service.RemoveReaction(env.commentID, env.bob, "thumbs_up"); err != nil {
		t.Fatalf("RemoveReaction error = %v", err)
	}
	if err := env.service.RemoveReaction(env.commentID, env.bob, "thumbs_up"); err != nil {
		t.Fatalf("removing a missing reaction should be a no-op, got %v", err)
	}

	comments := []models.Comment{{ID: env.commentID, Source: "human"}, {ID: -1, Source: "approval"}}
	if err := env.service.AttachReactions(comments); err != nil {
		t.Fatalf("AttachReactions error = %v", err)
	}
	if r := comments[0].Reactions; len(r) != 2 || r[0].Count != 1 || r[0].UserIDs[0] != env.alice {
		t.Fatalf("attached reactions = %+v, want alice's thumbs_up and bob's eyes", r)
	}
	if comments[1].Reactions != nil {
		t.Fatalf("approval row got reactions: %+v", comments[1].Reactions)
	}
}

func TestCommentReactionsRejectUnknownKeysAndComments(t *testing.T) {
	env := newReactionEnv(t)

	if _, err := env.service.AddReaction(env.commentID, env.alice, "rocket"); !errors.Is(err, services.ErrInvalidCommentReaction) {
		t.Fatalf("AddReaction(rocket) error = %v, want ErrInvalidCommentReaction", err)
	}
	if err := env.service.RemoveReaction(env.commentID, env.alice, "rocket"); !errors.Is(err, services.ErrInvalidCommentReaction) {
		t.Fatalf("RemoveReaction(rocket) error = %v, want ErrInvalidCommentReaction", err)
	}
	if _, err := env.service.AddReaction(env.commentID+100000, env.alice, "check"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("AddReaction(missing comment) error = %v, want ErrNotFound", err)
	}
}

func TestCommentReactionsReadsAreInvalidatedByWrites(t *testing.T) {
	env := newReactionEnv(t)
	comments := []models.Comment{{ID: env.commentID, Source: "human"}}

	// Prime the read cache with "no reactions".
	if err := env.service.AttachReactions(comments); err != nil {
		t.Fatalf("AttachReactions error = %v", err)
	}
	if len(comments[0].Reactions) != 0 {
		t.Fatalf("primed reactions = %+v, want none", comments[0].Reactions)
	}

	if _, err := env.service.AddReaction(env.commentID, env.alice, "check"); err != nil {
		t.Fatalf("AddReaction error = %v", err)
	}
	if err := env.service.AttachReactions(comments); err != nil {
		t.Fatalf("AttachReactions error = %v", err)
	}
	if len(comments[0].Reactions) != 1 || comments[0].Reactions[0].Reaction != "check" {
		t.Fatalf("reactions after add = %+v, want the new check reaction", comments[0].Reactions)
	}

	if err := env.service.RemoveReaction(env.commentID, env.alice, "check"); err != nil {
		t.Fatalf("RemoveReaction error = %v", err)
	}
	if err := env.service.AttachReactions(comments); err != nil {
		t.Fatalf("AttachReactions error = %v", err)
	}
	if len(comments[0].Reactions) != 0 {
		t.Fatalf("reactions after remove = %+v, want none", comments[0].Reactions)
	}
}

func TestCommentReactionsCascadeWithTheComment(t *testing.T) {
	env := newReactionEnv(t)
	if _, err := env.service.AddReaction(env.commentID, env.alice, "check"); err != nil {
		t.Fatalf("AddReaction error = %v", err)
	}
	if err := env.service.Delete(env.commentID); err != nil {
		t.Fatalf("Delete error = %v", err)
	}

	var n int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM comment_reactions`).Scan(&n); err != nil {
		t.Fatalf("count comment_reactions: %v", err)
	}
	if n != 0 {
		t.Fatalf("comment_reactions rows after comment delete = %d, want 0", n)
	}
}
