package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/CrymfoxLabs/YTGlean/internal/channel"
	"github.com/CrymfoxLabs/YTGlean/internal/config"
	"github.com/CrymfoxLabs/YTGlean/internal/db"
	"github.com/CrymfoxLabs/YTGlean/internal/digest"
	"github.com/CrymfoxLabs/YTGlean/internal/fetcher"
	"github.com/CrymfoxLabs/YTGlean/internal/summarizer"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func resolveChannelID(ctx context.Context, store *db.Store, channel string) (string, error) {
	if channel == "" {
		return "", nil
	}
	ch, err := store.GetChannelByID(ctx, channel)
	if err != nil {
		return "", err
	}
	if ch != nil {
		return ch.ChannelID, nil
	}
	ch, err = store.GetChannelByName(ctx, channel)
	if err != nil {
		return "", err
	}
	if ch == nil {
		return "", fmt.Errorf("channel %q not found", channel)
	}
	return ch.ChannelID, nil
}

func truncateToWordBoundary(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	idx := strings.LastIndex(s[:maxLen], " ")
	if idx <= 0 {
		return s[:maxLen] + "..."
	}
	return s[:idx] + "..."
}

// NewServer creates an MCP server with all YTGlean tools registered.
func NewServer(store *db.Store, f *fetcher.Fetcher, languages []string, version string, summarizerCfg *config.SummarizerConfig) *server.MCPServer {
	s := server.NewMCPServer(
		"YTGlean",
		version,
	)

	s.AddTool(
		mcplib.NewTool("list_channels",
			mcplib.WithDescription("List all tracked YouTube channels"),
		),
		listChannelsHandler(store),
	)

	s.AddTool(
		mcplib.NewTool("search_transcripts",
			mcplib.WithDescription("Search across all stored YouTube transcripts"),
			mcplib.WithString("query", mcplib.Required(), mcplib.Description("Search query string")),
			mcplib.WithString("channel", mcplib.Description("Filter by channel ID")),
			mcplib.WithNumber("limit", mcplib.Description("Maximum results to return (default 10)")),
		),
		searchTranscriptsHandler(store),
	)

	s.AddTool(
		mcplib.NewTool("get_transcript",
			mcplib.WithDescription("Get the full transcript for a specific video"),
			mcplib.WithString("video_id", mcplib.Required(), mcplib.Description("YouTube video ID")),
			mcplib.WithString("format", mcplib.Description("Output format: 'text' (default) or 'timestamped'")),
			mcplib.WithString("language", mcplib.Description("Transcript language (default: first configured)")),
			mcplib.WithNumber("max_chars", mcplib.Description("Maximum characters to return (0 = full transcript)")),
		),
		getTranscriptHandler(store, languages),
	)

	s.AddTool(
		mcplib.NewTool("get_recent_videos",
			mcplib.WithDescription("List recent videos from tracked channels"),
			mcplib.WithString("channel", mcplib.Description("Filter by channel ID")),
			mcplib.WithString("since", mcplib.Description("Time window, e.g. '24h', '168h' (default: '24h')")),
			mcplib.WithNumber("limit", mcplib.Description("Maximum results (default 50)")),
		),
		getRecentVideosHandler(store),
	)

	s.AddTool(
		mcplib.NewTool("fetch_new",
			mcplib.WithDescription("Fetch transcripts for new videos from tracked channels"),
			mcplib.WithString("channel", mcplib.Description("Filter to a specific channel ID or name")),
			mcplib.WithString("since", mcplib.Description("Time window, e.g. '24h' (default: '24h')")),
			mcplib.WithBoolean("all", mcplib.Description("Fetch all videos in feed, ignoring time filter")),
			mcplib.WithBoolean("dry_run", mcplib.Description("Preview what would be fetched without fetching")),
		),
		fetchNewHandler(store, f),
	)

	s.AddTool(
		mcplib.NewTool("list_videos",
			mcplib.WithDescription("List all stored videos with metadata (title, channel, date, transcript status)"),
			mcplib.WithString("channel", mcplib.Description("Filter by channel ID or name")),
			mcplib.WithNumber("limit", mcplib.Description("Maximum results (default 50)")),
		),
		listVideosHandler(store),
	)

	s.AddTool(
		mcplib.NewTool("list_digests",
			mcplib.WithDescription("List all stored digest summaries with metadata (no body text)"),
		),
		listDigestsHandler(store),
	)

	s.AddTool(
		mcplib.NewTool("get_digest",
			mcplib.WithDescription("Get a specific digest summary by ID"),
			mcplib.WithNumber("id", mcplib.Required(), mcplib.Description("Digest ID (use list_digests to find IDs)")),
		),
		getDigestHandler(store),
	)

	s.AddTool(
		mcplib.NewTool("get_video_info",
			mcplib.WithDescription("Get video metadata without full transcript (title, channel, date, language, word count)"),
			mcplib.WithString("video_id", mcplib.Required(), mcplib.Description("YouTube video ID")),
		),
		getVideoInfoHandler(store),
	)

	s.AddTool(
		mcplib.NewTool("summarize",
			mcplib.WithDescription("Summarize transcripts using an LLM. Requires summarizer API key. If not configured, use search_transcripts and get_transcript to retrieve content and summarize yourself."),
			mcplib.WithString("channel", mcplib.Description("Filter by channel ID or name")),
			mcplib.WithString("video_id", mcplib.Description("Summarize a specific video by ID")),
			mcplib.WithString("query", mcplib.Description("Search transcripts matching query, then summarize")),
			mcplib.WithString("since", mcplib.Description("Time window, e.g. '24h', '168h' (default: '24h')")),
			mcplib.WithString("prompt", mcplib.Description("Custom system prompt for the summarizer")),
			mcplib.WithBoolean("re_summarize", mcplib.Description("Force re-summarize even if the same video set was already summarized")),
		),
		summarizeHandler(store, summarizerCfg),
	)

	s.AddTool(
		mcplib.NewTool("add_channel",
			mcplib.WithDescription("Add a YouTube channel to track. Accepts a handle (@name), URL, or channel ID. The channel name is auto-resolved from YouTube."),
			mcplib.WithString("input", mcplib.Required(), mcplib.Description("YouTube handle (@Fireship), URL, or channel ID")),
			mcplib.WithString("name", mcplib.Description("Optional display name override")),
		),
		addChannelHandler(store),
	)

	s.AddTool(
		mcplib.NewTool("remove_channel",
			mcplib.WithDescription("Remove a tracked YouTube channel"),
			mcplib.WithString("channel", mcplib.Required(), mcplib.Description("Channel ID or name to remove")),
		),
		removeChannelHandler(store),
	)

	s.AddTool(
		mcplib.NewTool("queue_list",
			mcplib.WithDescription("List fetch queue jobs with state and error details"),
			mcplib.WithString("state", mcplib.Description("Filter by state: pending, in_progress, failed, dead, no_transcript")),
			mcplib.WithNumber("limit", mcplib.Description("Maximum jobs to return (default 50)")),
		),
		queueListHandler(store),
	)

	s.AddTool(
		mcplib.NewTool("queue_retry",
			mcplib.WithDescription("Reset a specific fetch job to pending with a fresh retry budget"),
			mcplib.WithNumber("id", mcplib.Required(), mcplib.Description("Job ID to retry (from queue_list)")),
		),
		queueRetryHandler(store),
	)

	s.AddTool(
		mcplib.NewTool("queue_retry_all",
			mcplib.WithDescription("Make all failed fetch jobs immediately eligible for retry"),
		),
		queueRetryAllHandler(store),
	)

	return s
}

