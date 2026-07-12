package transcript

import (
	"context"
	"fmt"
	"log/slog"
	"time"
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
	t, err := retryWithBackoff(ctx, 3, time.Second, func() (*Transcript, error) {
		return d.primary.FetchTranscript(ctx, videoID, languages)
	})
	if err == nil {
		return t, nil
	}

	slog.Warn("primary provider exhausted retries, trying fallback",
		"provider", d.primary.Name(),
		"fallback", d.fallback.Name(),
		"video", videoID,
		"error", err)

	t, err = retryWithBackoff(ctx, 3, time.Second, func() (*Transcript, error) {
		return d.fallback.FetchTranscript(ctx, videoID, languages)
	})
	if err != nil {
		return nil, fmt.Errorf("all providers failed for %s: primary exhausted, fallback: %w", videoID, err)
	}
	return t, nil
}

func retryWithBackoff(ctx context.Context, maxAttempts int, baseDelay time.Duration, fn func() (*Transcript, error)) (*Transcript, error) {
	var lastErr error
	delay := baseDelay
	for attempt := 0; attempt < maxAttempts; attempt++ {
		t, err := fn()
		if err == nil {
			return t, nil
		}
		lastErr = err
		if attempt < maxAttempts-1 {
			slog.Warn("retrying after failure",
				"attempt", attempt+1,
				"max_attempts", maxAttempts,
				"next_delay", delay,
				"error", err)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}
	}
	return nil, fmt.Errorf("failed after %d attempts: %w", maxAttempts, lastErr)
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
