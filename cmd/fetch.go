package cmd

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/CrymfoxLabs/YTGlean/internal/db"
	"github.com/CrymfoxLabs/YTGlean/internal/feed"
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

		// Discover new videos from feeds
		type videoJob struct {
			entry     feed.Entry
			channelID string
		}
		var jobs []videoJob

		for _, ch := range channels {
			slog.Info("checking feed", "channel", ch.Name, "id", ch.ChannelID)
			entries, err := feed.FetchNewVideos(ctx, ch.ChannelID, sinceTime)
			if err != nil {
				slog.Error("failed to fetch feed", "channel", ch.ChannelID, "error", err)
				continue
			}

			for _, entry := range entries {
				has, err := store.HasVideo(ctx, entry.VideoID)
				if err != nil {
					slog.Error("checking video existence", "video", entry.VideoID, "error", err)
					continue
				}
				if has {
					// Check if we already have a transcript
					hasTx, _ := store.HasTranscript(ctx, entry.VideoID, cfg.Transcript.Languages[0])
					if hasTx {
						slog.Debug("skipping, transcript exists", "video", entry.VideoID)
						continue
					}
				}
				jobs = append(jobs, videoJob{entry: entry, channelID: ch.ChannelID})
			}

			_ = store.UpdateLastChecked(ctx, ch.ChannelID)
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
		sem := make(chan struct{}, cfg.Transcript.MaxConcurrent)
		g, gCtx := errgroup.WithContext(ctx)

		var successCount, failCount int

		for _, job := range jobs {
			job := job
			g.Go(func() error {
				sem <- struct{}{}
				defer func() { <-sem }()

				// Delay between fetches to avoid rate limiting
				if cfg.Transcript.FetchDelay > 0 {
					time.Sleep(cfg.Transcript.FetchDelay)
				}

				slog.Info("fetching transcript", "video", job.entry.VideoID, "title", job.entry.Title)

				// Ensure the video record exists
				pubTime := job.entry.Published
				if err := store.AddVideo(gCtx, job.entry.VideoID, job.channelID, job.entry.Title, &pubTime); err != nil {
					slog.Error("adding video record", "video", job.entry.VideoID, "error", err)
					failCount++
					return nil // don't abort other fetches
				}

				tx, err := provider.FetchTranscript(gCtx, job.entry.VideoID, cfg.Transcript.Languages)
				if err != nil {
					slog.Error("fetching transcript", "video", job.entry.VideoID, "error", err)
					failCount++
					return nil
				}

				if err := store.AddTranscript(gCtx, tx.VideoID, tx.Language, tx.RawJSON, tx.FullText, tx.Provider); err != nil {
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

		if err := g.Wait(); err != nil {
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