func listChannelsHandler(store *db.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		channels, err := store.ListChannels(ctx)
		if err != nil {
			return errorResult(err), nil
		}
		data, _ := json.MarshalIndent(channels, "", "  ")
		return mcplib.NewToolResultText(string(data)), nil
	}
}

func searchTranscriptsHandler(store *db.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		query := req.GetString("query", "")
		channel := req.GetString("channel", "")
		limit := req.GetInt("limit", 10)
		excerptLen := req.GetInt("excerpt_length", 500)

		channelID, err := resolveChannelID(ctx, store, channel)
		if err != nil {
			return errorResult(err), nil
		}

		results, err := store.SearchTranscriptsWithMetadata(ctx, query, channelID, limit)
		if err != nil {
			return errorResult(err), nil
		}

		type searchResult struct {
			VideoID    string  `json:"video_id"`
			VideoTitle string  `json:"video_title"`
			Channel    string  `json:"channel"`
			Language   string  `json:"language"`
			Published  *string `json:"published_at,omitempty"`
			Excerpt    string  `json:"excerpt"`
		}

		var out []searchResult
		for _, r := range results {
			excerpt := ""
			if r.ContentText != nil {
				excerpt = truncateToWordBoundary(*r.ContentText, excerptLen)
			}
			var pubStr *string
			if r.PublishedAt != nil {
				s := r.PublishedAt.Format("2006-01-02")
				pubStr = &s
			}
			out = append(out, searchResult{
				VideoID:    r.VideoID,
				VideoTitle: r.VideoTitle,
				Channel:    r.ChannelName,
				Language:   r.Language,
				Published:  pubStr,
				Excerpt:    excerpt,
			})
		}

		data, _ := json.MarshalIndent(out, "", "  ")
		return mcplib.NewToolResultText(string(data)), nil
	}
}

