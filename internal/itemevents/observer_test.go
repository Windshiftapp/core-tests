//go:build test

package itemevents

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

type recordingObserver struct {
	calls [][]RecordedFact
	err   error
}

func (o *recordingObserver) ObserveItemFacts(_ context.Context, _ database.Tx, facts []RecordedFact) error {
	copied := append([]RecordedFact(nil), facts...)
	o.calls = append(o.calls, copied)
	return o.err
}

func newTestItem(id, workspaceID int) *models.Item {
	return &models.Item{ID: id, WorkspaceID: workspaceID, WorkspaceItemNumber: id, Title: "item"}
}

func TestRecorderInvokesObserverInsideTransaction(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	observer := &recordingObserver{}
	RegisterFactObserver(observer)
	t.Cleanup(ClearFactObserver)

	recorder := NewRecorder(db)
	before := newTestItem(11, 3)
	after := *before
	after.Title = "renamed"

	err := database.WithTx(db, func(tx database.Tx) error {
		_, err := recorder.Updated(context.Background(), tx, &after, Changes(before, &after), User(1, "test"))
		return err
	})
	if err != nil {
		t.Fatalf("Updated: %v", err)
	}

	if len(observer.calls) != 1 || len(observer.calls[0]) != 1 {
		t.Fatalf("observer calls = %d/%v, want one call with one fact", len(observer.calls), observer.calls)
	}
	fact := observer.calls[0][0]
	if fact.Type != Updated || fact.ItemID != 11 || fact.WorkspaceID != 3 {
		t.Fatalf("fact = %#v, want updated item 11 in workspace 3", fact)
	}
	if len(fact.Changes) != 1 || fact.Changes[0].Field != "title" {
		t.Fatalf("fact changes = %#v, want a title change", fact.Changes)
	}
}

func TestRecorderBatchInvokesObserverOnceWithAllFacts(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	observer := &recordingObserver{}
	RegisterFactObserver(observer)
	t.Cleanup(ClearFactObserver)

	recorder := NewRecorder(db)
	records := []UpdateRecord{
		{Item: newTestItem(1, 5), Changes: []FieldChange{{Field: "title", OldValue: "a", NewValue: "b"}}, Metadata: User(1, "test")},
		{Item: newTestItem(2, 5), Changes: []FieldChange{{Field: "status_id", OldValue: 1, NewValue: 2}}, StatusChanged: true, Metadata: User(1, "test")},
	}
	err := database.WithTx(db, func(tx database.Tx) error {
		_, err := recorder.UpdatedBatch(context.Background(), tx, records)
		return err
	})
	if err != nil {
		t.Fatalf("UpdatedBatch: %v", err)
	}
	if len(observer.calls) != 1 || len(observer.calls[0]) != 2 {
		t.Fatalf("observer calls = %d/%v, want one call with two facts", len(observer.calls), observer.calls)
	}
	if observer.calls[0][0].Type != Updated || observer.calls[0][1].Type != StatusChanged {
		t.Fatalf("fact types = %q/%q", observer.calls[0][0].Type, observer.calls[0][1].Type)
	}
}

func TestRecorderObserverErrorRollsBackItemWrite(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	observerErr := errors.New("observer failed")
	RegisterFactObserver(&recordingObserver{err: observerErr})
	t.Cleanup(ClearFactObserver)

	recorder := NewRecorder(db)
	item := newTestItem(77, 5)
	err := database.WithTx(db, func(tx database.Tx) error {
		_, err := recorder.Created(context.Background(), tx, item, nil, User(1, "test"))
		return err
	})
	if !errors.Is(err, observerErr) {
		t.Fatalf("Created error = %v, want %v", err, observerErr)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM domain_events WHERE aggregate_id = '77'").Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 0 {
		t.Fatalf("event count = %d, want 0 after observer failure rolled back the transaction", count)
	}
}

func TestRecorderWithoutObserverIsNoop(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	ClearFactObserver()
	t.Cleanup(ClearFactObserver)

	recorder := NewRecorder(db)
	item := newTestItem(88, 5)
	if err := database.WithTx(db, func(tx database.Tx) error {
		_, err := recorder.Created(context.Background(), tx, item, nil, User(1, "test"))
		return err
	}); err != nil {
		t.Fatalf("Created without observer: %v", err)
	}
}
