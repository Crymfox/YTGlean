package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Transcript struct {
	ID          int64
	VideoID     string
	Language    string
	ContentJSON *string
	ContentText *string
	Provider    string
	CreatedAt   time.Time
}

type Digest struct {
	ID             int64
	WindowStart    time.Time
	WindowEnd      time.Time
	ChannelFilter  string
	Model          string
	PromptTemplate string
	DigestText     string
	VideoCount     int
	VideoIDs       string // JSON array of video IDs
	CreatedAt      time.Time
}

func (s *Store) AddTranscript(ctx context.Context, videoID, language, contentJSON, contentText, provider string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO transcripts (video_id, language, content_json, content_text, provider)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(video_id, language) DO UPDATE SET
		   content_json = excluded.content_json,
		   content_text = excluded.content_text,
		   provider = excluded.provider,
		   created_at = datetime('now')`,
		videoID, language, contentJSON, contentText, provider,
	)
	if err != nil {
		return fmt.Errorf("adding transcript for %s: %w", videoID, err)
	}
	return nil
}

func (s *Store) GetTranscript(ctx context.Context, videoID, language string) (*Transcript, error) {
	t := &Transcript{}
	var createdAt string
	err := s.db.QueryRowContext(ctx,
		"SELECT id, video_id, language, content_json, content_text, provider, created_at FROM transcripts WHERE video_id = ? AND language = ?",
		videoID, language,
	).Scan(&t.ID, &t.VideoID, &t.Language, &t.ContentJSON, &t.ContentText, &t.Provider, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting transcript for %s: %w", videoID, err)
	}
	t.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return t, nil
}

func (s *Store) HasTranscript(ctx context.Context, videoID, language string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM transcripts WHERE video_id = ? AND language = ?)",
		videoID, language,
	).Scan(&exists)
	return exists, err
}

func (s *Store) HasAnyTranscript(ctx context.Context, videoID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM transcripts WHERE video_id = ?)",
		videoID,
	).Scan(&exists)
	return exists, err
}

// GetTranscriptsInWindow returns all transcripts for videos published within the given time window.
func (s *Store) GetTranscriptsInWindow(ctx context.Context, start, end time.Time, channelID string) ([]Transcript, error) {
	query := `SELECT t.id, t.video_id, t.language, t.content_json, t.content_text, t.provider, t.created_at
		FROM transcripts t
		JOIN videos v ON t.video_id = v.video_id
		WHERE v.published_at >= ? AND v.published_at <= ?`
	args := []any{
		start.UTC().Format("2006-01-02 15:04:05"),
		end.UTC().Format("2006-01-02 15:04:05"),
	}
	if channelID != "" {
		query += " AND v.channel_id = ?"
		args = append(args, channelID)
	}
	query += " ORDER BY v.published_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying transcripts in window: %w", err)
	}
	defer rows.Close()

	var transcripts []Transcript
	for rows.Next() {
		var t Transcript
		var createdAt string
		if err := rows.Scan(&t.ID, &t.VideoID, &t.Language, &t.ContentJSON, &t.ContentText, &t.Provider, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning transcript: %w", err)
		}
		t.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		transcripts = append(transcripts, t)
	}
	return transcripts, rows.Err()
}

// SearchTranscripts performs a simple LIKE search across transcript text.
func (s *Store) SearchTranscripts(ctx context.Context, query string, channelID string, limit int) ([]Transcript, error) {
	sqlQuery := `SELECT t.id, t.video_id, t.language, t.content_json, t.content_text, t.provider, t.created_at
		FROM transcripts t
		JOIN videos v ON t.video_id = v.video_id
		WHERE t.content_text LIKE ?`
	args := []any{"%" + query + "%"}
	if channelID != "" {
		sqlQuery += " AND v.channel_id = ?"
		args = append(args, channelID)
	}
	if limit <= 0 {
		limit = 10
	}
	sqlQuery += " ORDER BY t.created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("searching transcripts: %w", err)
	}
	defer rows.Close()

	var transcripts []Transcript
	for rows.Next() {
		var t Transcript
		var createdAt string
		if err := rows.Scan(&t.ID, &t.VideoID, &t.Language, &t.ContentJSON, &t.ContentText, &t.Provider, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning transcript: %w", err)
		}
		t.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		transcripts = append(transcripts, t)
	}
	return transcripts, rows.Err()
}

// AddDigest stores a digest summary for a time window.
func (s *Store) AddDigest(ctx context.Context, d *Digest) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO digests (window_start, window_end, channel_filter, model, prompt_template, digest_text, video_count, video_ids)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.WindowStart.UTC().Format("2006-01-02 15:04:05"),
		d.WindowEnd.UTC().Format("2006-01-02 15:04:05"),
		d.ChannelFilter, d.Model, d.PromptTemplate, d.DigestText, d.VideoCount, d.VideoIDs,
	)
	if err != nil {
		return fmt.Errorf("adding digest: %w", err)
	}
	return nil
}

// ListDigests returns all digests ordered by most recent first.
func (s *Store) ListDigests(ctx context.Context) ([]Digest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, window_start, window_end, channel_filter, model, prompt_template, digest_text, video_count, video_ids, created_at
		 FROM digests ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing digests: %w", err)
	}
	defer rows.Close()

	var digests []Digest
	for rows.Next() {
		var d Digest
		var windowStart, windowEnd, createdAt string
		if err := rows.Scan(&d.ID, &windowStart, &windowEnd, &d.ChannelFilter, &d.Model, &d.PromptTemplate, &d.DigestText, &d.VideoCount, &d.VideoIDs, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning digest: %w", err)
		}
		d.WindowStart, _ = time.Parse("2006-01-02 15:04:05", windowStart)
		d.WindowEnd, _ = time.Parse("2006-01-02 15:04:05", windowEnd)
		d.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		digests = append(digests, d)
	}
	return digests, rows.Err()
}

// GetDigest returns a single digest by ID.
func (s *Store) GetDigest(ctx context.Context, id int64) (*Digest, error) {
	var d Digest
	var windowStart, windowEnd, createdAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, window_start, window_end, channel_filter, model, prompt_template, digest_text, video_count, video_ids, created_at
		 FROM digests WHERE id = ?`, id,
	).Scan(&d.ID, &windowStart, &windowEnd, &d.ChannelFilter, &d.Model, &d.PromptTemplate, &d.DigestText, &d.VideoCount, &d.VideoIDs, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting digest %d: %w", id, err)
	}
	d.WindowStart, _ = time.Parse("2006-01-02 15:04:05", windowStart)
	d.WindowEnd, _ = time.Parse("2006-01-02 15:04:05", windowEnd)
	d.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return &d, nil
}