func getTranscriptHandler(store *db.Store, defaultLanguages []string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		videoID := req.GetString("video_id", "")
		format := req.GetString("format", "text")
		maxChars := req.GetInt("max_chars", 0)
		lang := req.GetString("language", "")
		if lang == "" && len(defaultLanguages) > 0 {
			lang = defaultLanguages[0]
		}

		t, err := store.GetTranscript(ctx, videoID, lang)
		if err != nil {
			return errorResult(err), nil
		}
		if t == nil {
			return mcplib.NewToolResultText(fmt.Sprintf("No transcript found for video %s (language: %s)", videoID, lang)), nil
		}

		switch format {
		case "timestamped":
			if t.ContentJSON != nil {
				return mcplib.NewToolResultText(*t.ContentJSON), nil
			}
			return mcplib.NewToolResultText("No timestamped content available"), nil
		default:
			if t.ContentText == nil {
				return mcplib.NewToolResultText("No text content available"), nil
			}
			text := *t.ContentText
			if maxChars > 0 && len(text) > maxChars {
				truncated := truncateToWordBoundary(text, maxChars)
				truncated += fmt.Sprintf("\n\n[Truncated at %d chars. Total: %d chars]", maxChars, len(text))
				return mcplib.NewToolResultText(truncated), nil
			}
			return mcplib.NewToolResultText(text), nil
		}
	}
}

func getRecentVideosHandler(store *db.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		sinceStr := req.GetString("since", "")
		channel := req.GetString("channel", "")

		since := 24 * time.Hour
		if sinceStr != "" {
			d, err := time.ParseDuration(sinceStr)
			if err == nil {
				since = d
			}
		}

		sinceTime := time.Now().Add(-since)

		var videos []db.Video
		var err error
		if channel != "" {
			videos, err = store.ListVideosByChannel(ctx, channel, &sinceTime)
		} else {
			videos, err = store.ListRecentVideos(ctx, sinceTime)
		}
		if err != nil {
			return errorResult(err), nil
		}

		limit := req.GetInt("limit", 50)
		if len(videos) > limit {
			videos = videos[:limit]
		}

		data, _ := json.MarshalIndent(videos, "", "  ")
		return mcplib.NewToolResultText(string(data)), nil
	}
}

