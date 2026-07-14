package transcript

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// DualProvider tries the primary provider first, falling back to the secondary on error.
type DualProvider struct {
	primary  Provider
	fallback Provider
	backoff  func() // optional callback for rate-limit backoff
}

func NewDualProvider(primary, fallback Provider) *DualProvider {
	return &DualProvider{primary: primary, fallback: fallback}
}

// SetBackoffCallback sets a function to call when a rate-limit error is detected.
func (d *DualProvider) SetBackoffCallback(fn func()) {
	d.backoff = fn
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

	// Don't fallback on permanent errors (no transcript exists)
	if IsPermanentError(err) {
		slog.Debug("no transcript available, skipping fallback",
			"provider", d.primary.Name(), "video", videoID)
		return nil, err
	}

	// Check for rate-limit errors and trigger backoff
	if isRateLimitError(err) && d.backoff != nil {
		slog.Warn("rate limit detected on primary provider, backing off",
			"provider", d.primary.Name(), "video", videoID)
		d.backoff()
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
		// Check for rate-limit errors on fallback too
		if isRateLimitError(err) && d.backoff != nil {
			slog.Warn("rate limit detected on fallback provider, backing off",
				"provider", d.fallback.Name(), "video", videoID)
			d.backoff()
		}
		return nil, fmt.Errorf("all providers failed for %s: primary exhausted, fallback: %w", videoID, err)
	}
	return t, nil
}

// isRateLimitError checks if an error is related to rate limiting.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "402") ||
		strings.Contains(errStr, "Too Many Requests") ||
		strings.Contains(errStr, "RequestBlocked") ||
		strings.Contains(errStr, "rate limit")
}

// IsPermanentError checks if an error indicates no transcript exists (not retryable).
func IsPermanentError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "no transcript found") ||
		strings.Contains(errStr, "no segments found") ||
		strings.Contains(errStr, "subtitle file not found")
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

		// Don't retry permanent errors (no transcript exists)
		if IsPermanentError(err) {
			return nil, err
		}

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
