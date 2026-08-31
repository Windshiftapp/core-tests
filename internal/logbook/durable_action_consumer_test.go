//go:build test

package logbook

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/events"
	"windshift/internal/logbookevents"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestLogbookIngestionCommitsReadyStateChunksAndEventsTogether(t *testing.T) {
	tdb := newLogbookPostgresTestDB(t)
	db := tdb.GetDatabase()
	repo, doc := createLogbookDocumentFixture(t, db, "atomic-success")
	recorder := NewDurableDocumentEventRecorder(db)
	ingestion := NewIngestionService(repo, nil, recorder)

	if err := ingestion.IngestNote(context.Background(), doc.ID); err != nil {
		t.Fatalf("IngestNote() error = %v", err)
	}
	loaded, err := repo.GetDocument(doc.ID)
	if err != nil {
		t.Fatalf("GetDocument() error = %v", err)
	}
	if loaded.Status != models.LogbookDocStatusReady || loaded.ChunkCount == 0 {
		t.Fatalf("completed document = status:%q chunks:%d, want ready with chunks", loaded.Status, loaded.ChunkCount)
	}

	rows, err := db.Query(`
		SELECT event_type, aggregate_sequence, payload
		FROM domain_events
		WHERE aggregate_type = 'logbook_document' AND aggregate_id = ?
		ORDER BY aggregate_sequence
	`, doc.ID)
	if err != nil {
		t.Fatalf("query document events: %v", err)
	}
	defer rows.Close()
	wantTypes := []string{logbookevents.Classified, DurableLogbookActionCompatibilityEvent}
	for index, wantType := range wantTypes {
		if !rows.Next() {
			t.Fatalf("document event %d missing", index)
		}
		var eventType string
		var sequence int64
		var payload []byte
		if err := rows.Scan(&eventType, &sequence, &payload); err != nil {
			t.Fatalf("scan document event %d: %v", index, err)
		}
		if eventType != wantType || sequence != int64(index+1) {
			t.Fatalf("document event %d = %q sequence %d, want %q sequence %d", index, eventType, sequence, wantType, index+1)
		}
		if eventType == logbookevents.Classified {
			var fact logbookevents.ClassifiedV1
			if err := json.Unmarshal(payload, &fact); err != nil {
				t.Fatalf("decode classified fact: %v", err)
			}
			if fact.Document.ID != doc.ID || fact.Document.BucketID != doc.BucketID || fact.Document.ContentType != models.LogbookContentTypeKnowledge || fact.Document.MimeType != "text/markdown" {
				t.Fatalf("classified fact document = %+v", fact.Document)
			}
		}
	}
	if rows.Next() {
		t.Fatal("unexpected extra document event")
	}
}

func TestLogbookIngestionRollsBackReadyStateAndChunksWhenEventAppendFails(t *testing.T) {
	tdb := newLogbookPostgresTestDB(t)
	db := tdb.GetDatabase()
	repo, doc := createLogbookDocumentFixture(t, db, "atomic-rollback")
	ingestion := NewIngestionService(repo, nil, classifiedRecorderFunc(func(context.Context, database.Tx, logbookevents.ClassifiedInput) error {
		return errForcedLogbookEventAppend
	}))

	err := ingestion.IngestNote(context.Background(), doc.ID)
	if !errors.Is(err, errForcedLogbookEventAppend) {
		t.Fatalf("IngestNote() error = %v, want forced event append failure", err)
	}
	loaded, loadErr := repo.GetDocument(doc.ID)
	if loadErr != nil {
		t.Fatalf("GetDocument() error = %v", loadErr)
	}
	if loaded.Status == models.LogbookDocStatusReady || loaded.ChunkCount != 0 {
		t.Fatalf("rolled-back document = status:%q chunks:%d, want non-ready with no chunks", loaded.Status, loaded.ChunkCount)
	}
	var eventCount int
	if queryErr := db.QueryRow("SELECT COUNT(*) FROM domain_events WHERE aggregate_id = ?", doc.ID).Scan(&eventCount); queryErr != nil {
		t.Fatalf("count rolled-back events: %v", queryErr)
	}
	if eventCount != 0 {
		t.Fatalf("rolled-back event count = %d, want 0", eventCount)
	}
}

