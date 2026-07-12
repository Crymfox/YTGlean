package transcript

import (
	"context"
	"log/slog"
)

// DualProvider tries the primary provider first, falling back to the secondary on error.
type DualProvider struct {
	primary  Provider
	fallback Provider
}

func NewDualProvider(primary, fallback Provider) *DualProvider {
	return &DualProvider{primary: primary, fallback: fallback}
}

func (d *DualProvider) Name() string {
	return "dual(" + d.primary.Name() + "+" + d.fallback.Name() + ")"
}

func (d *DualProvider) FetchTranscript(ctx context.Context, videoID string, languages []string) (*Transcript, error) {
	t, err := d.primary.FetchTranscript(ctx, videoID, languages)
	if err == nil {
		return t, nil
	}

	slog.Warn("primary provider failed, trying fallback",
		"provider", d.primary.Name(),
		"fallback", d.fallback.Name(),
		"video", videoID,
		"error", err,
	)

	return d.fallback.FetchTranscript(ctx, videoID, languages)
}

// NewProvider creates a transcript provider based on the mode string.
//   - "auto": InnerTube first, yt-dlp fallback (default)
//   - "ytdlp": yt-dlp only
//   - "innertube": InnerTube only
func NewProvider(mode string, cookieFile string) Provider {
	switch mode {
	case "ytdlp":
		return NewYtDlpProvider(cookieFile)
	case "innertube":
		return NewInnerTubeProvider()
	default: // "auto"
		return NewDualProvider(
			NewInnerTubeProvider(),
			NewYtDlpProvider(cookieFile),
		)
	}
}
