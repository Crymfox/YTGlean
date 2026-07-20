package watcher

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/CrymfoxLabs/YTGlean/internal/fetcher"
)

func TestFirstCycleRunsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fetched := make(chan struct{}, 1)
	w := &Watcher{
		Interval: time.Hour, // ticker should never fire
		Fetch: func(ctx context.Context) (fetcher.Result, error) {
			fetched <- struct{}{}
			return fetcher.Result{}, nil
		},
	}

	done := make(chan struct{})
	go func() { defer close(done); _ = w.Run(ctx) }()

	select {
	case <-fetched:
	case <-time.After(2 * time.Second):
		t.Fatalf("first cycle did not run immediately")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("watcher did not stop on cancel")
	}
}

func TestTickerFiresSubsequentCycles(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	cycles := 0
	w := &Watcher{
		Interval: 30 * time.Millisecond,
		Fetch: func(ctx context.Context) (fetcher.Result, error) {
			mu.Lock()
			cycles++
			mu.Unlock()
			return fetcher.Result{}, nil
		},
	}

	go func() { _ = w.Run(ctx) }()
	time.Sleep(120 * time.Millisecond)
	cancel()

	mu.Lock()
	got := cycles
	mu.Unlock()
	if got < 2 {
		t.Fatalf("expected >= 2 cycles, got %d", got)
	}
}

func TestAutoSummarizeThresholdAndReset(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	summarized := 0
	fetchResults := []int{2, 2, 1, 0, 3} // cumulative: 2, 4(hit), 1, 1, 4(hit)
	call := 0

	w := &Watcher{
		Interval:           10 * time.Millisecond,
		SummarizeThreshold: 3,
		Fetch: func(ctx context.Context) (fetcher.Result, error) {
			mu.Lock()
			defer mu.Unlock()
			if call >= len(fetchResults) {
				return fetcher.Result{}, nil
			}
			n := fetchResults[call]
			call++
			return fetcher.Result{Succeeded: n, Claimed: n}, nil
		},
		Summarize: func(ctx context.Context, window time.Duration) error {
			mu.Lock()
			summarized++
			mu.Unlock()
			return nil
		},
	}

	go func() { _ = w.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		c, s := call, summarized
		mu.Unlock()
		if c >= len(fetchResults) && s >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out: calls=%d summarized=%d", c, s)
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if summarized != 2 {
		t.Fatalf("expected exactly 2 summarize calls, got %d", summarized)
	}
}

func TestFetchErrorDoesNotStopLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	cycles := 0
	w := &Watcher{
		Interval: 20 * time.Millisecond,
		Fetch: func(ctx context.Context) (fetcher.Result, error) {
			mu.Lock()
			cycles++
			mu.Unlock()
			return fetcher.Result{}, errors.New("boom")
		},
	}

	go func() { _ = w.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)
	cancel()

	mu.Lock()
	got := cycles
	mu.Unlock()
	if got < 2 {
		t.Fatalf("loop stopped after error, cycles = %d", got)
	}
}

func TestSummarizeErrorKeepsCounter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	attempts := 0
	w := &Watcher{
		Interval:           10 * time.Millisecond,
		SummarizeThreshold: 1,
		Fetch: func(ctx context.Context) (fetcher.Result, error) {
			mu.Lock()
			defer mu.Unlock()
			if attempts == 0 {
				return fetcher.Result{Succeeded: 1}, nil
			}
			return fetcher.Result{}, nil // no new transcripts after first
		},
		Summarize: func(ctx context.Context, window time.Duration) error {
			mu.Lock()
			attempts++
			mu.Unlock()
			return errors.New("api down")
		},
	}

	go func() { _ = w.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)
	cancel()

	mu.Lock()
	got := attempts
	mu.Unlock()
	// Counter not reset on error -> threshold still met -> retried on later cycles
	if got < 2 {
		t.Fatalf("expected summarize retried after error, attempts = %d", got)
	}
}
