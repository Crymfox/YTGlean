package fetcher

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/CrymfoxLabs/YTGlean/internal/db"
	"github.com/CrymfoxLabs/YTGlean/internal/feed"
	"github.com/CrymfoxLabs/YTGlean/internal/ratelimit"
	"github.com/CrymfoxLabs/YTGlean/internal/transcript"
)

// fakeProvider returns canned results per video ID.
type fakeProvider struct {
	mu      sync.Mutex
	results map[string]error // videoID -> error (nil = success)
	calls   map[string]int
	block   chan struct{} // if set, FetchTranscript blocks until closed
}

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) FetchTranscript(ctx context.Context, videoID string, languages []string) (*transcript.Transcript, error) {
	if p.block != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.block:
		}
	}
	p.mu.Lock()
	p.calls[videoID]++
	err := p.results[videoID]
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &transcript.Transcript{
		VideoID:  videoID,
		Language: "en",
		FullText: "text for " + videoID,
		Provider: "fake",
	}, nil
}

func newTestFetcher(t *testing.T, results map[string]error, entries map[string][]feed.Entry) (*Fetcher, *db.Store, *fakeProvider) {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	provider := &fakeProvider{results: results, calls: make(map[string]int)}
	limiter := ratelimit.New(ratelimit.DefaultConfig())
	f := New(store, provider, limiter, Config{
		Languages:      []string{"en"},
		MaxConcurrent:  2,
		MaxRetries:     3,
		BaseRetryDelay: time.Second,
	})
	f.fetchFeed = func(ctx context.Context, channelID string) ([]feed.Entry, error) {
		return entries[channelID], nil
	}
	return f, store, provider
}

func addChannel(t *testing.T, store *db.Store, id string) db.Channel {
	t.Helper()
	if err := store.AddChannel(context.Background(), id, "Channel "+id, ""); err != nil {
		t.Fatalf("adding channel: %v", err)
	}
	return db.Channel{ChannelID: id, Name: "Channel " + id}
}

func TestDiscoverEnqueuesAndDedupes(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	entries := map[string][]feed.Entry{
		"ch1": {
			{VideoID: "new1", Title: "New 1", Published: now},
			{VideoID: "old1", Title: "Old", Published: now.Add(-48 * time.Hour)},
			{VideoID: "have1", Title: "Have", Published: now},
		},
	}
	f, store, _ := newTestFetcher(t, nil, entries)
	ch := addChannel(t, store, "ch1")

	// Pre-existing transcript for have1
	pub := now
	_ = store.AddVideo(ctx, "have1", "ch1", "Have", &pub)
	_ = store.AddTranscript(ctx, "have1", "en", "", "existing", "test")

	candidates, enqueued, err := f.Discover(ctx, []db.Channel{ch}, now.Add(-24*time.Hour), false)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(candidates) != 1 || candidates[0].VideoID != "new1" {
		t.Fatalf("candidates = %+v, want just new1", candidates)
	}
	if enqueued != 1 {
		t.Fatalf("enqueued = %d, want 1", enqueued)
	}

	// Video row recorded at discovery
	v, _ := store.GetVideo(ctx, "new1")
	if v == nil {
		t.Fatalf("video row not created at discovery")
	}

	// Second discover: cached feed, job already queued -> no new enqueue
	_, enqueued, err = f.Discover(ctx, []db.Channel{ch}, now.Add(-24*time.Hour), false)
	if err != nil {
		t.Fatalf("second discover: %v", err)
	}
	if enqueued != 0 {
		t.Fatalf("second enqueue = %d, want 0", enqueued)
	}
}

func TestDiscoverDryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	entries := map[string][]feed.Entry{
		"ch1": {{VideoID: "new1", Title: "New 1", Published: now}},
	}
	f, store, _ := newTestFetcher(t, nil, entries)
	ch := addChannel(t, store, "ch1")

	candidates, enqueued, err := f.Discover(ctx, []db.Channel{ch}, now.Add(-time.Hour), true)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(candidates) != 1 || enqueued != 0 {
		t.Fatalf("dry run: candidates=%d enqueued=%d", len(candidates), enqueued)
	}
	if v, _ := store.GetVideo(ctx, "new1"); v != nil {
		t.Fatalf("dry run created a video row")
	}
	if jobs, _ := store.ListFetchJobs(ctx, "", 10); len(jobs) != 0 {
		t.Fatalf("dry run enqueued jobs: %+v", jobs)
	}
}

