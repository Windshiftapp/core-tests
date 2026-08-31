//go:build test

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

type publicBoardContractFixture struct {
	db           *testutils.TestDB
	handler      *PublicBoardHandler
	collectionID int
	statusA      int
	statusB      int
	newestItemID int
}

func newPublicBoardContractFixture(t *testing.T) *publicBoardContractFixture {
	t.Helper()
	db := testutils.CreateTestDB(t, true)
	if !testutils.IsPostgres() {
		t.Cleanup(func() { _ = db.Close() })
	}

	var workspaceID, statusA, statusB int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Large public board', 'PBIG') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	rows, err := db.Query(`SELECT id FROM statuses ORDER BY id LIMIT 2`)
	if err != nil {
		t.Fatalf("load statuses: %v", err)
	}
	defer rows.Close()
	statuses := []int{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan status: %v", err)
		}
		statuses = append(statuses, id)
	}
	if len(statuses) != 2 {
		t.Fatalf("seeded statuses = %v, want at least two", statuses)
	}
	statusA, statusB = statuses[0], statuses[1]

	var collectionID int
	if err := db.QueryRow(`
		INSERT INTO collections (name, ql_query, is_public, workspace_id, public_slug)
		VALUES ('Large public board', 'workspace_id = ' || ? || ' AND id > 0', true, ?, 'large-board') RETURNING id
	`, workspaceID, workspaceID).Scan(&collectionID); err != nil {
		t.Fatalf("insert collection: %v", err)
	}
	var configID int
	if err := db.QueryRow(`
		INSERT INTO board_configurations (collection_id, card_fields)
		VALUES (?, '[{"field_identifier":"status","field_type":"system","display_order":0},{"field_identifier":"custom_field_7","field_type":"custom","display_order":1}]')
		RETURNING id
	`, collectionID).Scan(&configID); err != nil {
		t.Fatalf("insert board configuration: %v", err)
	}
	insertColumn := func(name string, order, statusID int) {
		t.Helper()
		var columnID int
		if err := db.QueryRow(`
			INSERT INTO board_columns (board_configuration_id, name, display_order, color)
			VALUES (?, ?, ?, '#64748b') RETURNING id
		`, configID, name, order).Scan(&columnID); err != nil {
			t.Fatalf("insert column %s: %v", name, err)
		}
		if _, err := db.ExecWrite(`
			INSERT INTO board_column_statuses (board_column_id, status_id)
			VALUES (?, ?)
		`, columnID, statusID); err != nil {
			t.Fatalf("map column %s: %v", name, err)
		}
	}
	insertColumn("Primary", 0, statusA)
	insertColumn("Secondary", 1, statusB)

	base := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	newestItemID := 0
	f := factory.NewTestFactory(db.GetDatabase())
	for number := 1; number <= publicBoardItemLimit+1; number++ {
		statusID := statusA
		if number == 1 {
			statusID = statusB
		}
		createdAt := base.Add(time.Duration(number) * time.Minute)
		itemID, err := f.CreateItem(factory.CreateItemOpts{
			WorkspaceID: workspaceID,
			Title:       fmt.Sprintf("Card %d", number),
			StatusID:    &statusID,
			CreatedAt:   &createdAt,
			UpdatedAt:   &createdAt,
		})
		if err != nil {
			t.Fatalf("insert item %d: %v", number, err)
		}
		if number == publicBoardItemLimit+1 {
			newestItemID = itemID
		}
	}

	return &publicBoardContractFixture{
		db: db, handler: NewPublicBoardHandler(db, nil, t.TempDir()),
		collectionID: collectionID, statusA: statusA, statusB: statusB, newestItemID: newestItemID,
	}
}

func (f *publicBoardContractFixture) getBoard(t *testing.T) publicBoardResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/public/board/large-board", nil)
	request.SetPathValue("slug", "large-board")
	recorder := httptest.NewRecorder()
	f.handler.GetPublicBoard(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response publicBoardResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode board: %v", err)
	}
	return response
}

