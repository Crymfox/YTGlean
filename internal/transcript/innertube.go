package transcript

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	ytapi "github.com/virmundi/go-youtube-transcript-api"
)

// InnerTubeProvider fetches transcripts directly via YouTube's InnerTube API.
type InnerTubeProvider struct{}

func NewInnerTubeProvider() *InnerTubeProvider {
	return &InnerTubeProvider{}
}

func (p *InnerTubeProvider) Name() string { return "innertube" }

func (p *InnerTubeProvider) FetchTranscript(ctx context.Context, videoID string, languages []string) (*Transcript, error) {
	if len(languages) == 0 {
		languages = []string{"en"}
	}

	var lastErr error
	for _, lang := range languages {
		slog.Debug("fetching transcript via InnerTube", "video", videoID, "lang", lang)

		result, err := ytapi.FetchTranscript(videoID, lang)
		if err != nil {
			slog.Debug("InnerTube language attempt failed", "video", videoID, "lang", lang, "error", err)
			lastErr = err
			continue
		}

		var segments []Segment
		for _, snippet := range result.Snippets {
			text := strings.TrimSpace(snippet.Text)
			if text == "" {
				continue
			}
			startMs := int64(snippet.Start * 1000)
			endMs := int64((snippet.Start + snippet.Duration) * 1000)
			segments = append(segments, Segment{
				Text:    text,
				StartMs: startMs,
				EndMs:   endMs,
			})
		}

		if len(segments) == 0 {
			lastErr = fmt.Errorf("no transcript segments found for %s with lang %s", videoID, lang)
			slog.Debug("no segments returned", "video", videoID, "lang", lang)
			continue
		}

		return &Transcript{
			VideoID:  videoID,
			Language: lang,
			Segments: segments,
			FullText: buildFullText(segments),
			Provider: "innertube",
		}, nil
	}

	return nil, fmt.Errorf("innertube fetch failed for %s, tried languages %v: %w", videoID, languages, lastErr)
}