// GetLatestDigest returns the most recent digest for a given channel filter, or nil if none exists.
func (s *Store) GetLatestDigest(ctx context.Context, channelFilter string) (*Digest, error) {
	var d Digest
	var windowStart, windowEnd, createdAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, window_start, window_end, channel_filter, model, prompt_template, digest_text, video_count, video_ids, created_at
		 FROM digests WHERE channel_filter = ? ORDER BY created_at DESC LIMIT 1`,
		channelFilter,
	).Scan(&d.ID, &windowStart, &windowEnd, &d.ChannelFilter, &d.Model, &d.PromptTemplate, &d.DigestText, &d.VideoCount, &d.VideoIDs, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting latest digest: %w", err)
	}
	d.WindowStart, _ = time.Parse("2006-01-02 15:04:05", windowStart)
	d.WindowEnd, _ = time.Parse("2006-01-02 15:04:05", windowEnd)
	d.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return &d, nil
}

// PruneOlderThan deletes videos (and cascading transcripts) older than the given time.
// Returns the number of videos deleted.
func (s *Store) PruneOlderThan(ctx context.Context, before time.Time) (int64, error) {
	// Delete old digests
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM digests WHERE window_end < ?",
		before.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return 0, fmt.Errorf("pruning digests: %w", err)
	}

	// Delete old videos (cascades to transcripts)
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM videos WHERE published_at < ?",
		before.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return 0, fmt.Errorf("pruning videos: %w", err)
	}
	return res.RowsAffected()
}

// CountOlderThan returns how many videos would be pruned.
func (s *Store) CountOlderThan(ctx context.Context, before time.Time) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM videos WHERE published_at < ?",
		before.UTC().Format("2006-01-02 15:04:05"),
	).Scan(&count)
	return count, err
}

// Vacuum runs VACUUM on the database to reclaim disk space.
func (s *Store) Vacuum(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "VACUUM")
	return err
}