func TestPublicBoardReportsExplicitLimitAndRefreshesMovedCards(t *testing.T) {
	fixture := newPublicBoardContractFixture(t)

	response := fixture.getBoard(t)
	if response.TotalItems != publicBoardItemLimit+1 || response.LoadedItems != publicBoardItemLimit {
		t.Fatalf("item metadata = total %d loaded %d, want %d/%d", response.TotalItems, response.LoadedItems, publicBoardItemLimit+1, publicBoardItemLimit)
	}
	if !response.Truncated || response.ItemLimit != publicBoardItemLimit {
		t.Fatalf("truncation metadata = truncated %v limit %d, want true/%d", response.Truncated, response.ItemLimit, publicBoardItemLimit)
	}
	if len(response.Columns) != 2 || len(response.Columns[0].Items) != publicBoardItemLimit || len(response.Columns[1].Items) != 0 {
		t.Fatalf("column counts = %+v, want %d/0 with explicit truncation", []int{len(response.Columns[0].Items), len(response.Columns[1].Items)}, publicBoardItemLimit)
	}
	if len(response.CardFields) != 1 || response.CardFields[0].FieldIdentifier != "status" {
		t.Fatalf("public card fields = %+v, want only approved status field", response.CardFields)
	}

	if _, err := fixture.db.ExecWrite(`UPDATE items SET status_id = ? WHERE id = ?`, fixture.statusB, fixture.newestItemID); err != nil {
		t.Fatalf("move newest item: %v", err)
	}
	response = fixture.getBoard(t)
	if len(response.Columns[1].Items) != 1 || response.Columns[1].Items[0].Key != "PBIG-501" {
		t.Fatalf("secondary column after move = %+v, want PBIG-501", response.Columns[1].Items)
	}
	if len(response.Columns[0].Items) != publicBoardItemLimit-1 {
		t.Fatalf("primary count after move = %d, want %d", len(response.Columns[0].Items), publicBoardItemLimit-1)
	}
}

func TestPublicBoardCardFieldAllowlist(t *testing.T) {
	supported := []models.ListColumn{
		{FieldIdentifier: "status", FieldType: "system"},
		{FieldIdentifier: "due_date", FieldType: "system"},
		{FieldIdentifier: "labels", FieldType: "system"},
	}
	if err := validatePublicBoardCardFields(supported); err != nil {
		t.Fatalf("supported fields rejected: %v", err)
	}
	for _, field := range []models.ListColumn{
		{FieldIdentifier: "custom_field_7", FieldType: "custom"},
		{FieldIdentifier: "milestone", FieldType: "system"},
	} {
		if err := validatePublicBoardCardFields([]models.ListColumn{field}); err == nil {
			t.Fatalf("unsupported public field unexpectedly accepted: %+v", field)
		}
	}
}

func TestPublicCommentsRewriteAttachmentsAndExcludePrivateContent(t *testing.T) {
	fixture := newPublicBoardContractFixture(t)
	var userID int
	if err := fixture.db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('commenter@example.test', 'commenter', 'Public', 'Commenter', true) RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert commenter: %v", err)
	}
	if _, err := fixture.db.ExecWrite(`
		INSERT INTO comments (item_id, author_id, content, is_private)
		VALUES
			(?, ?, 'Visible ![](/api/attachments/41/download)', false),
			(?, ?, 'Private ![](/api/attachments/42/download)', true)
	`, fixture.newestItemID, userID, fixture.newestItemID, userID); err != nil {
		t.Fatalf("insert comments: %v", err)
	}

	comments, err := fixture.handler.loadPublicComments(fixture.newestItemID, "large-board")
	if err != nil {
		t.Fatalf("loadPublicComments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("comments = %+v, want one non-private comment", comments)
	}
	if !strings.Contains(comments[0].Content, "/api/public/board/large-board/attachments/41/download") {
		t.Fatalf("comment attachment was not rewritten: %q", comments[0].Content)
	}
	if strings.Contains(comments[0].Content, "/api/attachments/") {
		t.Fatalf("comment retained authenticated attachment route: %q", comments[0].Content)
	}
}

func TestPublicBoardAttachmentRejectsItemsOutsideCollection(t *testing.T) {
	fixture := newPublicBoardContractFixture(t)
	var workspaceID, itemID, attachmentID int
	if err := fixture.db.QueryRow(`
		INSERT INTO workspaces (name, key)
		VALUES ('Private attachment workspace', 'PATT') RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert outside workspace: %v", err)
	}
	f := factory.NewTestFactory(fixture.db.GetDatabase())
	itemID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: workspaceID,
		Title:       "Outside item",
		StatusID:    &fixture.statusA,
	})
	if err != nil {
		t.Fatalf("insert outside item: %v", err)
	}
	if err := fixture.db.QueryRow(`
		INSERT INTO attachments
			(item_id, entity_type, filename, original_filename, file_path, mime_type, file_size)
		VALUES (?, 'item', 'outside.png', 'outside.png', 'outside.png', 'image/png', 1)
		RETURNING id
	`, itemID).Scan(&attachmentID); err != nil {
		t.Fatalf("insert outside attachment: %v", err)
	}

	request := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/public/board/large-board/attachments/%d", attachmentID),
		nil,
	)
	request.SetPathValue("slug", "large-board")
	request.SetPathValue("id", fmt.Sprintf("%d", attachmentID))
	recorder := httptest.NewRecorder()

	fixture.handler.DownloadAttachment(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", recorder.Header().Get("Cache-Control"))
	}
}
