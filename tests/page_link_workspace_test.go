package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// Item↔page links must stay inside one workspace. Denial is intentionally
// opaque (404, matching the per-page ACL denial shape) so the response does
// not leak the existence of the foreign page.
func TestPageLinksStayInWorkspace(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()

	_ = CreateBearerToken(t, server)

	homeWorkspaceID, _ := CreateTestWorkspace(t, server, "Page link home", "PLHA")
	awayWorkspaceID, _ := CreateTestWorkspace(t, server, "Page link away", "PLAW")

	item := CreateTestItem(t, server, homeWorkspaceID, "Home item")

	createPage := func(workspaceID int, title string) int {
		t.Helper()
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/pages", workspaceID), map[string]interface{}{
			"title":   title,
			"content": "page body",
		})
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("Failed to create page %q: %d - %s", title, resp.StatusCode, string(body))
		}
		var page struct {
			ID int `json:"id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			t.Fatalf("decode created page %q: %v", title, err)
		}
		resp.Body.Close()
		if page.ID == 0 {
			t.Fatalf("created page %q has no id", title)
		}
		return page.ID
	}

	homePage := createPage(homeWorkspaceID, "Home page")
	awayPage := createPage(awayWorkspaceID, "Away page")

	// Resolve the seeded "Page" link type by name instead of assuming ids.
	resp := MakeAuthRequest(t, server, http.MethodGet, "/v2/link-types", nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to list link types: %d - %s", resp.StatusCode, string(body))
	}
	var linkTypes struct {
		Data []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&linkTypes); err != nil {
		t.Fatalf("decode link types: %v", err)
	}
	resp.Body.Close()

	pageLinkTypeID := 0
	for _, linkType := range linkTypes.Data {
		if linkType.Name == "Page" {
			pageLinkTypeID = linkType.ID
		}
	}
	if pageLinkTypeID == 0 {
		t.Fatal("Default \"Page\" link type was not seeded")
	}

	createLink := func(targetPageID int) *http.Response {
		return MakeAuthRequest(t, server, http.MethodPost, "/links", map[string]interface{}{
			"link_type_id": pageLinkTypeID,
			"source_type":  "item",
			"source_id":    item,
			"target_type":  "page",
			"target_id":    targetPageID,
		})
	}

	t.Run("SameWorkspaceLinkSucceeds", func(t *testing.T) {
		resp := createLink(homePage)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("Expected 201 for same-workspace page link, got %d - %s", resp.StatusCode, string(body))
		}
	})

	t.Run("CrossWorkspaceLinkDenied", func(t *testing.T) {
		resp := createLink(awayPage)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("Expected opaque 404 for cross-workspace page link, got %d - %s", resp.StatusCode, string(body))
		}
	})

	t.Run("CrossWorkspaceLinkDeniedFromPageSide", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/links", map[string]interface{}{
			"link_type_id": pageLinkTypeID,
			"source_type":  "page",
			"source_id":    awayPage,
			"target_type":  "item",
			"target_id":    item,
		})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("Expected opaque 404 for reverse cross-workspace page link, got %d - %s", resp.StatusCode, string(body))
		}
	})

	t.Run("LinkedPageListsItem", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/pages/%d/links", homePage), nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("Failed to list page links: %d - %s", resp.StatusCode, string(body))
		}
		body, _ := io.ReadAll(resp.Body)
		var payload struct {
			Data struct {
				Incoming []map[string]interface{} `json:"incoming"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode page links: %v", err)
		}
		listing := payload.Data
		found := false
		for _, link := range listing.Incoming {
			if link["source_type"] == "item" && int(link["source_id"].(float64)) == item {
				found = true
			}
		}
		if !found {
			t.Errorf("Expected home page to list the item as incoming, got %+v", listing.Incoming)
		}
	})

	t.Run("AwayPageHasNoLinks", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/pages/%d/links", awayPage), nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("Failed to list away page links: %d - %s", resp.StatusCode, string(body))
		}
		var payload struct {
			Data struct {
				Incoming []map[string]interface{} `json:"incoming"`
				Outgoing []map[string]interface{} `json:"outgoing"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode away page links: %v", err)
		}
		listing := payload.Data
		if len(listing.Incoming) != 0 || len(listing.Outgoing) != 0 {
			t.Errorf("Expected away page to have no links, got incoming=%d outgoing=%d", len(listing.Incoming), len(listing.Outgoing))
		}
	})
}
