package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Video struct {
	ID          int64
	VideoID     string
	ChannelID   string
	Title       string
	PublishedAt *time.Time
	FetchedAt   time.Time
	Duration    *int
}

func (s *Store) AddVideo(ctx context.Context, videoID, channelID, title string, publishedAt *time.Time) error {
	var pubStr *string
	if publishedAt != nil {
		t := publishedAt.UTC().Format("2006-01-02 15:04:05")
		pubStr = &t
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO videos (video_id, channel_id, title, published_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(video_id) DO NOTHING`,
		videoID, channelID, title, pubStr,
	)
	if err != nil {
		return fmt.Errorf("adding video %s: %w", videoID, err)
	}
	return nil
}

func (s *Store) HasVideo(ctx context.Context, videoID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM videos WHERE video_id = ?)", videoID,
	).Scan(&exists)
	return exists, err
}

// HasVideos returns a map of videoID -> exists for batch checking.
func (s *Store) HasVideos(ctx context.Context, videoIDs []string) (map[string]bool, error) {
	result := make(map[string]bool, len(videoIDs))
	if len(videoIDs) == 0 {
		return result, nil
	}

	// Build placeholders
	placeholders := make([]string, len(videoIDs))
	args := make([]any, len(videoIDs))
	for i, id := range videoIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := "SELECT video_id FROM videos WHERE video_id IN (" + strings.Join(placeholders, ",") + ")"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("batch checking videos: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var videoID string
		if err := rows.Scan(&videoID); err != nil {
			return nil, fmt.Errorf("scanning video batch result: %w", err)
		}
		result[videoID] = true
	}

	// Mark unchecked IDs as false
	for _, id := range videoIDs {
		if !result[id] {
			result[id] = false
		}
	}

	return result, rows.Err()
}

func (s *Store) GetVideo(ctx context.Context, videoID string) (*Video, error) {
	v := &Video{}
	var publishedAt, fetchedAt sql.NullString
	var duration sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		"SELECT id, video_id, channel_id, title, published_at, fetched_at, duration FROM videos WHERE video_id = ?",
		videoID,
	).Scan(&v.ID, &v.VideoID, &v.ChannelID, &v.Title, &publishedAt, &fetchedAt, &duration)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting video %s: %w", videoID, err)
	}
	if publishedAt.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", publishedAt.String)
		v.PublishedAt = &t
	}
	if fetchedAt.Valid {
		v.FetchedAt, _ = time.Parse("2006-01-02 15:04:05", fetchedAt.String)
	}
	if duration.Valid {
		d := int(duration.Int64)
		v.Duration = &d
	}
	return v, nil
}

func (s *Store) ListVideosByChannel(ctx context.Context, channelID string, since *time.Time) ([]Video, error) {
	query := "SELECT id, video_id, channel_id, title, published_at, fetched_at, duration FROM videos WHERE channel_id = ?"
	args := []any{channelID}
	if since != nil {
		query += " AND published_at >= ?"
		args = append(args, since.UTC().Format("2006-01-02 15:04:05"))
	}
	query += " ORDER BY published_at DESC"

	return s.queryVideos(ctx, query, args...)
}

func (s *Store) ListRecentVideos(ctx context.Context, since time.Time) ([]Video, error) {
	return s.queryVideos(ctx,
		"SELECT id, video_id, channel_id, title, published_at, fetched_at, duration FROM videos WHERE published_at >= ? ORDER BY published_at DESC",
		since.UTC().Format("2006-01-02 15:04:05"),
	)
}

type VideoListItem struct {
	VideoID       string     `json:"video_id"`
	Title         string     `json:"title"`
	ChannelID     string     `json:"channel_id"`
	ChannelName   string     `json:"channel_name"`
	PublishedAt   *time.Time `json:"published_at"`
	HasTranscript bool       `json:"has_transcript"`
}

func (s *Store) ListVideosWithMetadata(ctx context.Context, channelID string, limit int) ([]VideoListItem, error) {
	query := `SELECT v.video_id, v.title, v.channel_id, COALESCE(c.name, ''),
	                 v.published_at,
	                 EXISTS(SELECT 1 FROM transcripts t WHERE t.video_id = v.video_id)
	          FROM videos v
	          LEFT JOIN channels c ON v.channel_id = c.channel_id`
	args := []any{}
	if channelID != "" {
		query += " WHERE v.channel_id = ?"
		args = append(args, channelID)
	}
	query += " ORDER BY v.published_at DESC"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing videos with metadata: %w", err)
	}
	defer rows.Close()

	var videos []VideoListItem
	for rows.Next() {
		var v VideoListItem
		var publishedAt sql.NullString
		if err := rows.Scan(&v.VideoID, &v.Title, &v.ChannelID, &v.ChannelName, &publishedAt, &v.HasTranscript); err != nil {
			return nil, fmt.Errorf("scanning video item: %w", err)
		}
		if publishedAt.Valid {
			t, _ := time.Parse("2006-01-02 15:04:05", publishedAt.String)
			v.PublishedAt = &t
		}
		videos = append(videos, v)
	}
	return videos, rows.Err()
}

func (s *Store) queryVideos(ctx context.Context, query string, args ...any) ([]Video, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying videos: %w", err)
	}
	defer rows.Close()

	var videos []Video
	for rows.Next() {
		var v Video
		var publishedAt, fetchedAt sql.NullString
		var duration sql.NullInt64
		if err := rows.Scan(&v.ID, &v.VideoID, &v.ChannelID, &v.Title, &publishedAt, &fetchedAt, &duration); err != nil {
			return nil, fmt.Errorf("scanning video: %w", err)
		}
		if publishedAt.Valid {
			t, _ := time.Parse("2006-01-02 15:04:05", publishedAt.String)
			v.PublishedAt = &t
		}
		if fetchedAt.Valid {
			v.FetchedAt, _ = time.Parse("2006-01-02 15:04:05", fetchedAt.String)
		}
		if duration.Valid {
			d := int(duration.Int64)
			v.Duration = &d
		}
		videos = append(videos, v)
	}
	return videos, rows.Err()
}
