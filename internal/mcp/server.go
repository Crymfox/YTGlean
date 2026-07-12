package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/CrymfoxLabs/YTGlean/internal/db"
	"github.com/CrymfoxLabs/YTGlean/internal/feed"
	"github.com/CrymfoxLabs/YTGlean/internal/transcript"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewServer creates an MCP server with all YTGlean tools registered.
func NewServer(store *db.Store, provider transcript.Provider, languages []string, version string) *server.MCPServer {
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
		),
		getTranscriptHandler(store),
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
			mcplib.WithString("channel", mcplib.Description("Filter to a specific channel ID")),
			mcplib.WithString("since", mcplib.Description("Time window, e.g. '24h' (default: '24h')")),
		),
		fetchNewHandler(store, provider, languages),
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

		transcripts, err := store.SearchTranscripts(ctx, query, channel, limit)
		if err != nil {
			return errorResult(err), nil
		}

		type searchResult struct {
			VideoID  string `json:"video_id"`
			Language string `json:"language"`
			Provider string `json:"provider"`
			Excerpt  string `json:"excerpt"`
		}

		var results []searchResult
		for _, t := range transcripts {
			excerpt := ""
			if t.ContentText != nil {
				excerpt = *t.ContentText
				if len(excerpt) > 500 {
					excerpt = excerpt[:500] + "..."
				}
			}
			results = append(results, searchResult{
				VideoID:  t.VideoID,
				Language: t.Language,
				Provider: t.Provider,
				Excerpt:  excerpt,
			})
		}

		data, _ := json.MarshalIndent(results, "", "  ")
		return mcplib.NewToolResultText(string(data)), nil
	}
}

func getTranscriptHandler(store *db.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		videoID := req.GetString("video_id", "")
		format := req.GetString("format", "text")

		t, err := store.GetTranscript(ctx, videoID, "en")
		if err != nil {
			return errorResult(err), nil
		}
		if t == nil {
			return mcplib.NewToolResultText(fmt.Sprintf("No transcript found for video %s", videoID)), nil
		}

		switch format {
		case "timestamped":
			if t.ContentJSON != nil {
				return mcplib.NewToolResultText(*t.ContentJSON), nil
			}
			return mcplib.NewToolResultText("No timestamped content available"), nil
		default:
			if t.ContentText != nil {
				return mcplib.NewToolResultText(*t.ContentText), nil
			}
			return mcplib.NewToolResultText("No text content available"), nil
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

func fetchNewHandler(store *db.Store, provider transcript.Provider, languages []string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		channelFilter := req.GetString("channel", "")
		sinceStr := req.GetString("since", "")

		since := 24 * time.Hour
		if sinceStr != "" {
			d, err := time.ParseDuration(sinceStr)
			if err == nil {
				since = d
			}
		}
		sinceTime := time.Now().Add(-since)

		channels, err := store.ListChannels(ctx)
		if err != nil {
			return errorResult(err), nil
		}

		var successCount, failCount int
		for _, ch := range channels {
			if channelFilter != "" && ch.ChannelID != channelFilter {
				continue
			}

			entries, err := feed.FetchNewVideos(ctx, ch.ChannelID, sinceTime)
			if err != nil {
				failCount++
				continue
			}

			for _, entry := range entries {
				has, _ := store.HasTranscript(ctx, entry.VideoID, languages[0])
				if has {
					continue
				}

				pubTime := entry.Published
				_ = store.AddVideo(ctx, entry.VideoID, ch.ChannelID, entry.Title, &pubTime)

				tx, err := provider.FetchTranscript(ctx, entry.VideoID, languages)
				if err != nil {
					failCount++
					continue
				}

				if err := store.AddTranscript(ctx, tx.VideoID, tx.Language, tx.RawJSON, tx.FullText, tx.Provider); err != nil {
					failCount++
					continue
				}
				successCount++
			}

			_ = store.UpdateLastChecked(ctx, ch.ChannelID)
		}

		return mcplib.NewToolResultText(fmt.Sprintf("Fetched %d transcripts, %d failures", successCount, failCount)), nil
	}
}

func errorResult(err error) *mcplib.CallToolResult {
	return mcplib.NewToolResultError(err.Error())
}
