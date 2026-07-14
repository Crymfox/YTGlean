package ratelimit

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Config holds rate limiter configuration.
type Config struct {
	Feed struct {
		RequestsPerSecond float64 `yaml:"requests_per_second" mapstructure:"requests_per_second"`
		Burst             int     `yaml:"burst" mapstructure:"burst"`
	} `yaml:"feed" mapstructure:"feed"`
	InnerTube struct {
		RequestsPerSecond float64 `yaml:"requests_per_second" mapstructure:"requests_per_second"`
		Burst             int     `yaml:"burst" mapstructure:"burst"`
	} `yaml:"innertube" mapstructure:"innertube"`
	YtDlp struct {
		RequestsPerSecond float64 `yaml:"requests_per_second" mapstructure:"requests_per_second"`
		Burst             int     `yaml:"burst" mapstructure:"burst"`
	} `yaml:"ytdlp" mapstructure:"ytdlp"`
	BackoffMultiplier float64 `yaml:"backoff_multiplier" mapstructure:"backoff_multiplier"`
	RecoverySeconds   int     `yaml:"recovery_seconds" mapstructure:"recovery_seconds"`
}

// DefaultConfig returns sensible rate limit defaults.
func DefaultConfig() Config {
	cfg := Config{}
	cfg.Feed.RequestsPerSecond = 2.0
	cfg.Feed.Burst = 5
	cfg.InnerTube.RequestsPerSecond = 1.0
	cfg.InnerTube.Burst = 3
	cfg.YtDlp.RequestsPerSecond = 0.167 // ~10/min, conservative for guest sessions
	cfg.YtDlp.Burst = 1
	cfg.BackoffMultiplier = 0.5
	cfg.RecoverySeconds = 60
	return cfg
}

// Limiter provides rate limiting with adaptive backoff.
type Limiter struct {
	feedLimiter      *rate.Limiter
	innertubeLimiter *rate.Limiter
	ytdlpLimiter     *rate.Limiter

	backoffMu     sync.Mutex
	baseLimit     rate.Limit
	recoveryAfter time.Duration
	recoverAt     time.Time
}

// New creates a rate limiter with the given config.
func New(cfg Config) *Limiter {
	return &Limiter{
		feedLimiter:      rate.NewLimiter(rate.Limit(cfg.Feed.RequestsPerSecond), cfg.Feed.Burst),
		innertubeLimiter: rate.NewLimiter(rate.Limit(cfg.InnerTube.RequestsPerSecond), cfg.InnerTube.Burst),
		ytdlpLimiter:     rate.NewLimiter(rate.Limit(cfg.YtDlp.RequestsPerSecond), cfg.YtDlp.Burst),
		baseLimit:        rate.Limit(cfg.InnerTube.RequestsPerSecond),
		recoveryAfter:    time.Duration(cfg.RecoverySeconds) * time.Second,
	}
}

// WaitFeed blocks until a feed request token is available.
func (l *Limiter) WaitFeed(ctx context.Context) error {
	return l.feedLimiter.Wait(ctx)
}

// WaitInnerTube blocks until an InnerTube request token is available.
// Respects adaptive backoff.
func (l *Limiter) WaitInnerTube(ctx context.Context) error {
	l.maybeRecover()
	return l.innertubeLimiter.Wait(ctx)
}

// WaitYtDlp blocks until a yt-dlp request token is available.
func (l *Limiter) WaitYtDlp(ctx context.Context) error {
	l.maybeRecover()
	return l.ytdlpLimiter.Wait(ctx)
}

// Backoff reduces the rate limit (call on 429/402 errors).
func (l *Limiter) Backoff() {
	l.backoffMu.Lock()
	defer l.backoffMu.Unlock()

	newLimit := l.innertubeLimiter.Limit() * 0.5
	if newLimit < 0.1 {
		newLimit = 0.1 // floor at 1 request per 10 seconds
	}
	l.innertubeLimiter.SetLimit(newLimit)
	l.recoverAt = time.Now().Add(l.recoveryAfter)

	slog.Warn("rate limiter backoff activated",
		"new_limit", float64(newLimit),
		"recovery_at", l.recoverAt.Format(time.RFC3339))
}

func (l *Limiter) maybeRecover() {
	l.backoffMu.Lock()
	defer l.backoffMu.Unlock()

	if !l.recoverAt.IsZero() && time.Now().After(l.recoverAt) {
		l.innertubeLimiter.SetLimit(l.baseLimit)
		l.recoverAt = time.Time{}
		slog.Info("rate limiter recovered to normal speed")
	}
}
