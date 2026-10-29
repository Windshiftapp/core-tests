//go:build test

package services

import (
	"testing"
	"time"

	"windshift/internal/models"
)

func TestCommentReactionCacheServesWithinTTLAndExpires(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cache := commentReactionCache{now: func() time.Time { return now }}
	groups := []models.CommentReaction{{Reaction: "check", Count: 1, UserIDs: []int{1}, UserNames: []string{"Alice"}}}

	cache.set(7, groups)
	if got, ok := cache.get(7); !ok || len(got) != 1 || got[0].Reaction != "check" {
		t.Fatalf("get within TTL = %+v, %v; want the stored groups", got, ok)
	}

	now = now.Add(reactionCacheTTL + time.Millisecond)
	if _, ok := cache.get(7); ok {
		t.Fatal("get after TTL = hit, want miss")
	}
}

func TestCommentReactionCacheInvalidateDropsEntry(t *testing.T) {
	cache := commentReactionCache{}
	cache.set(7, []models.CommentReaction{{Reaction: "check"}})
	cache.invalidate(7)
	if _, ok := cache.get(7); ok {
		t.Fatal("get after invalidate = hit, want miss")
	}
}

func TestCommentReactionCachePrunesExpiredEntriesAtCapacity(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cache := commentReactionCache{now: func() time.Time { return now }}
	for id := 0; id < reactionCacheMaxEntries; id++ {
		cache.set(id, []models.CommentReaction{})
	}

	now = now.Add(reactionCacheTTL + time.Millisecond)
	cache.set(reactionCacheMaxEntries, []models.CommentReaction{{Reaction: "eyes"}})

	if len(cache.entries) != 1 {
		t.Fatalf("entries after prune = %d, want the expired set dropped", len(cache.entries))
	}
	if _, ok := cache.get(reactionCacheMaxEntries); !ok {
		t.Fatal("newly stored entry missing after prune")
	}
}
