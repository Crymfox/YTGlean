package cmd

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/CrymfoxLabs/YTGlean/internal/db"
	"github.com/CrymfoxLabs/YTGlean/internal/feed"
	"github.com/CrymfoxLabs/YTGlean/internal/ratelimit"
	"github.com/CrymfoxLabs/YTGlean/internal/transcript"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

var fetchCmd = &cobra.Command{
	Use:   "fetch",
	Short: "Fetch transcripts for new videos from tracked channels",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := db.Open(cfg.Database.Path)
		if err != nil {
			return err
		}
		defer store.Close()

		ctx := cmd.Context()
		channelFilter, _ := cmd.Flags().GetString("channel")
		sinceStr, _ := cmd.Flags().GetString("since")
		all, _ := cmd.Flags().GetBool("all")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		since := time.Duration(24 * time.Hour)
		if sinceStr != "" {
			d, err := time.ParseDuration(sinceStr)
			if err != nil {
				return fmt.Errorf("invalid --since duration: %w", err)
			}
			since = d
		}
		sinceTime := time.Now().Add(-since)
		if all {
			sinceTime = time.Time{}
		}

		channels, err := store.ListChannels(ctx)
		if err != nil {
			return err
		}
		if len(channels) == 0 {
			fmt.Println("No channels tracked. Use 'ytglean channel add' to add one.")
			return nil
		}

		// Filter to a specific channel if requested
		if channelFilter != "" {
			var filtered []db.Channel
			for _, ch := range channels {
				if ch.ChannelID == channelFilter || ch.Name == channelFilter {
					filtered = append(filtered, ch)
				}
			}
			if len(filtered) == 0 {
				return fmt.Errorf("channel %q not found", channelFilter)
			}
			channels = filtered
		}

		// Initialize rate limiter (used for both feed and transcript fetching)
		limiter := ratelimit.New(cfg.RateLimit)

		// Initialize feed cache (1 hour TTL)
		feedCache := feed.NewCache(1 * time.Hour)

		// Phase 1: Parallel feed discovery with caching
		type channelFeed struct {
			channelID string
			entries   []feed.Entry
		}

		var (
			feedResults []channelFeed
			feedMu      sync.Mutex
		)

		g, gCtx := errgroup.WithContext(ctx)
		sem := make(chan struct{}, 5) // max 5 concurrent feed fetches

		for _, ch := range channels {
			ch := ch
			g.Go(func() error {
				sem <- struct{}{}
				defer func() { <-sem }()

				slog.Info("checking feed", "channel", ch.Name, "id", ch.ChannelID)

				// Check cache first
				if cached, ok := feedCache.Get(ch.ChannelID); ok {
					slog.Debug("using cached feed", "channel", ch.Name, "count", len(cached))
					feedMu.Lock()
					feedResults = append(feedResults, channelFeed{channelID: ch.ChannelID, entries: cached})
					feedMu.Unlock()
					return nil
				}

				// Rate limit feed requests
				if err := limiter.WaitFeed(gCtx); err != nil {
					return err
				}

				entries, err := feed.FetchNewVideos(gCtx, ch.ChannelID, sinceTime)
				if err != nil {
					slog.Error("failed to fetch feed", "channel", ch.ChannelID, "error", err)
					return nil // don't abort other fetches
				}

				// Cache the results
				feedCache.Set(ch.ChannelID, entries)

				feedMu.Lock()
				feedResults = append(feedResults, channelFeed{channelID: ch.ChannelID, entries: entries})
				feedMu.Unlock()
				return nil
			})
		}

		if err := g.Wait(); err != nil {
			return err
		}

		// Phase 2: Collect all video IDs for batch DB check
		type videoJob struct {
			entry     feed.Entry
			channelID string
		}

		var allVideoIDs []string
		var jobsByChannel = make(map[string][]videoJob)

		for _, result := range feedResults {
			for _, entry := range result.entries {
				allVideoIDs = append(allVideoIDs, entry.VideoID)
				jobsByChannel[result.channelID] = append(jobsByChannel[result.channelID], videoJob{
					entry:     entry,
					channelID: result.channelID,
				})
			}
		}

		if len(allVideoIDs) == 0 {
			fmt.Println("No new videos found.")
			return nil
		}

		// Batch check existing videos and transcripts
		existingVideos, err := store.HasVideos(ctx, allVideoIDs)
		if err != nil {
			slog.Error("batch checking videos", "error", err)
			// Continue anyway — will re-add videos
		}

		existingTranscripts, err := store.HasAnyTranscripts(ctx, allVideoIDs)
		if err != nil {
			slog.Error("batch checking transcripts", "error", err)
			// Continue anyway — will try to fetch
		}

		// Filter to only new jobs
		var jobs []videoJob
		for channelID, channelJobs := range jobsByChannel {
			for _, job := range channelJobs {
				if existingVideos[job.entry.VideoID] && existingTranscripts[job.entry.VideoID] {
					slog.Debug("skipping, transcript exists", "video", job.entry.VideoID)
					continue
				}
				jobs = append(jobs, job)
			}
			// Update last checked timestamp
			_ = store.UpdateLastChecked(ctx, channelID)
		}

		if len(jobs) == 0 {
			fmt.Println("No new videos found.")
			return nil
		}

		if dryRun {
			fmt.Printf("Would fetch transcripts for %d video(s):\n", len(jobs))
			for _, j := range jobs {
				fmt.Printf("  %s  %s  (%s)\n", j.entry.VideoID, j.entry.Title, j.entry.ChannelName)
			}
			return nil
		}

		fmt.Printf("Fetching transcripts for %d video(s)...\n", len(jobs))

		provider := transcript.NewProvider(cfg.Transcript.Provider, cfg.Transcript.CookieFile)

		// Wire up rate-limit backoff for dual provider
		if dual, ok := provider.(*transcript.DualProvider); ok {
			dual.SetBackoffCallback(limiter.Backoff)
		}
		sem2 := make(chan struct{}, cfg.Transcript.MaxConcurrent)
		g2, gCtx2 := errgroup.WithContext(ctx)

		var successCount, failCount int

		for _, job := range jobs {
			job := job
			g2.Go(func() error {
				sem2 <- struct{}{}
				defer func() { <-sem2 }()

				slog.Info("fetching transcript", "video", job.entry.VideoID, "title", job.entry.Title)

				// Rate limit based on provider type
				switch {
				case strings.HasPrefix(provider.Name(), "dual(innertube") || provider.Name() == "innertube":
					if err := limiter.WaitInnerTube(gCtx2); err != nil {
						return err
					}
				case provider.Name() == "ytdlp":
					if err := limiter.WaitYtDlp(gCtx2); err != nil {
						return err
					}
				default:
					// Fallback: use fixed delay if configured
					if cfg.Transcript.FetchDelay > 0 {
						time.Sleep(cfg.Transcript.FetchDelay)
					}
				}

				// Ensure the video record exists
				pubTime := job.entry.Published
				if err := store.AddVideo(gCtx2, job.entry.VideoID, job.channelID, job.entry.Title, &pubTime); err != nil {
					slog.Error("adding video record", "video", job.entry.VideoID, "error", err)
					failCount++
					return nil // don't abort other fetches
				}

				tx, err := provider.FetchTranscript(gCtx2, job.entry.VideoID, cfg.Transcript.Languages)
				if err != nil {
					// Use info level for "no transcript" (expected), error level for real failures
					if transcript.IsPermanentError(err) {
						slog.Info("no transcript available", "video", job.entry.VideoID)
					} else {
						slog.Error("fetching transcript", "video", job.entry.VideoID, "error", err)
					}
					failCount++
					return nil
				}

				if err := store.AddTranscript(gCtx2, tx.VideoID, tx.Language, tx.RawJSON, tx.FullText, tx.Provider); err != nil {
					slog.Error("storing transcript", "video", job.entry.VideoID, "error", err)
					failCount++
					return nil
				}

				slog.Info("transcript stored", "video", job.entry.VideoID, "provider", tx.Provider,
					"segments", len(tx.Segments), "chars", len(tx.FullText))
				successCount++
				return nil
			})
		}

		if err := g2.Wait(); err != nil {
			return err
		}

		fmt.Printf("Done: %d succeeded, %d failed\n", successCount, failCount)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(fetchCmd)

	fetchCmd.Flags().String("channel", "", "fetch only from this channel (ID or name)")
	fetchCmd.Flags().String("since", "24h", "time window for new videos (e.g. 24h, 168h)")
	fetchCmd.Flags().Bool("all", false, "fetch all videos in feed (ignore time filter)")
	fetchCmd.Flags().Bool("dry-run", false, "show what would be fetched without fetching")
}