func TestDurableLogbookConsumerFreezesTargetsAndDeduplicatesExecution(t *testing.T) {
	tdb := newLogbookPostgresTestDB(t)
	db := tdb.GetDatabase()
	_, doc := createLogbookDocumentFixture(t, db, "frozen-targets")
	actionRepo := repository.NewLogbookActionRepository(db)
	classifiedID := createLogbookAction(t, actionRepo, doc.BucketID, "classified", models.LogbookTriggerDocumentClassified, "")
	keywordID := createLogbookAction(t, actionRepo, doc.BucketID, "keyword", models.LogbookTriggerContentKeyword, `{"keywords":["durable"],"keyword_mode":"all"}`)
	mimeID := createLogbookAction(t, actionRepo, doc.BucketID, "mime", models.LogbookTriggerMimeType, `{"mime_types":["text/*"]}`)
	service := NewLogbookActionService(db, actionRepo, "", "", "")
	t.Cleanup(service.Stop)
	service.InvalidateBucketCache(doc.BucketID)
	consumer := NewDurableLogbookActionConsumer(db, service, nil)
	event := classifiedLogbookDomainEvent(t, 71, "logbook-classified-71", doc, "durable systems need durable delivery")

	if err := consumer.Handle(context.Background(), event); err != nil {
		t.Fatalf("first Handle() error = %v", err)
	}
	laterID := createLogbookAction(t, actionRepo, doc.BucketID, "enabled later", models.LogbookTriggerDocumentClassified, "")
	service.InvalidateBucketCache(doc.BucketID)
	if err := consumer.Handle(context.Background(), event); err != nil {
		t.Fatalf("duplicate Handle() error = %v", err)
	}

	rows, err := db.Query("SELECT action_id FROM action_event_targets WHERE event_key = ? ORDER BY action_id", event.Key)
	if err != nil {
		t.Fatalf("query frozen targets: %v", err)
	}
	defer rows.Close()
	var gotIDs []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan frozen target: %v", err)
		}
		gotIDs = append(gotIDs, id)
	}
	wantIDs := map[int]bool{classifiedID: true, keywordID: true, mimeID: true}
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("frozen target IDs = %v, want classified/keyword/mime", gotIDs)
	}
	for _, id := range gotIDs {
		if !wantIDs[id] || id == laterID {
			t.Fatalf("unexpected frozen target %d in %v", id, gotIDs)
		}
	}
	var executionCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM logbook_action_execution_logs WHERE durable_event_key = ?", event.Key).Scan(&executionCount); err != nil {
		t.Fatalf("count durable executions: %v", err)
	}
	if executionCount != 3 {
		t.Fatalf("durable execution count = %d, want 3", executionCount)
	}
}

