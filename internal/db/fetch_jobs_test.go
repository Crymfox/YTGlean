package db

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func enqueueOne(t *testing.T, store *Store, videoID string) int64 {
	t.Helper()
	ctx := context.Background()
	n, err := store.EnqueueFetchJobs(ctx, []FetchJob{{VideoID: videoID, ChannelID: "ch1", Title: "t"}})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 inserted, got %d", n)
	}
	jobs, err := store.ListFetchJobs(ctx, "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, j := range jobs {
		if j.VideoID == videoID {
			return j.ID
		}
	}
	t.Fatalf("job for %s not found after enqueue", videoID)
	return 0
}

func TestEnqueueFetchJobsIdempotent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	enqueueOne(t, store, "vid1")
	n, err := store.EnqueueFetchJobs(ctx, []FetchJob{{VideoID: "vid1", ChannelID: "ch1"}})
	if err != nil {
		t.Fatalf("re-enqueue: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 inserted on duplicate, got %d", n)
	}
	jobs, _ := store.ListFetchJobs(ctx, "", 100)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
}

func TestClaimFetchJobsExclusive(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		enqueueOne(t, store, fmt.Sprintf("vid%d", i))
	}

	first, err := store.ClaimFetchJobs(ctx, 2)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("expected 2 claimed, got %d", len(first))
	}
	for _, j := range first {
		if j.State != JobStateInProgress {
			t.Fatalf("claimed job state = %s", j.State)
		}
	}

	second, err := store.ClaimFetchJobs(ctx, 10)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("expected 1 remaining claimable, got %d", len(second))
	}
}

func TestClaimRespectsRetryBackoff(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	id := enqueueOne(t, store, "vid1")
	if _, err := store.ClaimFetchJobs(ctx, 1); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Fail with a long base delay -> next_retry_at in the future -> not claimable
	if err := store.FailFetchJob(ctx, id, "boom", 5, time.Hour); err != nil {
		t.Fatalf("fail: %v", err)
	}
	claimed, _ := store.ClaimFetchJobs(ctx, 10)
	if len(claimed) != 0 {
		t.Fatalf("future-retry job should not be claimable, got %d", len(claimed))
	}

	// Backdate next_retry_at -> claimable
	if _, err := store.DB().Exec("UPDATE fetch_jobs SET next_retry_at = datetime('now', '-1 minute') WHERE id = ?", id); err != nil {
		t.Fatalf("backdating: %v", err)
	}
	claimed, _ = store.ClaimFetchJobs(ctx, 10)
	if len(claimed) != 1 {
		t.Fatalf("due-retry job should be claimable, got %d", len(claimed))
	}
}

func TestFailFetchJobBackoffAndDeadLetter(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	id := enqueueOne(t, store, "vid1")

	// Fail up to max_retries=3
	var prevDelay time.Duration
	for i := 1; i <= 3; i++ {
		if err := store.FailFetchJob(ctx, id, fmt.Sprintf("err %d", i), 3, 30*time.Second); err != nil {
			t.Fatalf("fail %d: %v", i, err)
		}
		jobs, _ := store.ListFetchJobs(ctx, "", 10)
		j := jobs[0]
		if j.RetryCount != i {
			t.Fatalf("attempt %d: retry_count = %d", i, j.RetryCount)
		}
		if i < 3 {
			if j.State != JobStateFailed {
				t.Fatalf("attempt %d: state = %s, want failed", i, j.State)
			}
			if j.NextRetryAt == nil {
				t.Fatalf("attempt %d: next_retry_at is nil", i)
			}
			delay := time.Until(*j.NextRetryAt)
			if delay <= prevDelay {
				t.Fatalf("attempt %d: backoff did not grow (%v <= %v)", i, delay, prevDelay)
			}
			prevDelay = delay
		} else {
			if j.State != JobStateDead {
				t.Fatalf("attempt %d: state = %s, want dead", i, j.State)
			}
		}
	}

	// Dead jobs are not claimable
	claimed, _ := store.ClaimFetchJobs(ctx, 10)
	if len(claimed) != 0 {
		t.Fatalf("dead job should not be claimable")
	}
	if lastErr := func() string {
		jobs, _ := store.ListFetchJobs(ctx, JobStateDead, 10)
		if len(jobs) == 1 && jobs[0].LastError != nil {
			return *jobs[0].LastError
		}
		return ""
	}(); lastErr != "err 3" {
		t.Fatalf("last_error = %q, want %q", lastErr, "err 3")
	}
}

func TestMarkNoTranscriptTerminalAndBlocksReenqueue(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	id := enqueueOne(t, store, "vid1")
	if err := store.MarkFetchJobNoTranscript(ctx, id, "no transcript found"); err != nil {
		t.Fatalf("mark: %v", err)
	}

	if claimed, _ := store.ClaimFetchJobs(ctx, 10); len(claimed) != 0 {
		t.Fatalf("no_transcript job should not be claimable")
	}

	// Re-enqueue attempt is blocked by UNIQUE(video_id)
	n, err := store.EnqueueFetchJobs(ctx, []FetchJob{{VideoID: "vid1", ChannelID: "ch1"}})
	if err != nil {
		t.Fatalf("re-enqueue: %v", err)
	}
	if n != 0 {
		t.Fatalf("no_transcript row should block re-enqueue, inserted %d", n)
	}
}

