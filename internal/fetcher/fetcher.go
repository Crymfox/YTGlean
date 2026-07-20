// Package fetcher implements the shared transcript fetch pipeline:
// feed discovery, durable job queueing, and rate-limited queue processing.
// It is used by `ytglean fetch`, `ytglean watch`, `ytglean serve --watch`,
// and the MCP fetch_new tool.
package fetcher

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/CrymfoxLabs/YTGlean/internal/db"
	"github.com/CrymfoxLabs/YTGlean/internal/feed"
	"github.com/CrymfoxLabs/YTGlean/internal/ratelimit"
	"github.com/CrymfoxLabs/YTGlean/internal/transcript"
	"golang.org/x/sync/errgroup"
)

// staleClaimAge is how long an in_progress job may sit untouched before it is
// considered orphaned by a crashed process and reclaimed.
const staleClaimAge = 15 * time.Minute

// maxConcurrentFeedFetches bounds parallel RSS feed requests.
const maxConcurrentFeedFetches = 5

// Config holds fetcher settings.
type Config struct {
	Languages      []string
	MaxConcurrent  int           // parallel transcript workers
	MaxRetries     int           // attempts before dead-letter
	BaseRetryDelay time.Duration // doubles each retry
	FeedCacheTTL   time.Duration // defaults to 1h if zero
}

// Fetcher runs the discovery and queue-processing pipeline.
type Fetcher struct {
	store     *db.Store
	provider  transcript.Provider
	limiter   *ratelimit.Limiter
	feedCache *feed.Cache
	cfg       Config

	// fetchFeed is injectable for tests; defaults to feed.FetchAllVideos.
	fetchFeed func(ctx context.Context, channelID string) ([]feed.Entry, error)
}

// New creates a Fetcher and wires the provider's rate-limit backoff callback.
func New(store *db.Store, provider transcript.Provider, limiter *ratelimit.Limiter, cfg Config) *Fetcher {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 3
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 5
	}
	if cfg.BaseRetryDelay <= 0 {
		cfg.BaseRetryDelay = 30 * time.Second
	}
	if cfg.FeedCacheTTL <= 0 {
		cfg.FeedCacheTTL = 1 * time.Hour
	}

	if dual, ok := provider.(*transcript.DualProvider); ok {
		dual.SetBackoffCallback(limiter.Backoff)
	}

	return &Fetcher{
		store:     store,
		provider:  provider,
		limiter:   limiter,
		feedCache: feed.NewCache(cfg.FeedCacheTTL),
		cfg:       cfg,
		fetchFeed: feed.FetchAllVideos,
	}
}

// Candidate is a discovered video that needs a transcript.
type Candidate struct {
	VideoID     string
	Title       string
	ChannelName string
}

// Result summarizes a fetch run.
type Result struct {
	Discovered   int // feed entries within the window
	Enqueued     int // new jobs inserted
	Claimed      int // jobs processed this run (including due retries)
	Succeeded    int
	NoTranscript int // videos with no transcript available (terminal)
	Failed       int // transient failures scheduled for retry
	Dead         int // jobs that hit max retries this run
}

