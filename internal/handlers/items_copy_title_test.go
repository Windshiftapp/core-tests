//go:build test

package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"windshift/internal/models"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
	"windshift/internal/validation"
)

func TestItemHandler_CopyNormalizesDerivedTitle(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	seed := tdb.SeedTestData(t)
	perm, tracker, notifications := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), perm, tracker, notifications)
	f := factory.NewTestFactory(tdb.GetDatabase())

	tests := []struct {
		name       string
		source     string
		wantStatus int
		wantTitle  string
	}{
		{
			name:       "trims surrounding whitespace",
			source:     "Legacy title \t",
			wantStatus: http.StatusOK,
			wantTitle:  "COPY - Legacy title",
		},
		{
			name:       "caps multibyte title by runes",
			source:     strings.Repeat("ø", validation.TitleMaxRunes),
			wantStatus: http.StatusOK,
			wantTitle:  "COPY - " + strings.Repeat("ø", validation.TitleMaxRunes-7),
		},
		{
			name:       "rejects control character",
			source:     "Legacy\x00title",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			itemID, err := f.CreateItem(factory.CreateItemOpts{
				WorkspaceID: seed.WorkspaceID,
				Title:       "Copy title fixture",
				StatusID:    &seed.StatusID,
				PriorityID:  &seed.PriorityID,
				CreatorID:   &seed.UserID,
			})
			if err != nil {
				t.Fatalf("create source item: %v", err)
			}

			// The production API refuses these legacy title shapes. Write one
			// directly so the copy boundary's normalization is observable.
			if _, err := tdb.Exec("UPDATE items SET title = ? WHERE id = ?", tt.source, itemID); err != nil {
				t.Fatalf("set legacy source title: %v", err)
			}

			req := testutils.CreateAuthenticatedJSONRequest(t, http.MethodPost, "/api/items/1/copy", nil, nil)
			req.SetPathValue("id", strconv.Itoa(itemID))
			rr := testutils.ExecuteRequest(t, handler.Copy, req)
			rr.AssertStatusCode(tt.wantStatus)

			if tt.wantStatus != http.StatusOK {
				rr.AssertBodyContains("Title must be a single line without control characters")
				return
			}

			var copied models.Item
			rr.AssertJSONResponse(&copied)
			if copied.Title != tt.wantTitle {
				t.Fatalf("copied title = %q, want %q", copied.Title, tt.wantTitle)
			}
			if !utf8.ValidString(copied.Title) || utf8.RuneCountInString(copied.Title) > validation.TitleMaxRunes {
				t.Fatalf("copied title is outside the title contract: %q", copied.Title)
			}
		})
	}
}
