package db

import (
	"context"
	"database/sql"
	"fmt"
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
