//go:build test

package services

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/integrations/docmost"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type fakeDocmostKnowledgeSource struct {
	baseURL     string
	token       string
	err         error
	invalidated int
	verified    int
}

func (f *fakeDocmostKnowledgeSource) Credential(context.Context, int) (string, string, error) {
	return f.baseURL, f.token, f.err
}
func (f *fakeDocmostKnowledgeSource) MarkInvalid(int)  { f.invalidated++ }
func (f *fakeDocmostKnowledgeSource) MarkVerified(int) { f.verified++ }

type fakeKnowledgeSearcher struct {
	hits  []docmost.SearchResult
	err   error
	calls int
}

func (f *fakeKnowledgeSearcher) Search(context.Context, string, string, string, int) ([]docmost.SearchResult, error) {
	f.calls++
	return f.hits, f.err
}

func newDocmostRetrieval(t *testing.T, source DocmostKnowledgeSource, searcher DocmostSearcher) *KnowledgeRetrievalService {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	svc := NewKnowledgeRetrievalService(tdb.DB, NewPagePermissionService(tdb.DB, nil))
	svc.SetDocmost(source, searcher)
	return svc
}

func TestKnowledgeRetrieval_DocmostConnected(t *testing.T) {
	source := &fakeDocmostKnowledgeSource{baseURL: "https://wiki.example.com", token: "tok"}
	searcher := &fakeKnowledgeSearcher{hits: []docmost.SearchResult{
		{ID: "p1", SlugID: "AbC", Title: "Runbook", SpaceSlug: "eng", Highlight: "the <b>deploy</b> steps", Rank: 0.9},
	}}
	svc := newDocmostRetrieval(t, source, searcher)

	response, err := svc.SearchWithSources(SearchInput{
		Ctx: context.Background(), UserID: 1, WorkspaceID: 2, Query: "deploy", Sources: []string{KnowledgeSourceDocmost},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(response.Results))
	}
	got := response.Results[0]
	if got.Source != KnowledgeSourceDocmost || got.URL != "https://wiki.example.com/s/eng/p/AbC" {
		t.Fatalf("result = %+v", got)
	}
	if got.Snippet != "the deploy steps" {
		t.Fatalf("snippet = %q, want tags stripped", got.Snippet)
	}
	if len(response.Sources) != 1 || response.Sources[0].State != KnowledgeSourceStateOK {
		t.Fatalf("sources = %+v, want docmost ok", response.Sources)
	}
	if source.verified != 1 {
		t.Fatalf("MarkVerified calls = %d, want 1", source.verified)
	}
}

func TestKnowledgeRetrieval_DocmostNotConnected(t *testing.T) {
	source := &fakeDocmostKnowledgeSource{err: repository.ErrNotFound}
	searcher := &fakeKnowledgeSearcher{}
	svc := newDocmostRetrieval(t, source, searcher)

	response, err := svc.SearchWithSources(SearchInput{
		UserID: 1, WorkspaceID: 2, Query: "deploy", Sources: []string{KnowledgeSourceDocmost},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(response.Results) != 0 {
		t.Fatalf("got %d results, want 0", len(response.Results))
	}
	if response.Sources[0].State != KnowledgeSourceStateNotConnected {
		t.Fatalf("state = %q, want not_connected", response.Sources[0].State)
	}
	if searcher.calls != 0 {
		t.Fatalf("searcher called %d times without a credential", searcher.calls)
	}
}

func TestKnowledgeRetrieval_DocmostRejectedTokenFlagsReconnect(t *testing.T) {
	source := &fakeDocmostKnowledgeSource{baseURL: "https://wiki.example.com", token: "stale"}
	searcher := &fakeKnowledgeSearcher{err: docmost.ErrUnauthorized}
	svc := newDocmostRetrieval(t, source, searcher)

	response, err := svc.SearchWithSources(SearchInput{
		UserID: 1, WorkspaceID: 2, Query: "deploy", Sources: []string{KnowledgeSourceDocmost},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if response.Sources[0].State != KnowledgeSourceStateReconnectRequired {
		t.Fatalf("state = %q, want reconnect_required", response.Sources[0].State)
	}
	if source.invalidated != 1 {
		t.Fatalf("MarkInvalid calls = %d, want 1", source.invalidated)
	}
}

func TestKnowledgeRetrieval_DocmostNotConfiguredIsNotReported(t *testing.T) {
	source := &fakeDocmostKnowledgeSource{err: ErrDocmostNotConfigured}
	searcher := &fakeKnowledgeSearcher{}
	svc := newDocmostRetrieval(t, source, searcher)

	response, err := svc.SearchWithSources(SearchInput{
		UserID: 1, WorkspaceID: 2, Query: "deploy", Sources: []string{KnowledgeSourceDocmost},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(response.Sources) != 0 {
		t.Fatalf("sources = %+v, want no docmost entry for an unconfigured deployment", response.Sources)
	}
	if searcher.calls != 0 {
		t.Fatalf("searcher called %d times", searcher.calls)
	}
}

func TestKnowledgeRetrieval_DocmostSourceWhitelistExcludesIt(t *testing.T) {
	source := &fakeDocmostKnowledgeSource{baseURL: "https://wiki.example.com", token: "tok"}
	searcher := &fakeKnowledgeSearcher{}
	svc := newDocmostRetrieval(t, source, searcher)

	if _, err := svc.SearchWithSources(SearchInput{
		UserID: 1, WorkspaceID: 2, Query: "deploy", Sources: []string{KnowledgeSourcePage},
	}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if searcher.calls != 0 {
		t.Fatalf("docmost searched %d times when excluded", searcher.calls)
	}
}

func TestKnowledgeRetrieval_DocmostReconnectStateSkipsSearch(t *testing.T) {
	source := &fakeDocmostKnowledgeSource{err: ErrDocmostReconnect}
	searcher := &fakeKnowledgeSearcher{}
	svc := newDocmostRetrieval(t, source, searcher)

	response, err := svc.SearchWithSources(SearchInput{
		UserID: 1, WorkspaceID: 2, Query: "deploy", Sources: []string{KnowledgeSourceDocmost},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if response.Sources[0].State != KnowledgeSourceStateReconnectRequired {
		t.Fatalf("state = %q, want reconnect_required", response.Sources[0].State)
	}
	if searcher.calls != 0 {
		t.Fatalf("searcher called %d times for an invalidated credential", searcher.calls)
	}
	if !errors.Is(source.err, ErrDocmostReconnect) {
		t.Fatal("fixture no longer returns ErrDocmostReconnect")
	}
}