func fetchNewHandler(store *db.Store, f *fetcher.Fetcher) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		channelFilter := req.GetString("channel", "")
		sinceStr := req.GetString("since", "")
		all := req.GetBool("all", false)
		dryRun := req.GetBool("dry_run", false)

		since := 24 * time.Hour
		if sinceStr != "" {
			d, err := time.ParseDuration(sinceStr)
			if err == nil {
				since = d
			}
		}
		sinceTime := time.Now().Add(-since)
		if all {
			sinceTime = time.Time{}
		}

		channelID, err := resolveChannelID(ctx, store, channelFilter)
		if err != nil {
			return errorResult(err), nil
		}

		channels, err := store.ListChannels(ctx)
		if err != nil {
			return errorResult(err), nil
		}
		if channelID != "" {
			var filtered []db.Channel
			for _, ch := range channels {
				if ch.ChannelID == channelID {
					filtered = append(filtered, ch)
				}
			}
			channels = filtered
		}

		if dryRun {
			candidates, _, err := f.Discover(ctx, channels, sinceTime, true)
			if err != nil {
				return errorResult(err), nil
			}
			if len(candidates) == 0 {
				return mcplib.NewToolResultText("No new videos found."), nil
			}
			type preview struct {
				VideoID     string `json:"video_id"`
				Title       string `json:"title"`
				ChannelName string `json:"channel_name"`
			}
			var out []preview
			for _, c := range candidates {
				out = append(out, preview{VideoID: c.VideoID, Title: c.Title, ChannelName: c.ChannelName})
			}
			data, _ := json.MarshalIndent(out, "", "  ")
			return mcplib.NewToolResultText(fmt.Sprintf("Would fetch transcripts for %d video(s):\n%s", len(candidates), data)), nil
		}

		res, err := f.Run(ctx, channels, sinceTime)
		if err != nil {
			return errorResult(err), nil
		}

		return mcplib.NewToolResultText(fmt.Sprintf(
			"Discovered %d new video(s): %d transcripts fetched, %d without transcripts, %d failed (will retry), %d dead",
			res.Discovered, res.Succeeded, res.NoTranscript, res.Failed, res.Dead)), nil
	}
}

func listVideosHandler(store *db.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		channel := req.GetString("channel", "")
		limit := req.GetInt("limit", 50)

		channelID, err := resolveChannelID(ctx, store, channel)
		if err != nil {
			return errorResult(err), nil
		}

		videos, err := store.ListVideosWithMetadata(ctx, channelID, limit)
		if err != nil {
			return errorResult(err), nil
		}

		data, _ := json.MarshalIndent(videos, "", "  ")
		return mcplib.NewToolResultText(string(data)), nil
	}
}

func listDigestsHandler(store *db.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		digests, err := store.ListDigests(ctx)
		if err != nil {
			return errorResult(err), nil
		}

		type digestSummary struct {
			ID            int64  `json:"id"`
			WindowStart   string `json:"window_start"`
			WindowEnd     string `json:"window_end"`
			ChannelFilter string `json:"channel_filter"`
			Model         string `json:"model"`
			VideoCount    int    `json:"video_count"`
			CreatedAt     string `json:"created_at"`
		}

		var out []digestSummary
		for _, d := range digests {
			out = append(out, digestSummary{
				ID:            d.ID,
				WindowStart:   d.WindowStart.Format("2006-01-02 15:04"),
				WindowEnd:     d.WindowEnd.Format("2006-01-02 15:04"),
				ChannelFilter: d.ChannelFilter,
				Model:         d.Model,
				VideoCount:    d.VideoCount,
				CreatedAt:     d.CreatedAt.Format("2006-01-02 15:04"),
			})
		}

		data, _ := json.MarshalIndent(out, "", "  ")
		return mcplib.NewToolResultText(string(data)), nil
	}
}

func getDigestHandler(store *db.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		id := int64(req.GetInt("id", 0))
		if id == 0 {
			return errorResult(fmt.Errorf("digest ID is required")), nil
		}

		d, err := store.GetDigest(ctx, id)
		if err != nil {
			return errorResult(err), nil
		}
		if d == nil {
			return mcplib.NewToolResultText(fmt.Sprintf("Digest #%d not found", id)), nil
		}

		data, _ := json.MarshalIndent(d, "", "  ")
		return mcplib.NewToolResultText(string(data)), nil
	}
}

