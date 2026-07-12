package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Channel struct {
	ID          int64
	ChannelID   string
	Name        string
	URL         string
	AddedAt     time.Time
	LastChecked *time.Time
}

func (s *Store) AddChannel(ctx context.Context, channelID, name, url string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO channels (channel_id, name, url) VALUES (?, ?, ?)
		 ON CONFLICT(channel_id) DO UPDATE SET name = excluded.name, url = excluded.url`,
		channelID, name, url,
	)
	if err != nil {
		return fmt.Errorf("adding channel %s: %w", channelID, err)
	}
	return nil
}

func (s *Store) RemoveChannel(ctx context.Context, channelID string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM channels WHERE channel_id = ?", channelID)
	if err != nil {
		return fmt.Errorf("removing channel %s: %w", channelID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("channel %s not found", channelID)
	}
	return nil
}

func (s *Store) ListChannels(ctx context.Context) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, channel_id, name, url, added_at, last_checked FROM channels ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("listing channels: %w", err)
	}
	defer rows.Close()

	var channels []Channel
	for rows.Next() {
		var ch Channel
		var addedAt string
		var lastChecked sql.NullString
		if err := rows.Scan(&ch.ID, &ch.ChannelID, &ch.Name, &ch.URL, &addedAt, &lastChecked); err != nil {
			return nil, fmt.Errorf("scanning channel: %w", err)
		}
		ch.AddedAt, _ = time.Parse("2006-01-02 15:04:05", addedAt)
		if lastChecked.Valid {
			t, _ := time.Parse("2006-01-02 15:04:05", lastChecked.String)
			ch.LastChecked = &t
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

func (s *Store) GetChannelByID(ctx context.Context, channelID string) (*Channel, error) {
	var ch Channel
	var addedAt string
	var lastChecked sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT id, channel_id, name, url, added_at, last_checked FROM channels WHERE channel_id = ?",
		channelID,
	).Scan(&ch.ID, &ch.ChannelID, &ch.Name, &ch.URL, &addedAt, &lastChecked)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting channel %s: %w", channelID, err)
	}
	ch.AddedAt, _ = time.Parse("2006-01-02 15:04:05", addedAt)
	if lastChecked.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", lastChecked.String)
		ch.LastChecked = &t
	}
	return &ch, nil
}

func (s *Store) UpdateLastChecked(ctx context.Context, channelID string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE channels SET last_checked = datetime('now') WHERE channel_id = ?",
		channelID,
	)
	return err
}
