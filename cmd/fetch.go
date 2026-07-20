package cmd

import (
	"fmt"
	"time"

	"github.com/CrymfoxLabs/YTGlean/internal/db"
	"github.com/CrymfoxLabs/YTGlean/internal/fetcher"
	"github.com/CrymfoxLabs/YTGlean/internal/ratelimit"
	"github.com/CrymfoxLabs/YTGlean/internal/transcript"
	"github.com/spf13/cobra"
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

		f := newFetcher(store)

		if dryRun {
			candidates, _, err := f.Discover(ctx, channels, sinceTime, true)
			if err != nil {
				return err
			}
			if len(candidates) == 0 {
				fmt.Println("No new videos found.")
				return nil
			}
			fmt.Printf("Would fetch transcripts for %d video(s):\n", len(candidates))
			for _, c := range candidates {
				fmt.Printf("  %s  %s  (%s)\n", c.VideoID, c.Title, c.ChannelName)
			}
			return nil
		}

		res, err := f.Run(ctx, channels, sinceTime)
		if err != nil {
			return err
		}

		if res.Claimed == 0 {
			fmt.Println("No new videos found.")
			return nil
		}
		fmt.Printf("Done: %d succeeded, %d no transcript, %d retrying later, %d dead\n",
			res.Succeeded, res.NoTranscript, res.Failed, res.Dead)
		if res.Failed > 0 || res.Dead > 0 {
			fmt.Println("Use 'ytglean queue list' to inspect failed jobs.")
		}
		return nil
	},
}

// newFetcher builds a Fetcher from the loaded config.
func newFetcher(store *db.Store) *fetcher.Fetcher {
	provider := transcript.NewProvider(cfg.Transcript.Provider, cfg.Transcript.CookieFile)
	limiter := ratelimit.New(cfg.RateLimit)
	return fetcher.New(store, provider, limiter, fetcher.Config{
		Languages:      cfg.Transcript.Languages,
		MaxConcurrent:  cfg.Transcript.MaxConcurrent,
		MaxRetries:     cfg.Fetch.MaxRetries,
		BaseRetryDelay: cfg.Fetch.BaseRetryDelay,
	})
}

func init() {
	rootCmd.AddCommand(fetchCmd)

	fetchCmd.Flags().String("channel", "", "fetch only from this channel (ID or name)")
	fetchCmd.Flags().String("since", "24h", "time window for new videos (e.g. 24h, 168h)")
	fetchCmd.Flags().Bool("all", false, "fetch all videos in feed (ignore time filter)")
	fetchCmd.Flags().Bool("dry-run", false, "show what would be fetched without fetching")
}