func TestProcessSuccess(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	entries := map[string][]feed.Entry{
		"ch1": {{VideoID: "vid1", Title: "V1", Published: now}},
	}
	f, store, _ := newTestFetcher(t, map[string]error{"vid1": nil}, entries)
	ch := addChannel(t, store, "ch1")

	res, err := f.Run(ctx, []db.Channel{ch}, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Succeeded != 1 || res.Claimed != 1 {
		t.Fatalf("res = %+v", res)
	}
	// Transcript stored, job deleted
	tr, _ := store.GetTranscript(ctx, "vid1", "en")
	if tr == nil {
		t.Fatalf("transcript not stored")
	}
	if jobs, _ := store.ListFetchJobs(ctx, "", 10); len(jobs) != 0 {
		t.Fatalf("job not deleted after success: %+v", jobs)
	}
}

func TestProcessPermanentError(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	entries := map[string][]feed.Entry{
		"ch1": {{VideoID: "vid1", Title: "V1", Published: now}},
	}
	f, store, provider := newTestFetcher(t,
		map[string]error{"vid1": errors.New("no transcript found for video")}, entries)
	ch := addChannel(t, store, "ch1")

	res, err := f.Run(ctx, []db.Channel{ch}, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.NoTranscript != 1 {
		t.Fatalf("res = %+v, want NoTranscript=1", res)
	}
	jobs, _ := store.ListFetchJobs(ctx, db.JobStateNoTranscript, 10)
	if len(jobs) != 1 {
		t.Fatalf("expected no_transcript job, got %+v", jobs)
	}
	if provider.calls["vid1"] != 1 {
		t.Fatalf("permanent error should not be retried within run, calls = %d", provider.calls["vid1"])
	}
}

func TestProcessTransientError(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	entries := map[string][]feed.Entry{
		"ch1": {{VideoID: "vid1", Title: "V1", Published: now}},
	}
	f, store, provider := newTestFetcher(t,
		map[string]error{"vid1": errors.New("connection reset")}, entries)
	ch := addChannel(t, store, "ch1")

	res, err := f.Run(ctx, []db.Channel{ch}, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Failed != 1 {
		t.Fatalf("res = %+v, want Failed=1", res)
	}
	jobs, _ := store.ListFetchJobs(ctx, db.JobStateFailed, 10)
	if len(jobs) != 1 || jobs[0].NextRetryAt == nil {
		t.Fatalf("expected failed job with next_retry_at, got %+v", jobs)
	}
	// Backoff not yet due -> single attempt within this run
	if provider.calls["vid1"] != 1 {
		t.Fatalf("transient failure re-claimed within same run, calls = %d", provider.calls["vid1"])
	}
}

func TestProcessCancelReleasesClaims(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	entries := map[string][]feed.Entry{
		"ch1": {{VideoID: "vid1", Title: "V1", Published: now}},
	}
	f, store, provider := newTestFetcher(t, map[string]error{"vid1": nil}, entries)
	provider.block = make(chan struct{}) // never closed — worker blocks until cancel
	ch := addChannel(t, store, "ch1")

	if _, _, err := f.Discover(ctx, []db.Channel{ch}, now.Add(-time.Hour), false); err != nil {
		t.Fatalf("discover: %v", err)
	}

	done := make(chan Result, 1)
	go func() {
		res, _ := f.Process(ctx)
		done <- res
	}()

	time.Sleep(100 * time.Millisecond) // let the worker claim and block
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("Process did not return after cancel")
	}

	pending, _ := store.ListFetchJobs(context.Background(), db.JobStatePending, 10)
	if len(pending) != 1 {
		all, _ := store.ListFetchJobs(context.Background(), "", 10)
		t.Fatalf("expected claim released to pending, queue: %+v", all)
	}
}