// Discover fetches channel feeds (cached, rate-limited, parallel), filters out
// videos that already have transcripts, records video rows, and enqueues fetch
// jobs. With dryRun it only reports candidates without writing anything.
func (f *Fetcher) Discover(ctx context.Context, channels []db.Channel, since time.Time, dryRun bool) ([]Candidate, int, error) {
	type channelFeed struct {
		channelID string
		entries   []feed.Entry
	}

	var (
		feedResults []channelFeed
		feedMu      sync.Mutex
	)

	g, gCtx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, maxConcurrentFeedFetches)

	for _, ch := range channels {
		ch := ch
		g.Go(func() error {
			sem <- struct{}{}
			defer func() { <-sem }()

			slog.Info("checking feed", "channel", ch.Name, "id", ch.ChannelID)

			// The cache stores the full (unfiltered) feed; the since filter
			// is applied below so different windows share cache entries.
			entries, ok := f.feedCache.Get(ch.ChannelID)
			if ok {
				slog.Debug("using cached feed", "channel", ch.Name, "count", len(entries))
			} else {
				if err := f.limiter.WaitFeed(gCtx); err != nil {
					return err
				}
				var err error
				entries, err = f.fetchFeed(gCtx, ch.ChannelID)
				if err != nil {
					slog.Error("failed to fetch feed", "channel", ch.ChannelID, "error", err)
					return nil // don't abort other fetches
				}
				f.feedCache.Set(ch.ChannelID, entries)
			}

			var filtered []feed.Entry
			for _, e := range entries {
				if e.Published.Before(since) {
					continue
				}
				filtered = append(filtered, e)
			}

			feedMu.Lock()
			feedResults = append(feedResults, channelFeed{channelID: ch.ChannelID, entries: filtered})
			feedMu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, 0, err
	}

	// Batch DB checks
	var allVideoIDs []string
	for _, result := range feedResults {
		for _, entry := range result.entries {
			allVideoIDs = append(allVideoIDs, entry.VideoID)
		}
	}
	if len(allVideoIDs) == 0 {
		return nil, 0, nil
	}

	existingTranscripts, err := f.store.HasAnyTranscripts(ctx, allVideoIDs)
	if err != nil {
		slog.Error("batch checking transcripts", "error", err)
		// Continue anyway — enqueue is idempotent
	}

	var candidates []Candidate
	var jobs []db.FetchJob
	for _, result := range feedResults {
		for _, entry := range result.entries {
			if existingTranscripts[entry.VideoID] {
				slog.Debug("skipping, transcript exists", "video", entry.VideoID)
				continue
			}
			candidates = append(candidates, Candidate{
				VideoID:     entry.VideoID,
				Title:       entry.Title,
				ChannelName: entry.ChannelName,
			})
			if dryRun {
				continue
			}
			// Record the video at discovery time so jobs don't carry metadata
			pubTime := entry.Published
			if err := f.store.AddVideo(ctx, entry.VideoID, result.channelID, entry.Title, &pubTime); err != nil {
				slog.Error("adding video record", "video", entry.VideoID, "error", err)
				continue
			}
			jobs = append(jobs, db.FetchJob{
				VideoID:   entry.VideoID,
				ChannelID: result.channelID,
				Title:     entry.Title,
			})
		}
		if !dryRun {
			_ = f.store.UpdateLastChecked(ctx, result.channelID)
		}
	}

	enqueued := 0
	if !dryRun && len(jobs) > 0 {
		enqueued, err = f.store.EnqueueFetchJobs(ctx, jobs)
		if err != nil {
			return candidates, enqueued, err
		}
	}
	return candidates, enqueued, nil
}

// Process drains the job queue: reclaims stale claims, then repeatedly claims
// batches and fetches transcripts with bounded concurrency until nothing is
// claimable. On context cancellation, unfinished claims are released back to
// pending.
func (f *Fetcher) Process(ctx context.Context) (Result, error) {
	var res Result

	if n, err := f.store.ReclaimStaleFetchJobs(ctx, staleClaimAge); err != nil {
		slog.Error("reclaiming stale jobs", "error", err)
	} else if n > 0 {
		slog.Info("reclaimed stale jobs", "count", n)
	}

	var (
		mu         sync.Mutex
		unfinished = make(map[int64]bool)
	)

	// Release any claims still unfinished when we exit (cancellation path).
	defer func() {
		mu.Lock()
		var ids []int64
		for id := range unfinished {
			ids = append(ids, id)
		}
		mu.Unlock()
		if len(ids) == 0 {
			return
		}
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := f.store.ReleaseFetchJobs(releaseCtx, ids); err != nil {
			slog.Error("releasing unfinished jobs", "error", err)
		} else {
			slog.Info("released unfinished jobs to pending", "count", len(ids))
		}
	}()

	for {
		if err := ctx.Err(); err != nil {
			return res, err
		}

		jobs, err := f.store.ClaimFetchJobs(ctx, f.cfg.MaxConcurrent)
		if err != nil {
			return res, err
		}
		if len(jobs) == 0 {
			return res, nil
		}
		res.Claimed += len(jobs)

		mu.Lock()
		for _, j := range jobs {
			unfinished[j.ID] = true
		}
		mu.Unlock()

		g, gCtx := errgroup.WithContext(ctx)
		for _, job := range jobs {
			job := job
			g.Go(func() error {
				if err := f.limiter.WaitProvider(gCtx, f.provider.Name()); err != nil {
					return err // ctx cancelled — leave job for the release path
				}

				slog.Info("fetching transcript", "video", job.VideoID, "title", job.Title)

				tx, err := f.provider.FetchTranscript(gCtx, job.VideoID, f.cfg.Languages)
				switch {
				case err == nil:
					if serr := f.store.AddTranscript(gCtx, tx.VideoID, tx.Language, tx.RawJSON, tx.FullText, tx.Provider); serr != nil {
						slog.Error("storing transcript", "video", job.VideoID, "error", serr)
						_ = f.store.FailFetchJob(gCtx, job.ID, serr.Error(), f.cfg.MaxRetries, f.cfg.BaseRetryDelay)
						mu.Lock()
						res.Failed++
						mu.Unlock()
					} else {
						slog.Info("transcript stored", "video", job.VideoID, "provider", tx.Provider,
							"segments", len(tx.Segments), "chars", len(tx.FullText))
						_ = f.store.CompleteFetchJob(gCtx, job.ID)
						mu.Lock()
						res.Succeeded++
						mu.Unlock()
					}
				case transcript.IsPermanentError(err):
					slog.Info("no transcript available", "video", job.VideoID)
					_ = f.store.MarkFetchJobNoTranscript(gCtx, job.ID, err.Error())
					mu.Lock()
					res.NoTranscript++
					mu.Unlock()
				default:
					if gCtx.Err() != nil {
						return gCtx.Err() // cancelled mid-fetch — leave for release
					}
					slog.Error("fetching transcript", "video", job.VideoID, "error", err)
					_ = f.store.FailFetchJob(gCtx, job.ID, err.Error(), f.cfg.MaxRetries, f.cfg.BaseRetryDelay)
					mu.Lock()
					if job.RetryCount+1 >= f.cfg.MaxRetries {
						res.Dead++
					} else {
						res.Failed++
					}
					mu.Unlock()
				}

				mu.Lock()
				delete(unfinished, job.ID)
				mu.Unlock()
				return nil
			})
		}

		if err := g.Wait(); err != nil {
			return res, err
		}
	}
}

// Run performs discovery followed by queue processing.
func (f *Fetcher) Run(ctx context.Context, channels []db.Channel, since time.Time) (Result, error) {
	candidates, enqueued, err := f.Discover(ctx, channels, since, false)
	if err != nil {
		return Result{}, err
	}
	res, err := f.Process(ctx)
	res.Discovered = len(candidates)
	res.Enqueued = enqueued
	return res, err
}