func getVideoInfoHandler(store *db.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		videoID := req.GetString("video_id", "")

		video, err := store.GetVideo(ctx, videoID)
		if err != nil {
			return errorResult(err), nil
		}
		if video == nil {
			return mcplib.NewToolResultText(fmt.Sprintf("Video %s not found", videoID)), nil
		}

		channelName := ""
		ch, err := store.GetChannelByID(ctx, video.ChannelID)
		if err == nil && ch != nil {
			channelName = ch.Name
		}

		stats, _ := store.GetTranscriptStats(ctx, videoID)

		type videoInfo struct {
			VideoID            string  `json:"video_id"`
			Title              string  `json:"title"`
			ChannelID          string  `json:"channel_id"`
			ChannelName        string  `json:"channel_name"`
			PublishedAt        *string `json:"published_at,omitempty"`
			HasTranscript      bool    `json:"has_transcript"`
			TranscriptLang     string  `json:"transcript_language,omitempty"`
			TranscriptChars    int     `json:"transcript_char_count,omitempty"`
			TranscriptProvider string  `json:"transcript_provider,omitempty"`
			HasTimestamped     bool    `json:"has_timestamped,omitempty"`
		}

		info := videoInfo{
			VideoID:       video.VideoID,
			Title:         video.Title,
			ChannelID:     video.ChannelID,
			ChannelName:   channelName,
			HasTranscript: stats != nil,
		}
		if video.PublishedAt != nil {
			s := video.PublishedAt.Format("2006-01-02")
			info.PublishedAt = &s
		}
		if stats != nil {
			info.TranscriptLang = stats.Language
			info.TranscriptChars = stats.CharCount
			info.TranscriptProvider = stats.Provider
			info.HasTimestamped = stats.HasJSON
		}

		data, _ := json.MarshalIndent(info, "", "  ")
		return mcplib.NewToolResultText(string(data)), nil
	}
}

func summarizeHandler(store *db.Store, cfg *config.SummarizerConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		if cfg == nil || cfg.APIKey == "" {
			return mcplib.NewToolResultText(
				"Summarizer is not configured (no API key). " +
					"To summarize content yourself:\n" +
					"1. Use search_transcripts to find relevant content\n" +
					"2. Use get_transcript to retrieve full transcript text\n" +
					"3. Summarize the content directly in your response",
			), nil
		}

		channel := req.GetString("channel", "")
		videoID := req.GetString("video_id", "")
		query := req.GetString("query", "")
		sinceStr := req.GetString("since", "24h")
		customPrompt := req.GetString("prompt", "")
		reSummarize := req.GetBool("re_summarize", false)

		channelID, err := resolveChannelID(ctx, store, channel)
		if err != nil {
			return errorResult(err), nil
		}

		// Query-based summarization: search, then summarize inline (digest.Generate doesn't support query).
		if query != "" {
			results, err := store.SearchTranscriptsWithMetadata(ctx, query, channelID, 20)
			if err != nil {
				return errorResult(err), nil
			}
			if len(results) == 0 {
				return mcplib.NewToolResultText("No transcripts found matching the query."), nil
			}
			var combined strings.Builder
			for i, r := range results {
				video, _ := store.GetVideo(ctx, r.VideoID)
				title := r.VideoID
				if video != nil && video.Title != "" {
					title = video.Title
				}
				if i > 0 {
					combined.WriteString("\n\n---\n\n")
				}
				fmt.Fprintf(&combined, "## Video: %s (ID: %s)\n\n", title, r.VideoID)
				if r.ContentText != nil {
					combined.WriteString(*r.ContentText)
				}
			}
			s := summarizer.New(cfg.Endpoint, cfg.APIKey, cfg.Model, cfg.MaxTokens)
			result, err := s.Summarize(ctx, combined.String(), customPrompt)
			if err != nil {
				return errorResult(fmt.Errorf("summarization failed: %w", err)), nil
			}
			output := fmt.Sprintf("=== Summary (%d videos) ===\n\n%s\n\n--- Model: %s | Tokens: %d prompt, %d completion ---",
				len(results), result.Summary, result.Model,
				result.Usage.PromptTokens, result.Usage.CompletionTokens)
			return mcplib.NewToolResultText(output), nil
		}

		// Time-window or single-video summarization via digest.Generate (supports dedup).
		since := 24 * time.Hour
		if sinceStr != "" {
			d, err := time.ParseDuration(sinceStr)
			if err == nil {
				since = d
			}
		}

		res, err := digest.Generate(ctx, store, *cfg, digest.Options{
			Since:   since,
			Channel: channelID,
			VideoID: videoID,
			Prompt:  customPrompt,
			Force:   reSummarize,
		})
		if err != nil {
			return errorResult(fmt.Errorf("summarization failed: %w", err)), nil
		}

		if res.Skipped {
			if res.NoTranscripts {
				return mcplib.NewToolResultText("No transcripts found matching the criteria."), nil
			}
			return mcplib.NewToolResultText(res.SkipReason + ". Use re_summarize=true to force."), nil
		}

		output := fmt.Sprintf("=== Summary (%d videos) ===\n\n%s\n\n--- Model: %s | Tokens: %d prompt, %d completion ---",
			res.Digest.VideoCount, res.Summary.Summary, res.Summary.Model,
			res.Summary.Usage.PromptTokens, res.Summary.Usage.CompletionTokens)
		return mcplib.NewToolResultText(output), nil
	}
}

