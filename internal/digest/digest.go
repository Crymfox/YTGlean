// Package digest generates and stores LLM digest summaries of transcripts.
// Shared by `ytglean summarize` and the watch loop's auto-summarize.
package digest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/CrymfoxLabs/YTGlean/internal/config"
	"github.com/CrymfoxLabs/YTGlean/internal/db"
	"github.com/CrymfoxLabs/YTGlean/internal/summarizer"
)

// Options controls what gets summarized.
type Options struct {
	Since    time.Duration // window ending now (ignored in single-video mode)
	Channel  string        // channel ID filter ("" = all)
	VideoID  string        // single-video mode
	Language string        // transcript language for single-video mode
	Prompt   string        // custom system prompt
	Force    bool          // skip same-video-set dedup
}

// Result reports the outcome of a digest generation.
type Result struct {
	Skipped       bool
	NoTranscripts bool // true when skipped because the window had no transcripts
	SkipReason    string
	Digest        *db.Digest
	Summary       *summarizer.Result
}

// Generate selects transcripts, dedups against the latest digest, calls the
// summarizer, and stores the digest. Returns a skipped Result (not an error)
// when there is nothing new to summarize.
func Generate(ctx context.Context, store *db.Store, cfg config.SummarizerConfig, opts Options) (*Result, error) {
	windowEnd := time.Now()
	windowStart := windowEnd.Add(-opts.Since)

	var transcripts []db.Transcript
	if opts.VideoID != "" {
		t, err := store.GetTranscript(ctx, opts.VideoID, opts.Language)
		if err != nil || t == nil {
			return nil, fmt.Errorf("no transcript found for video %s", opts.VideoID)
		}
		transcripts = append(transcripts, *t)
	} else {
		var err error
		transcripts, err = store.GetTranscriptsInWindow(ctx, windowStart, windowEnd, opts.Channel)
		if err != nil {
			return nil, err
		}
	}

	if len(transcripts) == 0 {
		return &Result{Skipped: true, NoTranscripts: true, SkipReason: "no transcripts found in window"}, nil
	}

	// Collect current video IDs and sort for stable comparison
	currentVideoIDs := make([]string, 0, len(transcripts))
	for _, t := range transcripts {
		currentVideoIDs = append(currentVideoIDs, t.VideoID)
	}
	sort.Strings(currentVideoIDs)
	currentJSON, _ := json.Marshal(currentVideoIDs)

	// Check for existing digest with same video set
	if !opts.Force && opts.VideoID == "" {
		latestDigest, err := store.GetLatestDigest(ctx, opts.Channel)
		if err != nil {
			slog.Warn("could not check existing digest", "error", err)
		}
		if latestDigest != nil {
			var existingIDs []string
			if err := json.Unmarshal([]byte(latestDigest.VideoIDs), &existingIDs); err == nil {
				sort.Strings(existingIDs)
				existingJSON, _ := json.Marshal(existingIDs)
				if string(existingJSON) == string(currentJSON) {
					return &Result{
						Skipped:    true,
						SkipReason: fmt.Sprintf("already summarized these %d videos", len(transcripts)),
					}, nil
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

	slog.Info("summarizing transcripts",
		"count", len(transcripts),
		"chars", len(text),
		"estimated_tokens", len(text)/4,
		"window", fmt.Sprintf("%s to %s", windowStart.Format("2006-01-02 15:04"), windowEnd.Format("2006-01-02 15:04")),
	)

	s := summarizer.New(cfg.Endpoint, cfg.APIKey, cfg.Model, cfg.MaxTokens)
	result, err := s.Summarize(ctx, text, opts.Prompt)
	if err != nil {
		return nil, fmt.Errorf("summarization failed: %w", err)
	}

	d := &db.Digest{
		WindowStart:    windowStart,
		WindowEnd:      windowEnd,
		ChannelFilter:  opts.Channel,
		Model:          result.Model,
		PromptTemplate: opts.Prompt,
		DigestText:     result.Summary,
		VideoCount:     len(transcripts),
		VideoIDs:       string(currentJSON),
	}
	if err := store.AddDigest(ctx, d); err != nil {
		slog.Error("failed to store digest", "error", err)
	}

	return &Result{Digest: d, Summary: result}, nil
}
