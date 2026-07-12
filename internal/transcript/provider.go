package transcript

import "context"

// Segment represents a timed text segment from a transcript.
type Segment struct {
	Text    string `json:"text"`
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
}

// Transcript holds the full transcript data for a video.
type Transcript struct {
	VideoID  string    `json:"video_id"`
	Language string    `json:"language"`
	Segments []Segment `json:"segments"`
	RawJSON  string    `json:"-"`
	FullText string    `json:"full_text"`
	Provider string    `json:"provider"`
}

// Provider is the interface for fetching transcripts from YouTube videos.
type Provider interface {
	FetchTranscript(ctx context.Context, videoID string, languages []string) (*Transcript, error)
	Name() string
}