func TestDurableLogbookCutoverStopsCompatibilityWithoutOmittingCanonicalFact(t *testing.T) {
	tdb := newLogbookPostgresTestDB(t)
	db := tdb.GetDatabase()
	repo, doc := createLogbookDocumentFixture(t, db, "cutover")
	ingress := NewDurableLogbookActionIngress(db)
	cutover, err := ingress.ActivateCanonicalDocuments(context.Background())
	if err != nil {
		t.Fatalf("ActivateCanonicalDocuments() error = %v", err)
	}
	ingestion := NewIngestionService(repo, nil, NewDurableDocumentEventRecorder(db))
	if err := ingestion.IngestNote(context.Background(), doc.ID); err != nil {
		t.Fatalf("IngestNote() error = %v", err)
	}

	var canonical, compatibility int
	if err := db.QueryRow("SELECT COUNT(*) FROM domain_events WHERE event_type = ?", logbookevents.Classified).Scan(&canonical); err != nil {
		t.Fatalf("count canonical facts: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM domain_events WHERE event_type = ?", DurableLogbookActionCompatibilityEvent).Scan(&compatibility); err != nil {
		t.Fatalf("count compatibility facts: %v", err)
	}
	if canonical != 1 || compatibility != 0 {
		t.Fatalf("post-cutover facts = canonical:%d compatibility:%d, want 1/0", canonical, compatibility)
	}
	if err := ConfigureDurableLogbookActionConsumers(context.Background(), events.NewStore(db), cutover); err != nil {
		t.Fatalf("ConfigureDurableLogbookActionConsumers() error = %v", err)
	}
	var active bool
	var startID int64
	if err := db.QueryRow("SELECT is_active, start_event_id FROM domain_event_consumers WHERE consumer_key = ?", DurableLogbookActionConsumerKey).Scan(&active, &startID); err != nil {
		t.Fatalf("load canonical logbook consumer: %v", err)
	}
	if !active || startID != cutover.StartEventID {
		t.Fatalf("canonical consumer = active:%v start:%d, want true/%d", active, startID, cutover.StartEventID)
	}
}

func TestDurableLogbookEngineRecoversEventsWrittenWhileStopped(t *testing.T) {
	tdb := newLogbookPostgresTestDB(t)
	db := tdb.GetDatabase()
	repo, firstDoc := createLogbookDocumentFixture(t, db, "restart")
	actionRepo := repository.NewLogbookActionRepository(db)
	createLogbookAction(t, actionRepo, firstDoc.BucketID, "restart", models.LogbookTriggerDocumentClassified, "")
	service := NewLogbookActionService(db, actionRepo, "", "", "")
	t.Cleanup(service.Stop)
	service.InvalidateBucketCache(firstDoc.BucketID)
	recorder := NewDurableDocumentEventRecorder(db)
	ingestion := NewIngestionService(repo, nil, recorder)

	config := logbookEventEngineTestConfig()
	startEngine := func(activate bool) *events.Engine {
		t.Helper()
		engine := events.NewEngine(db, config)
		if err := PrepareDurableLogbookActionEngine(context.Background(), engine, service, activate); err != nil {
			t.Fatalf("prepare logbook engine: %v", err)
		}
		if err := engine.Start(context.Background()); err != nil {
			t.Fatalf("start logbook engine: %v", err)
		}
		return engine
	}
	stopEngine := func(engine *events.Engine) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := engine.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown logbook engine: %v", err)
		}
	}

	firstEngine := startEngine(true)
	if err := ingestion.IngestNote(context.Background(), firstDoc.ID); err != nil {
		t.Fatalf("ingest first document: %v", err)
	}
	waitForLogbookExecutions(t, db, 1)
	stopEngine(firstEngine)

	secondDoc := createLogbookDocumentInBucket(t, repo, firstDoc.BucketID, "Written while stopped")
	if err := ingestion.IngestNote(context.Background(), secondDoc.ID); err != nil {
		t.Fatalf("ingest document while stopped: %v", err)
	}
	assertLogbookExecutionCount(t, db, 1)

	secondEngine := startEngine(false)
	t.Cleanup(func() { stopEngine(secondEngine) })
	waitForLogbookExecutions(t, db, 2)
}