func TestCompleteFetchJobDeletes(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	id := enqueueOne(t, store, "vid1")
	if err := store.CompleteFetchJob(ctx, id); err != nil {
		t.Fatalf("complete: %v", err)
	}
	jobs, _ := store.ListFetchJobs(ctx, "", 10)
	if len(jobs) != 0 {
		t.Fatalf("expected empty queue after complete, got %d", len(jobs))
	}
}

func TestReclaimStaleFetchJobs(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	staleID := enqueueOne(t, store, "vid1")
	enqueueOne(t, store, "vid2")
	claimed, _ := store.ClaimFetchJobs(ctx, 2)
	if len(claimed) != 2 {
		t.Fatalf("expected 2 claimed, got %d", len(claimed))
	}

	// Backdate only vid1's claim
	if _, err := store.DB().Exec("UPDATE fetch_jobs SET updated_at = datetime('now', '-1 hour') WHERE id = ?", staleID); err != nil {
		t.Fatalf("backdating: %v", err)
	}

	n, err := store.ReclaimStaleFetchJobs(ctx, 15*time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 reclaimed, got %d", n)
	}
	pending, _ := store.ListFetchJobs(ctx, JobStatePending, 10)
	if len(pending) != 1 || pending[0].ID != staleID {
		t.Fatalf("stale job not reclaimed to pending: %+v", pending)
	}
}

func TestReleaseFetchJobs(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	enqueueOne(t, store, "vid1")
	claimed, _ := store.ClaimFetchJobs(ctx, 1)
	if err := store.ReleaseFetchJobs(ctx, []int64{claimed[0].ID}); err != nil {
		t.Fatalf("release: %v", err)
	}
	pending, _ := store.ListFetchJobs(ctx, JobStatePending, 10)
	if len(pending) != 1 {
		t.Fatalf("expected released job pending, got %d", len(pending))
	}
}

func TestRetrySemantics(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// dead job
	deadID := enqueueOne(t, store, "vid-dead")
	_ = store.FailFetchJob(ctx, deadID, "x", 1, time.Second)
	// failed job with future retry
	failedID := enqueueOne(t, store, "vid-failed")
	_ = store.FailFetchJob(ctx, failedID, "y", 5, time.Hour)
	// no_transcript job
	ntID := enqueueOne(t, store, "vid-nt")
	_ = store.MarkFetchJobNoTranscript(ctx, ntID, "none")

	// retry-all touches only failed
	n, err := store.RetryAllFailedFetchJobs(ctx)
	if err != nil {
		t.Fatalf("retry-all: %v", err)
	}
	if n != 1 {
		t.Fatalf("retry-all should affect 1 job, got %d", n)
	}
	pending, _ := store.ListFetchJobs(ctx, JobStatePending, 10)
	if len(pending) != 1 || pending[0].ID != failedID {
		t.Fatalf("retry-all reset wrong jobs: %+v", pending)
	}
	if pending[0].RetryCount != 1 {
		t.Fatalf("retry-all should keep retry_count, got %d", pending[0].RetryCount)
	}

	// explicit retry resurrects dead with fresh budget
	if err := store.RetryFetchJob(ctx, deadID); err != nil {
		t.Fatalf("retry dead: %v", err)
	}
	jobs, _ := store.ListFetchJobs(ctx, JobStatePending, 10)
	found := false
	for _, j := range jobs {
		if j.ID == deadID {
			found = true
			if j.RetryCount != 0 {
				t.Fatalf("explicit retry should reset retry_count, got %d", j.RetryCount)
			}
		}
	}
	if !found {
		t.Fatalf("dead job not resurrected")
	}

	// retry of nonexistent job errors
	if err := store.RetryFetchJob(ctx, 99999); err == nil {
		t.Fatalf("expected error retrying nonexistent job")
	}

	// clear-dead removes no_transcript (dead was resurrected above)
	cleared, err := store.ClearDeadFetchJobs(ctx)
	if err != nil {
		t.Fatalf("clear-dead: %v", err)
	}
	if cleared != 1 {
		t.Fatalf("expected 1 cleared (no_transcript), got %d", cleared)
	}
}

func TestCountFetchJobsByState(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	enqueueOne(t, store, "vid1")
	enqueueOne(t, store, "vid2")
	id := enqueueOne(t, store, "vid3")
	_ = store.MarkFetchJobNoTranscript(ctx, id, "none")

	counts, err := store.CountFetchJobsByState(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts[JobStatePending] != 2 || counts[JobStateNoTranscript] != 1 {
		t.Fatalf("unexpected counts: %v", counts)
	}
}

func TestConcurrentClaimsDisjoint(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		enqueueOne(t, store, fmt.Sprintf("vid%d", i))
	}

	var mu sync.Mutex
	seen := make(map[int64]int)
	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				jobs, err := store.ClaimFetchJobs(ctx, 3)
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				if len(jobs) == 0 {
					return
				}
				mu.Lock()
				for _, j := range jobs {
					seen[j.ID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(seen) != 10 {
		t.Fatalf("expected 10 distinct claims, got %d", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("job %d claimed %d times", id, n)
		}
	}
}
