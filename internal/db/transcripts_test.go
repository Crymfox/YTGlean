package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func seedTranscript(t *testing.T, store *Store, videoID, channelID, title, text string) {
	t.Helper()
	ctx := context.Background()
	if err := store.AddChannel(ctx, channelID, "Channel "+channelID, ""); err != nil {
		t.Fatalf("adding channel: %v", err)
	}
	now := time.Now()
	if err := store.AddVideo(ctx, videoID, channelID, title, &now); err != nil {
		t.Fatalf("adding video: %v", err)
	}
	if err := store.AddTranscript(ctx, videoID, "en", "", text, "test"); err != nil {
		t.Fatalf("adding transcript: %v", err)
	}
}

func TestSearchTranscriptsFTS(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	seedTranscript(t, store, "vid1", "ch1", "Go Concurrency",
		"today we talk about goroutines and channels in the Go programming language")
	seedTranscript(t, store, "vid2", "ch1", "Rust Ownership",
		"the borrow checker enforces ownership rules at compile time")
	seedTranscript(t, store, "vid3", "ch2", "Cooking Pasta",
		"boil water and add salt before adding the pasta")

	tests := []struct {
		name    string
		query   string
		channel string
		want    []string
	}{
		{"single term", "goroutines", "", []string{"vid1"}},
		{"multi term AND", "borrow checker", "", []string{"vid2"}},
		{"stemless no match", "goroutine", "", nil}, // unicode61 doesn't stem
		{"prefix match", "goroutine*", "", []string{"vid1"}},
		{"channel filter hit", "pasta", "ch2", []string{"vid3"}},
		{"channel filter miss", "pasta", "ch1", nil},
		{"no results", "quantum", "", nil},
		{"quotes are safe", `"boil water"`, "", []string{"vid3"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := store.SearchTranscriptsWithMetadata(ctx, tt.query, tt.channel, 10)
			if err != nil {
				t.Fatalf("search error: %v", err)
			}
			var got []string
			for _, r := range results {
				got = append(got, r.VideoID)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestFTSIndexFollowsUpdatesAndDeletes(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	seedTranscript(t, store, "vid1", "ch1", "Video", "original words here")

	// Upsert replaces content — old term must stop matching, new term must match
	if err := store.AddTranscript(ctx, "vid1", "en", "", "replacement text entirely", "test"); err != nil {
		t.Fatalf("upserting transcript: %v", err)
	}
	if got, _ := store.SearchTranscripts(ctx, "original", "", 10); len(got) != 0 {
		t.Fatalf("stale FTS entry after upsert: %v", got)
	}
	if got, _ := store.SearchTranscripts(ctx, "replacement", "", 10); len(got) != 1 {
		t.Fatalf("FTS entry missing after upsert: %v", got)
	}

	// Cascade delete via video prune must clean the index
	future := time.Now().Add(24 * time.Hour)
	if _, err := store.PruneOlderThan(ctx, future); err != nil {
		t.Fatalf("pruning: %v", err)
	}
	if got, _ := store.SearchTranscripts(ctx, "replacement", "", 10); len(got) != 0 {
		t.Fatalf("stale FTS entry after cascade delete: %v", got)
	}
}

func TestFTSQueryEscaping(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"hello world", `"hello" "world"`},
		{`say "hi"`, `"say" """hi"""`},
		{"prefix*", `"prefix"*`},
		{"  spaced   out  ", `"spaced" "out"`},
		{"", ""},
		{"***", ""},
	}
	for _, tt := range tests {
		if got := ftsQuery(tt.in); got != tt.want {
			t.Errorf("ftsQuery(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