func addChannelHandler(store *db.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		input := req.GetString("input", "")
		name := req.GetString("name", "")

		channelID, channelURL, resolvedName, err := channel.Resolve(ctx, input)
		if err != nil {
			return errorResult(fmt.Errorf("resolving channel %q: %w", input, err)), nil
		}

		if name == "" {
			name = resolvedName
		}
		if name == "" {
			name = channelID
		}

		if err := store.AddChannel(ctx, channelID, name, channelURL); err != nil {
			return errorResult(err), nil
		}

		return mcplib.NewToolResultText(fmt.Sprintf("Added channel: %s (%s)", name, channelID)), nil
	}
}

func removeChannelHandler(store *db.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		input := req.GetString("channel", "")

		channelID := input
		if !channel.IsID(input) {
			ch, err := store.GetChannelByName(ctx, input)
			if err != nil {
				return errorResult(err), nil
			}
			if ch == nil {
				return errorResult(fmt.Errorf("channel %q not found", input)), nil
			}
			channelID = ch.ChannelID
		}

		if err := store.RemoveChannel(ctx, channelID); err != nil {
			return errorResult(err), nil
		}

		return mcplib.NewToolResultText(fmt.Sprintf("Removed channel: %s", channelID)), nil
	}
}

func queueListHandler(store *db.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		state := req.GetString("state", "")
		limit := req.GetInt("limit", 50)

		jobs, err := store.ListFetchJobs(ctx, state, limit)
		if err != nil {
			return errorResult(err), nil
		}

		counts, err := store.CountFetchJobsByState(ctx)
		if err != nil {
			return errorResult(err), nil
		}

		type queueResult struct {
			Jobs   []db.FetchJob  `json:"jobs"`
			Counts map[string]int `json:"counts"`
		}

		data, _ := json.MarshalIndent(queueResult{Jobs: jobs, Counts: counts}, "", "  ")
		return mcplib.NewToolResultText(string(data)), nil
	}
}

func queueRetryHandler(store *db.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		id := int64(req.GetInt("id", 0))
		if id == 0 {
			return errorResult(fmt.Errorf("job ID is required")), nil
		}

		if err := store.RetryFetchJob(ctx, id); err != nil {
			return errorResult(err), nil
		}

		return mcplib.NewToolResultText(fmt.Sprintf("Job %d reset to pending with fresh retry budget.", id)), nil
	}
}

func queueRetryAllHandler(store *db.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		n, err := store.RetryAllFailedFetchJobs(ctx)
		if err != nil {
			return errorResult(err), nil
		}

		return mcplib.NewToolResultText(fmt.Sprintf("%d failed job(s) reset to pending.", n)), nil
	}
}

func errorResult(err error) *mcplib.CallToolResult {
	return mcplib.NewToolResultError(err.Error())
}
