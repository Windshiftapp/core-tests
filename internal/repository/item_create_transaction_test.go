//go:build test

package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestWithItemCreateTransactionRetriesUniqueCollisions(t *testing.T) {
	tests := []struct {
		name          string
		firstNumber   int
		firstRank     string
		secondNumber  int
		secondRank    string
		wrapDuplicate bool
	}{
		{
			name:         "fractional rank",
			firstNumber:  2,
			firstRank:    "0|a0",
			secondNumber: 2,
			secondRank:   "0|a1",
		},
		{
			name:          "workspace item number",
			firstNumber:   1,
			firstRank:     "0|a1",
			secondNumber:  2,
			secondRank:    "0|a1",
			wrapDuplicate: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tdb := testutils.CreateTestDB(t, true)
			defer tdb.Close()
			data := tdb.SeedTestData(t)

			if _, err := tdb.Exec(`
				INSERT INTO items (workspace_id, workspace_item_number, title, frac_index)
				VALUES (?, 1, 'Existing item', '0|a0')
			`, data.WorkspaceID); err != nil {
				t.Fatalf("insert existing item: %v", err)
			}

			attempts := 0
			itemID, err := WithItemCreateTransaction(context.Background(), tdb.GetDatabase(), func(tx database.Tx) (int, error) {
				attempts++
				number, rank := test.firstNumber, test.firstRank
				if attempts > 1 {
					number, rank = test.secondNumber, test.secondRank
				}

				var id int
				if err := tx.QueryRow(`
					INSERT INTO items (workspace_id, workspace_item_number, title, frac_index)
					VALUES (?, ?, 'Retried item', ?)
					RETURNING id
				`, data.WorkspaceID, number, rank).Scan(&id); err != nil {
					if test.wrapDuplicate {
						return 0, fmt.Errorf("%w: %w", ErrDuplicateEntry, err)
					}
					return 0, fmt.Errorf("insert retried item: %w", err)
				}
				return id, nil
			})
			if err != nil {
				t.Fatalf("create item transaction: %v", err)
			}
			if attempts != 2 {
				t.Fatalf("attempts = %d, want 2", attempts)
			}

			var number int
			var rank string
			if err := tdb.QueryRow("SELECT workspace_item_number, frac_index FROM items WHERE id = ?", itemID).Scan(&number, &rank); err != nil {
				t.Fatalf("load retried item: %v", err)
			}
			if number != test.secondNumber || rank != test.secondRank {
				t.Fatalf("retried item = number %d, rank %q; want number %d, rank %q", number, rank, test.secondNumber, test.secondRank)
			}
		})
	}
}

func TestItemRepositoryCreateWithRetryRollsBackCallbackFailure(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	data := tdb.SeedTestData(t)

	wantErr := errors.New("source metadata failed")
	repo := NewItemRepository(tdb.GetDatabase())
	_, err := repo.CreateWithRetry(context.Background(), &models.Item{
		WorkspaceID: data.WorkspaceID,
		Title:       "Rolled back item",
	}, func(tx database.Tx, itemID int) error {
		if _, err := tx.Exec("UPDATE items SET description = ? WHERE id = ?", "callback ran", itemID); err != nil {
			return fmt.Errorf("update item in callback: %w", err)
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("CreateWithRetry error = %v, want %v", err, wantErr)
	}

	var count int
	if err := tdb.QueryRow("SELECT COUNT(*) FROM items WHERE title = ?", "Rolled back item").Scan(&count); err != nil {
		t.Fatalf("count rolled back items: %v", err)
	}
	if count != 0 {
		t.Fatalf("rolled back item count = %d, want 0", count)
	}
}
