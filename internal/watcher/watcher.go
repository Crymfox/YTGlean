// Package watcher runs the continuous fetch (and optional auto-summarize)
// loop used by `ytglean watch` and `ytglean serve --watch`.
package watcher

import (
	"context"
	"log/slog"
	"time"

	"github.com/CrymfoxLabs/YTGlean/internal/fetcher"
)

// Watcher runs fetch cycles at a fixed interval and optionally triggers
// summarization once enough new transcripts have accumulated. The function
// fields keep it decoupled from concrete fetcher/digest types for testing.
type Watcher struct {
	Interval time.Duration

	// Fetch runs one discovery+process cycle.
	Fetch func(ctx context.Context) (fetcher.Result, error)

	// Summarize generates a digest for the given window. Nil disables
	// auto-summarize.
	Summarize func(ctx context.Context, window time.Duration) error

	// SummarizeThreshold is the minimum number of new transcripts
	// accumulated across cycles before Summarize is called.
	SummarizeThreshold int
}

// Run executes the watch loop until ctx is cancelled. The first cycle runs
// immediately. Per-cycle errors are logged, never fatal. Returns nil on
// graceful shutdown.
func (w *Watcher) Run(ctx context.Context) error {
	slog.Info("watcher started", "interval", w.Interval, "auto_summarize", w.Summarize != nil)

	newSinceSummarize := 0
	lastSummarized := time.Now()

	cycle := func() {
		res, err := w.Fetch(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("fetch cycle failed", "error", err)
			return
		}
		slog.Info("fetch cycle complete",
			"discovered", res.Discovered, "succeeded", res.Succeeded,
			"no_transcript", res.NoTranscript, "failed", res.Failed, "dead", res.Dead)

		newSinceSummarize += res.Succeeded
		if w.Summarize == nil || newSinceSummarize < w.SummarizeThreshold {
			return
		}

		window := time.Since(lastSummarized)
		if err := w.Summarize(ctx, window); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("auto-summarize failed", "error", err)
			return
		}
		slog.Info("auto-summarize complete", "transcripts", newSinceSummarize, "window", window)
		newSinceSummarize = 0
		lastSummarized = time.Now()
	}

	cycle() // first cycle runs immediately

	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("watcher stopped")
			return nil
		case <-ticker.C:
			cycle()
		}
	}
}
