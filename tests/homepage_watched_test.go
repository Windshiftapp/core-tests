package tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestHomepageWatchedItemTimestampTracksAnyUserActivity verifies that the
// watched_items feed timestamp is the item's own recency (last_active_at),
// so a watched item surfaces when any user updates it. The watcher has no
// personal view/edit/comment activity, so the old per-user implementation
// would have returned an empty timestamp or dropped the item entirely.
func TestHomepageWatchedItemTimestampTracksAnyUserActivity(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()

	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Watched Recency Workspace", shortKey("WRW"))
	itemID := CreateTestItem(t, server, workspaceID, "Watched recency item")

	_, watcherUsername, watcherPassword := CreateTestUserWithCredentials(
		t, server, "watched_recency_watcher", "watched-recency-watcher@test.com",
	)
	watcherToken, _ := CreateAuthCredentialsForUser(t, server, watcherUsername, watcherPassword)

	// The watcher only subscribes; they never view, edit, or comment on the item.
	watchResp := MakeAuthRequestWithToken(t, server, watcherToken, http.MethodPut, fmt.Sprintf("/v2/items/%d/watch", itemID), map[string]interface{}{})
	AssertStatusCode(t, watchResp, http.StatusOK)
	watchResp.Body.Close()

	// A different user updates the item. That must move the watched item's feed
	// timestamp even though the watcher has no personal activity on it.
	beforeOtherUserComment := time.Now().UTC().Add(-time.Second)
	_, otherUsername, otherPassword := CreateTestUserWithCredentials(
		t, server, "watched_recency_other", "watched-recency-other@test.com",
	)
	otherToken, _ := CreateAuthCredentialsForUser(t, server, otherUsername, otherPassword)
	commentResp := MakeAuthRequestWithToken(t, server, otherToken, http.MethodPost, fmt.Sprintf("/items/%d/comments", itemID), map[string]interface{}{
		"content": "Update from another user",
	})
	AssertStatusCode(t, commentResp, http.StatusCreated)
	commentResp.Body.Close()

	homepage := getHomepageActivity(t, server, watcherToken)
	watched, found := findHomepageFeedItem(homepage.WatchedItems, itemID)
	if !found {
		t.Fatalf("watched_items should contain item %d watched by the user", itemID)
	}
	if watched.LastActiveAt == "" {
		t.Fatalf("watched item %d last_active_at is empty", itemID)
	}
	lastActiveAt, err := time.Parse(time.RFC3339, watched.LastActiveAt)
	if err != nil {
		t.Fatalf("watched item last_active_at %q is not RFC3339: %v", watched.LastActiveAt, err)
	}
	if lastActiveAt.Before(beforeOtherUserComment) {
		t.Errorf("watched item last_active_at = %s, want at or after the other user's comment at %s",
			lastActiveAt, beforeOtherUserComment)
	}
	if watched.LastActivity != "" {
		t.Errorf("watched item %d last_activity = %q, want empty because the watcher has no personal activity",
			itemID, watched.LastActivity)
	}

	// The watcher has no personal activity, so the per-user feeds stay empty.
	for name, feed := range map[string][]homepageItemActivity{
		"recently_viewed":    homepage.RecentlyViewed,
		"recently_edited":    homepage.RecentlyEdited,
		"recently_commented": homepage.RecentlyCommented,
	} {
		if homepageFeedContainsItem(feed, itemID) {
			t.Errorf("%s should not contain watched item %d without watcher activity", name, itemID)
		}
	}
}

// TestHomepageFeedExposesItemTypeMetadata verifies each homepage activity row
// carries the item type metadata the dashboard renders as the type icon beside
// the item key.
func TestHomepageFeedExposesItemTypeMetadata(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()

	adminSession := CreateBearerToken(t, server)

	configSetID := GetDefaultConfigurationSet(t, server)
	bugTypeID := RequireItemTypeID(t, GetItemTypes(t, server, configSetID), "Bug")

	workspaceID, _ := CreateTestWorkspace(t, server, "Homepage Item Type Workspace", shortKey("HIT"))
	itemID := CreateTestItemWithType(t, server, workspaceID, "Typed watched item", bugTypeID)

	watchResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/v2/items/%d/watch", itemID), map[string]interface{}{})
	AssertStatusCode(t, watchResp, http.StatusOK)
	watchResp.Body.Close()

	homepage := getHomepageActivity(t, server, adminSession)
	watched, found := findHomepageFeedItem(homepage.WatchedItems, itemID)
	if !found {
		t.Fatalf("watched_items should contain item %d", itemID)
	}
	if watched.ItemTypeName != "Bug" {
		t.Errorf("watched item item_type_name = %q, want Bug", watched.ItemTypeName)
	}
	if watched.ItemTypeBuiltinKey != "bug" {
		t.Errorf("watched item item_type_builtin_key = %q, want bug", watched.ItemTypeBuiltinKey)
	}
	if watched.ItemTypeIcon != "Bug" {
		t.Errorf("watched item item_type_icon = %q, want Bug", watched.ItemTypeIcon)
	}
	if watched.ItemTypeColor != "#ea580c" {
		t.Errorf("watched item item_type_color = %q, want #ea580c", watched.ItemTypeColor)
	}
}

func findHomepageFeedItem(feed []homepageItemActivity, itemID int) (homepageItemActivity, bool) {
	for _, item := range feed {
		if item.ItemID == itemID {
			return item, true
		}
	}
	return homepageItemActivity{}, false
}
