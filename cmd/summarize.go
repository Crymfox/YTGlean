package cmd

import (
	"fmt"
	"time"

	"github.com/CrymfoxLabs/YTGlean/internal/db"
	"github.com/CrymfoxLabs/YTGlean/internal/digest"
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

		res, err := digest.Generate(ctx, store, cfg.Summarizer, digest.Options{
			Since:    since,
			Channel:  channelFilter,
			VideoID:  videoID,
			Language: cfg.Transcript.Languages[0],
			Prompt:   customPrompt,
			Force:    reSummarize,
		})
		if err != nil {
			return err
		}

		if res.Skipped {
			if res.NoTranscripts {
				fmt.Printf("No transcripts found in the last %s.\n", since)
			} else {
				fmt.Printf("%s. Use --re-summarize to force.\n", res.SkipReason)
			}
			return nil
		}

		fmt.Printf("=== Digest (%d videos, %s window) ===\n\n", res.Digest.VideoCount, since)
		fmt.Println(res.Summary.Summary)
		fmt.Printf("\n--- Model: %s | Tokens: %d prompt, %d completion ---\n",
			res.Summary.Model, res.Summary.Usage.PromptTokens, res.Summary.Usage.CompletionTokens)

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
