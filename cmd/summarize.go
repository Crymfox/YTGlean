package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
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
		reSummarize, _ := cmd.Flags().GetBool("re-summarize")
		videoID, _ := cmd.Flags().GetString("video")

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

		var transcripts []db.Transcript
		if videoID != "" {
			// Single video mode
			t, err := store.GetTranscript(ctx, videoID, cfg.Transcript.Languages[0])
			if err != nil || t == nil {
				return fmt.Errorf("no transcript found for video %s", videoID)
			}
			transcripts = append(transcripts, *t)
		} else {
			transcripts, err = store.GetTranscriptsInWindow(ctx, windowStart, windowEnd, channelFilter)
			if err != nil {
				return err
			}
		}

		if len(transcripts) == 0 {
			fmt.Printf("No transcripts found in the last %s.\n", since)
			return nil
		}

		// Collect current video IDs and sort for stable comparison
		currentVideoIDs := make([]string, 0, len(transcripts))
		for _, t := range transcripts {
			currentVideoIDs = append(currentVideoIDs, t.VideoID)
		}
		sort.Strings(currentVideoIDs)
		currentJSON, _ := json.Marshal(currentVideoIDs)

		// Check for existing digest with same video set
		if !reSummarize && videoID == "" {
			latestDigest, err := store.GetLatestDigest(ctx, channelFilter)
			if err != nil {
				slog.Warn("could not check existing digest", "error", err)
			}
			if latestDigest != nil {
				var existingIDs []string
				if err := json.Unmarshal([]byte(latestDigest.VideoIDs), &existingIDs); err == nil {
					sort.Strings(existingIDs)
					existingJSON, _ := json.Marshal(existingIDs)
					if string(existingJSON) == string(currentJSON) {
						fmt.Printf("Already summarized these %d videos. Use --re-summarize to force.\n", len(transcripts))
						return nil
					}
				}
			}
		}

		// Build the combined transcript text
		var combined strings.Builder
		for i, t := range transcripts {
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
			VideoIDs:       string(currentJSON),
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
	summarizeCmd.Flags().Bool("re-summarize", false, "force re-summarize even if video set hasn't changed")
	summarizeCmd.Flags().String("video", "", "summarize a specific video by ID")
}
