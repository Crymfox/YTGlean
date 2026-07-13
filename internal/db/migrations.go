package db

import (
	"fmt"
	"log/slog"
)

var migrations = []string{
	// Migration 001: Initial schema
	`CREATE TABLE IF NOT EXISTS channels (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id  TEXT NOT NULL UNIQUE,
		name        TEXT NOT NULL DEFAULT '',
		url         TEXT NOT NULL DEFAULT '',
		added_at    TEXT NOT NULL DEFAULT (datetime('now')),
		last_checked TEXT
	);

	CREATE TABLE IF NOT EXISTS videos (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		video_id     TEXT NOT NULL UNIQUE,
		channel_id   TEXT NOT NULL,
		title        TEXT NOT NULL DEFAULT '',
		published_at TEXT,
		fetched_at   TEXT NOT NULL DEFAULT (datetime('now')),
		duration     INTEGER,
		FOREIGN KEY (channel_id) REFERENCES channels(channel_id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS transcripts (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		video_id     TEXT NOT NULL,
		language     TEXT NOT NULL DEFAULT 'en',
		content_json TEXT,
		content_text TEXT,
		provider     TEXT NOT NULL DEFAULT '',
		created_at   TEXT NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (video_id) REFERENCES videos(video_id) ON DELETE CASCADE,
		UNIQUE(video_id, language)
	);

	CREATE TABLE IF NOT EXISTS digests (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		window_start    TEXT NOT NULL,
		window_end      TEXT NOT NULL,
		channel_filter  TEXT NOT NULL DEFAULT '',
		model           TEXT NOT NULL,
		prompt_template TEXT NOT NULL DEFAULT '',
		digest_text     TEXT NOT NULL,
		video_count     INTEGER NOT NULL DEFAULT 0,
		created_at      TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE INDEX IF NOT EXISTS idx_videos_channel_id ON videos(channel_id);
	CREATE INDEX IF NOT EXISTS idx_videos_published_at ON videos(published_at);
	CREATE INDEX IF NOT EXISTS idx_transcripts_video_id ON transcripts(video_id);
	CREATE INDEX IF NOT EXISTS idx_digests_window ON digests(window_start, window_end);`,

	// Migration 002: Add video_ids to digests for dedup
	`ALTER TABLE digests ADD COLUMN video_ids TEXT NOT NULL DEFAULT '[]';`,
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	if err != nil {
		return fmt.Errorf("creating schema_version table: %w", err)
	}

	var current int
	row := s.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version")
	if err := row.Scan(&current); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}

	for i := current; i < len(migrations); i++ {
		version := i + 1
		slog.Debug("applying migration", "version", version)

		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("beginning migration %d: %w", version, err)
		}

		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("applying migration %d: %w", version, err)
		}

		if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (?)", version); err != nil {
			tx.Rollback()
			return fmt.Errorf("recording migration %d: %w", version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing migration %d: %w", version, err)
		}

		slog.Info("applied migration", "version", version)
	}

	return nil
}