func TestDurableLogbookPoisonEventReplayAndSkipPreserveDocumentOrder(t *testing.T) {
	tdb := newLogbookPostgresTestDB(t)
	db := tdb.GetDatabase()
	_, doc := createLogbookDocumentFixture(t, db, "poison")
	actionRepo := repository.NewLogbookActionRepository(db)
	createLogbookAction(t, actionRepo, doc.BucketID, "after poison", models.LogbookTriggerDocumentClassified, "")
	service := NewLogbookActionService(db, actionRepo, "", "", "")
	t.Cleanup(service.Stop)
	service.InvalidateBucketCache(doc.BucketID)

	engine := events.NewEngine(db, logbookEventEngineTestConfig())
	if err := PrepareDurableLogbookActionEngine(context.Background(), engine, service, true); err != nil {
		t.Fatalf("prepare logbook engine: %v", err)
	}
	badInput := classifiedLogbookEventInput(t, doc, "poison-v2")
	badInput.PayloadVersion = 99
	bad, err := engine.AppendStandalone(context.Background(), badInput)
	if err != nil {
		t.Fatalf("append poison event: %v", err)
	}
	good, err := engine.AppendStandalone(context.Background(), classifiedLogbookEventInput(t, doc, "after-poison"))
	if err != nil {
		t.Fatalf("append ordered event: %v", err)
	}
	if bad.AggregateSequence != 1 || good.AggregateSequence != 2 {
		t.Fatalf("aggregate sequences = %d/%d, want 1/2", bad.AggregateSequence, good.AggregateSequence)
	}
	if err := engine.Start(context.Background()); err != nil {
		t.Fatalf("start logbook engine: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = engine.Shutdown(ctx)
	})
	waitForDeliveryState(t, db, bad.ID, DurableLogbookActionConsumerKey, string(events.StateFailed))
	assertDeliveryState(t, db, good.ID, DurableLogbookActionConsumerKey, string(events.StatePending))

	operator := events.Operator{Kind: "user", Ref: "1"}
	if err := engine.Store().Replay(context.Background(), bad.ID, DurableLogbookActionConsumerKey, operator, "verify poison replay", time.Now().UTC()); err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	waitForDeliveryState(t, db, bad.ID, DurableLogbookActionConsumerKey, string(events.StateFailed))
	if err := engine.Store().Skip(context.Background(), bad.ID, DurableLogbookActionConsumerKey, operator, "invalid payload version", time.Now().UTC()); err != nil {
		t.Fatalf("Skip() error = %v", err)
	}
	waitForDeliveryState(t, db, good.ID, DurableLogbookActionConsumerKey, string(events.StateCompleted))
	waitForLogbookExecutions(t, db, 1)

	var auditActions int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM domain_event_delivery_actions
		WHERE event_id = ? AND consumer_key = ? AND action IN ('replay', 'skip')
	`, bad.ID, DurableLogbookActionConsumerKey).Scan(&auditActions); err != nil {
		t.Fatalf("count replay/skip audit actions: %v", err)
	}
	if auditActions != 2 {
		t.Fatalf("replay/skip audit actions = %d, want 2", auditActions)
	}
}

func TestLogbookDurableEngineShutdownIsBounded(t *testing.T) {
	tdb := newLogbookPostgresTestDB(t)
	db := tdb.GetDatabase()
	store := events.NewStore(db)
	const consumerKey = "logbook.shutdown.test.v1"
	if err := store.ConfigureConsumer(context.Background(), events.Consumer{
		Key: consumerKey, HandlerVersion: 1, Active: true, StartEventID: 1,
		EventTypes: []string{"logbook.shutdown.v1"},
	}); err != nil {
		t.Fatalf("configure shutdown consumer: %v", err)
	}
	input := events.NewEvent{
		Key: "logbook-shutdown-event", AggregateType: "logbook_document", AggregateID: "shutdown-doc",
		Type: "logbook.shutdown.v1", PayloadVersion: 1, OccurredAt: time.Now().UTC(),
		ActorKind: "system", SourceKind: "test", Payload: json.RawMessage(`{"ok":true}`),
	}
	if _, err := store.AppendStandalone(context.Background(), input); err != nil {
		t.Fatalf("append shutdown event: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	engine := events.NewEngine(db, logbookEventEngineTestConfig())
	if err := engine.RegisterHandler(consumerKey, events.HandlerFunc(func(context.Context, events.Event) error {
		close(started)
		<-release
		return nil
	})); err != nil {
		t.Fatalf("register blocking handler: %v", err)
	}
	if err := engine.Start(context.Background()); err != nil {
		t.Fatalf("start blocking engine: %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("blocking handler did not start")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := engine.Shutdown(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("bounded Shutdown() error = %v, want context.Canceled", err)
	}
	close(release)
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := engine.Shutdown(ctx); err != nil {
		t.Fatalf("drained Shutdown() error = %v", err)
	}
}

type classifiedRecorderFunc func(context.Context, database.Tx, logbookevents.ClassifiedInput) error

func (f classifiedRecorderFunc) RecordClassified(ctx context.Context, tx database.Tx, input logbookevents.ClassifiedInput) error {
	return f(ctx, tx, input)
}

var errForcedLogbookEventAppend = errors.New("forced event append failure")

func newLogbookPostgresTestDB(t *testing.T) *testutils.TestDB {
	t.Helper()
	if !testutils.IsPostgres() {
		t.Skip("logbook sidecar uses PostgreSQL")
	}
	tdb := testutils.CreateTestDB(t, true)
	if err := InitializeSchema(tdb.GetDatabase()); err != nil {
		t.Fatalf("InitializeSchema() error = %v", err)
	}
	return tdb
}

func createLogbookDocumentFixture(t *testing.T, db database.Database, suffix string) (*Repository, *models.LogbookDocument) {
	t.Helper()
	repo := NewRepository(db)
	bucket, err := repo.CreateBucket(models.LogbookBucketCreateRequest{Name: "Durable " + suffix}, 1)
	if err != nil {
		t.Fatalf("create logbook bucket: %v", err)
	}
	doc := createLogbookDocumentInBucket(t, repo, bucket.ID, "Durable note")
	return repo, doc
}

func createLogbookDocumentInBucket(t *testing.T, repo *Repository, bucketID, title string) *models.LogbookDocument {
	t.Helper()
	doc := &models.LogbookDocument{
		BucketID: bucketID, Title: title, SourceType: models.LogbookSourceNote,
		RawContent: "durable systems need durable delivery", MimeType: "text/markdown",
		Status: models.LogbookDocStatusPending, CreatedBy: 1,
	}
	if err := repo.CreateDocument(doc); err != nil {
		t.Fatalf("create logbook document: %v", err)
	}
	return doc
}

func createLogbookAction(t *testing.T, repo *repository.LogbookActionRepository, bucketID, name string, trigger models.LogbookActionTriggerType, config string) int {
	t.Helper()
	createdBy := 1
	id, err := repo.Create(&models.LogbookAction{
		BucketID: bucketID, Name: name, IsEnabled: true, TriggerType: trigger,
		TriggerConfig: config, CreatedBy: &createdBy,
	})
	if err != nil {
		t.Fatalf("create logbook action %q: %v", name, err)
	}
	return id
}

func classifiedLogbookDomainEvent(t *testing.T, id int64, key string, doc *models.LogbookDocument, rawContent string) events.Event {
	t.Helper()
	payload, err := json.Marshal(logbookevents.ClassifiedV1{Document: logbookevents.DocumentSnapshot{
		ID: doc.ID, BucketID: doc.BucketID, Title: doc.Title,
		ContentType: models.LogbookContentTypeKnowledge, MimeType: "text/markdown",
		SourceType: doc.SourceType, Author: doc.Author, RawContent: rawContent,
	}})
	if err != nil {
		t.Fatalf("marshal classified event: %v", err)
	}
	return events.Event{
		ID: id, Key: key, AggregateType: "logbook_document", AggregateID: doc.ID,
		AggregateSequence: 1, Type: logbookevents.Classified, PayloadVersion: logbookevents.PayloadVersion,
		OccurredAt: time.Date(2026, time.August, 27, 14, 0, 0, 0, time.UTC),
		ActorKind:  "user", ActorRef: fmt.Sprint(doc.CreatedBy), SourceKind: "logbook", Payload: payload,
	}
}

func classifiedLogbookEventInput(t *testing.T, doc *models.LogbookDocument, key string) events.NewEvent {
	t.Helper()
	event := classifiedLogbookDomainEvent(t, 0, key, doc, doc.RawContent)
	return events.NewEvent{
		Key: event.Key, AggregateType: event.AggregateType, AggregateID: event.AggregateID,
		Type: event.Type, PayloadVersion: event.PayloadVersion, OccurredAt: event.OccurredAt,
		ActorKind: event.ActorKind, ActorRef: event.ActorRef, SourceKind: event.SourceKind, Payload: event.Payload,
	}
}

func logbookEventEngineTestConfig() events.Config {
	config := events.DefaultConfig()
	config.WorkerCount = 2
	config.PollInterval = 5 * time.Millisecond
	config.HandlerTimeout = time.Second
	config.LeaseDuration = 2 * time.Second
	config.MaxAttempts = 1
	return config
}

func waitForLogbookExecutions(t *testing.T, db database.Database, want int) {
	t.Helper()
	waitForLogbookCondition(t, func() (bool, string) {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM logbook_action_execution_logs WHERE durable_event_key IS NOT NULL").Scan(&count); err != nil {
			t.Fatalf("count durable logbook executions: %v", err)
		}
		return count == want, fmt.Sprintf("execution count %d, want %d", count, want)
	})
}

func assertLogbookExecutionCount(t *testing.T, db database.Database, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM logbook_action_execution_logs WHERE durable_event_key IS NOT NULL").Scan(&count); err != nil {
		t.Fatalf("count durable logbook executions: %v", err)
	}
	if count != want {
		t.Fatalf("durable logbook execution count = %d, want %d", count, want)
	}
}

func waitForDeliveryState(t *testing.T, db database.Database, eventID int64, consumerKey, want string) {
	t.Helper()
	waitForLogbookCondition(t, func() (bool, string) {
		var state string
		err := db.QueryRow("SELECT state FROM domain_event_deliveries WHERE event_id = ? AND consumer_key = ?", eventID, consumerKey).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return false, "delivery not materialized"
		}
		if err != nil {
			t.Fatalf("load delivery state: %v", err)
		}
		return state == want, fmt.Sprintf("delivery state %q, want %q", state, want)
	})
}

func assertDeliveryState(t *testing.T, db database.Database, eventID int64, consumerKey, want string) {
	t.Helper()
	var state string
	if err := db.QueryRow("SELECT state FROM domain_event_deliveries WHERE event_id = ? AND consumer_key = ?", eventID, consumerKey).Scan(&state); err != nil {
		t.Fatalf("load delivery state: %v", err)
	}
	if state != want {
		t.Fatalf("delivery state = %q, want %q", state, want)
	}
}

func waitForLogbookCondition(t *testing.T, condition func() (bool, string)) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, detail := condition()
		if ok {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal(detail)
		case <-ticker.C:
		}
	}
}
