//go:build test

package handlers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/blobstore"
	"windshift/internal/jira"
)

// TestJiraParallelRunsEveryIndexWithinWorkerLimit is the WI-1982 helper
// contract: every index runs exactly once, at most `workers` run at a time,
// and the pool actually overlaps work.
func TestJiraParallelRunsEveryIndexWithinWorkerLimit(t *testing.T) {
	t.Parallel()

	const (
		workers = 3
		n       = 9
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	release := make(chan struct{})
	var releaseOnce sync.Once
	var (
		mu       sync.Mutex
		inFlight int
		maxSeen  int
		started  int
		seen     = make(map[int]int)
	)
	jiraParallel(ctx, workers, n, func(ctx context.Context, i int) {
		mu.Lock()
		inFlight++
		started++
		if inFlight > maxSeen {
			maxSeen = inFlight
		}
		if started >= workers {
			// Release once the pool has filled its slots. If it never does, the
			// context deadline ends the wait instead of hanging the test.
			releaseOnce.Do(func() { close(release) })
		}
		mu.Unlock()

		select {
		case <-release:
		case <-ctx.Done():
		}

		mu.Lock()
		inFlight--
		seen[i]++
		mu.Unlock()
	})

	if len(seen) != n {
		t.Fatalf("ran %d distinct indices, want %d", len(seen), n)
	}
	for i := 0; i < n; i++ {
		if seen[i] != 1 {
			t.Fatalf("index %d ran %d times, want once", i, seen[i])
		}
	}
	if maxSeen > workers {
		t.Fatalf("max concurrency = %d, want <= %d", maxSeen, workers)
	}
	if maxSeen < 2 {
		t.Fatalf("max concurrency = %d, want the pool to overlap work", maxSeen)
	}
}

func TestJiraParallelSequentialFallbackKeepsOrder(t *testing.T) {
	t.Parallel()

	var order []int
	jiraParallel(context.Background(), 1, 4, func(_ context.Context, i int) {
		order = append(order, i)
	})
	if len(order) != 4 {
		t.Fatalf("ran %d indices, want 4", len(order))
	}
	for i, got := range order {
		if got != i {
			t.Fatalf("order = %v, want ascending", order)
		}
	}
}

func TestJiraParallelStopsWhenContextCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	var ran atomic.Int32
	jiraParallel(ctx, 2, 1000, func(_ context.Context, _ int) {
		ran.Add(1)
		cancel()
	})
	if ran.Load() >= 1000 {
		t.Fatalf("ran %d tasks after cancellation, want the pool to stop early", ran.Load())
	}
}

// TestWithImportConcurrencyKeepsDefaultAndClamps keeps the pool bounded even
// when an operator supplies an unusable or extreme value.
func TestWithImportConcurrencyKeepsDefaultAndClamps(t *testing.T) {
	t.Parallel()

	handler := NewJiraImportHandler(nil, "jira-concurrency-config-secret", "", blobstore.Disabled())
	if handler.importConcurrency != jiraImportDefaultConcurrency {
		t.Fatalf("default concurrency = %d, want %d", handler.importConcurrency, jiraImportDefaultConcurrency)
	}
	if got := handler.WithImportConcurrency(0).importConcurrency; got != jiraImportDefaultConcurrency {
		t.Fatalf("concurrency after 0 = %d, want the default %d", got, jiraImportDefaultConcurrency)
	}
	if got := handler.WithImportConcurrency(3).importConcurrency; got != 3 {
		t.Fatalf("concurrency after 3 = %d, want 3", got)
	}
	if got := handler.WithImportConcurrency(1000).importConcurrency; got != jiraImportMaxConcurrency {
		t.Fatalf("concurrency after 1000 = %d, want the max %d", got, jiraImportMaxConcurrency)
	}
}

// TestCompleteJiraBatchSubresourcesFoldsFailuresFromEveryIssue proves the
// concurrent batch fan-out still attributes each issue's comment/worklog loss
// exactly once.
func TestCompleteJiraBatchSubresourcesFoldsFailuresFromEveryIssue(t *testing.T) {
	t.Parallel()

	const issueCount = 12
	issues := make([]jira.JiraIssue, issueCount)
	for i := range issues {
		issues[i] = jira.JiraIssue{
			Key: fmt.Sprintf("APP-%d", i+1),
			Fields: jira.JiraIssueFields{
				Comment: &jira.JiraCommentContainer{
					Comments:   []jira.JiraComment{comment("c1")},
					Total:      2,
					MaxResults: 1,
				},
			},
		}
	}
	client := &fakeJiraSubresourceClient{
		getComments: func(int) (*jira.JiraCommentContainer, error) { return nil, errors.New("comments unavailable") },
		getWorklogs: func(int) (*jira.JiraWorklogContainer, error) { return nil, errors.New("unused") },
	}
	im := &jiraImportContext{progress: &ImportProgress{}, client: client}
	handler := &JiraImportHandler{importConcurrency: 4}

	handler.completeJiraBatchSubresources(t.Context(), im, "APP", issues, nil)

	if im.progress.FailedComments != issueCount {
		t.Fatalf("FailedComments = %d, want %d", im.progress.FailedComments, issueCount)
	}
	if im.progress.FailedWorklogs != 0 {
		t.Fatalf("FailedWorklogs = %d, want 0", im.progress.FailedWorklogs)
	}
	for i := range issues {
		if len(issues[i].Fields.Comment.Comments) != 1 || issues[i].Fields.Comment.Total != 2 {
			t.Fatalf("issue %d comment container = %#v, want the embedded page preserved", i, issues[i].Fields.Comment)
		}
	}
}
