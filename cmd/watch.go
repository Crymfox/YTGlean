package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/CrymfoxLabs/YTGlean/internal/db"
	"github.com/CrymfoxLabs/YTGlean/internal/digest"
	"github.com/CrymfoxLabs/YTGlean/internal/fetcher"
	"github.com/CrymfoxLabs/YTGlean/internal/watcher"
	"github.com/spf13/cobra"
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Continuously fetch new transcripts (and optionally summarize)",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := db.Open(cfg.Database.Path)
		if err != nil {
			return err
		}
		defer store.Close()

		interval, _ := cmd.Flags().GetDuration("fetch-interval")
		autoSummarize, _ := cmd.Flags().GetBool("auto-summarize")
		threshold, _ := cmd.Flags().GetInt("summarize-threshold")
		summarizeChannel, _ := cmd.Flags().GetString("summarize-channel")
		channelFilter, _ := cmd.Flags().GetString("channel")
		sinceStr, _ := cmd.Flags().GetString("since")

		since := 24 * time.Hour
		if sinceStr != "" {
			d, err := time.ParseDuration(sinceStr)
			if err != nil {
				return fmt.Errorf("invalid --since duration: %w", err)
			}
			since = d
		}

		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		w, err := buildWatcher(ctx, store, watcherOptions{
			interval:         interval,
			since:            since,
			channelFilter:    channelFilter,
			autoSummarize:    autoSummarize,
			threshold:        threshold,
			summarizeChannel: summarizeChannel,
		})
		if err != nil {
			return err
		}

		return w.Run(ctx)
	},
}

type watcherOptions struct {
	interval         time.Duration
	since            time.Duration
	channelFilter    string
	autoSummarize    bool
	threshold        int
	summarizeChannel string
}

// buildWatcher wires a Watcher over a fetcher and (optionally) the digest
// generator. Shared by `watch` and `serve --watch`.
func buildWatcher(ctx context.Context, store *db.Store, opts watcherOptions) (*watcher.Watcher, error) {
	return buildWatcherWithFetcher(ctx, store, newFetcher(store), opts)
}

func buildWatcherWithFetcher(ctx context.Context, store *db.Store, f *fetcher.Fetcher, opts watcherOptions) (*watcher.Watcher, error) {
	w := &watcher.Watcher{
		Interval:           opts.interval,
		SummarizeThreshold: opts.threshold,
		Fetch: func(ctx context.Context) (fetcher.Result, error) {
			// Re-list channels each cycle so newly added ones are picked up
			channels, err := store.ListChannels(ctx)
			if err != nil {
				return fetcher.Result{}, err
			}
			if opts.channelFilter != "" {
				var filtered []db.Channel
				for _, ch := range channels {
					if ch.ChannelID == opts.channelFilter || ch.Name == opts.channelFilter {
						filtered = append(filtered, ch)
					}
				}
				channels = filtered
			}
			if len(channels) == 0 {
				return fetcher.Result{}, nil
			}
			return f.Run(ctx, channels, time.Now().Add(-opts.since))
		},
	}

	if opts.autoSummarize {
		if cfg.Summarizer.APIKey == "" && cfg.Summarizer.Endpoint == "https://api.openai.com/v1" {
			return nil, fmt.Errorf("auto-summarize requires summarizer.api_key in config")
		}
		// Resolve summarize channel name -> ID once at startup
		summarizeChannelID := opts.summarizeChannel
		if summarizeChannelID != "" {
			ch, err := store.GetChannelByID(ctx, summarizeChannelID)
			if err == nil && ch == nil {
				ch, err = store.GetChannelByName(ctx, summarizeChannelID)
			}
			if err != nil {
				return nil, err
			}
			if ch == nil {
				return nil, fmt.Errorf("summarize channel %q not found", opts.summarizeChannel)
			}
			summarizeChannelID = ch.ChannelID
		}
		w.Summarize = func(ctx context.Context, window time.Duration) error {
			_, err := digest.Generate(ctx, store, cfg.Summarizer, digest.Options{
				Since:   window,
				Channel: summarizeChannelID,
			})
			return err
		}
	}

	return w, nil
}

func init() {
	rootCmd.AddCommand(watchCmd)

	watchCmd.Flags().Duration("fetch-interval", 0, "how often to check for new videos (default from config, 30m)")
	watchCmd.Flags().Bool("auto-summarize", false, "auto-summarize after enough new transcripts")
	watchCmd.Flags().Int("summarize-threshold", 0, "min new transcripts before auto-summarize (default from config, 5)")
	watchCmd.Flags().String("summarize-channel", "", "channel filter for auto-summarize digests")
	watchCmd.Flags().String("channel", "", "watch only this channel (ID or name)")
	watchCmd.Flags().String("since", "24h", "discovery window per cycle (e.g. 24h)")

	// Config-backed defaults resolved in PersistentPreRunE ordering: flags
	// with zero values fall back to config in PreRunE below.
	watchCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if v, _ := cmd.Flags().GetDuration("fetch-interval"); v == 0 {
			_ = cmd.Flags().Set("fetch-interval", cfg.Watch.FetchInterval.String())
		}
		if v, _ := cmd.Flags().GetInt("summarize-threshold"); v == 0 {
			_ = cmd.Flags().Set("summarize-threshold", fmt.Sprintf("%d", cfg.Watch.SummarizeThreshold))
		}
		if !cmd.Flags().Changed("auto-summarize") && cfg.Watch.AutoSummarize {
			_ = cmd.Flags().Set("auto-summarize", "true")
		}
		if v, _ := cmd.Flags().GetString("summarize-channel"); v == "" && cfg.Watch.SummarizeChannel != "" {
			_ = cmd.Flags().Set("summarize-channel", cfg.Watch.SummarizeChannel)
		}
		return nil
	}
}
