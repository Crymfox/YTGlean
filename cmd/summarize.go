package cmd

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/CrymfoxLabs/YTGlean/internal/db"
	"github.com/CrymfoxLabs/YTGlean/internal/summarizer"
	"github.com/spf13/cobra"
)

var summarizeCmd = &cobra.Command{
	Use:   "summarize",
	Short: "Generate a digest summary of recent transcripts",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := db.Open(cfg.Database.Path)
		if err != nil {
			return err
		}
		defer store.Close()

		ctx := cmd.Context()
		sinceStr, _ := cmd.Flags().GetString("since")
		channelFilter, _ := cmd.Flags().GetString("channel")
		customPrompt, _ := cmd.Flags().GetString("prompt")

		since := 24 * time.Hour
		if sinceStr != "" {
			d, err := time.ParseDuration(sinceStr)
			if err != nil {
				return fmt.Errorf("invalid --since duration: %w", err)
			}
			since = d
		}

		if cfg.Summarizer.APIKey == "" && cfg.Summarizer.Endpoint == "https://api.openai.com/v1" {
			return fmt.Errorf("no API key set. Configure summarizer.api_key in ~/.config/ytglean/config.yaml")
		}

		windowEnd := time.Now()
		windowStart := windowEnd.Add(-since)

		transcripts, err := store.GetTranscriptsInWindow(ctx, windowStart, windowEnd, channelFilter)
		if err != nil {
			return err
		}

		if len(transcripts) == 0 {
			fmt.Printf("No transcripts found in the last %s.\n", since)
			return nil
		}

		// Build the combined transcript text
		var combined strings.Builder
		for i, t := range transcripts {
			// Get video info for context
			video, err := store.GetVideo(ctx, t.VideoID)
			if err != nil {
				slog.Warn("could not get video info", "video_id", t.VideoID, "error", err)
			}

			title := t.VideoID
			if video != nil && video.Title != "" {
				title = video.Title
			}

			if i > 0 {
				combined.WriteString("\n\n---\n\n")
			}
			fmt.Fprintf(&combined, "## Video: %s (ID: %s)\n\n", title, t.VideoID)
			if t.ContentText != nil {
				combined.WriteString(*t.ContentText)
			}
		}

		text := combined.String()

		// Rough token estimate: 4 chars per token
		estimatedTokens := len(text) / 4
		slog.Info("summarizing transcripts",
			"count", len(transcripts),
			"chars", len(text),
			"estimated_tokens", estimatedTokens,
			"window", fmt.Sprintf("%s to %s", windowStart.Format("2006-01-02 15:04"), windowEnd.Format("2006-01-02 15:04")),
		)

		s := summarizer.New(cfg.Summarizer.Endpoint, cfg.Summarizer.APIKey, cfg.Summarizer.Model, cfg.Summarizer.MaxTokens)
		result, err := s.Summarize(ctx, text, customPrompt)
		if err != nil {
			return fmt.Errorf("summarization failed: %w", err)
		}

		// Store the digest
		digest := &db.Digest{
			WindowStart:    windowStart,
			WindowEnd:      windowEnd,
			ChannelFilter:  channelFilter,
			Model:          result.Model,
			PromptTemplate: customPrompt,
			DigestText:     result.Summary,
			VideoCount:     len(transcripts),
		}
		if err := store.AddDigest(ctx, digest); err != nil {
			slog.Error("failed to store digest", "error", err)
		}

		fmt.Printf("=== Digest (%d videos, %s window) ===\n\n", len(transcripts), since)
		fmt.Println(result.Summary)
		fmt.Printf("\n--- Model: %s | Tokens: %d prompt, %d completion ---\n",
			result.Model, result.Usage.PromptTokens, result.Usage.CompletionTokens)

		return nil
	},
}

func init() {
	rootCmd.AddCommand(summarizeCmd)

	summarizeCmd.Flags().String("since", "24h", "time window for transcripts (e.g. 24h, 48h, 168h)")
	summarizeCmd.Flags().String("channel", "", "filter to specific channel (ID or name)")
	summarizeCmd.Flags().String("prompt", "", "custom system prompt for summarization")
}
