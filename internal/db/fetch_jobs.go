package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Fetch job states.
const (
	JobStatePending      = "pending"
	JobStateInProgress   = "in_progress"
	JobStateFailed       = "failed"
	JobStateDead         = "dead"
	JobStateNoTranscript = "no_transcript"
)

// FetchJob is a durable transcript fetch task.
type FetchJob struct {
	ID          int64
	VideoID     string
	ChannelID   string
	Title       string
	State       string
	RetryCount  int
	LastError   *string
	NextRetryAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// EnqueueFetchJobs inserts jobs, skipping videos already queued in any state.
// Returns the number of jobs actually inserted.
func (s *Store) EnqueueFetchJobs(ctx context.Context, jobs []FetchJob) (int, error) {
	inserted := 0
	for _, job := range jobs {
		res, err := s.db.ExecContext(ctx,
			`INSERT INTO fetch_jobs (video_id, channel_id, title)
			 VALUES (?, ?, ?)
			 ON CONFLICT(video_id) DO NOTHING`,
			job.VideoID, job.ChannelID, job.Title,
		)
		if err != nil {
			return inserted, fmt.Errorf("enqueueing fetch job for %s: %w", job.VideoID, err)
		}
		n, _ := res.RowsAffected()
		inserted += int(n)
	}
	return inserted, nil
}

// ClaimFetchJobs atomically claims up to n eligible jobs (pending, or failed
// with a due retry) and marks them in_progress. Safe for concurrent callers.
func (s *Store) ClaimFetchJobs(ctx context.Context, n int) ([]FetchJob, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`UPDATE fetch_jobs
		 SET state = ?, updated_at = datetime('now')
		 WHERE id IN (
		     SELECT id FROM fetch_jobs
		     WHERE state = ?
		        OR (state = ? AND next_retry_at IS NOT NULL AND next_retry_at <= datetime('now'))
		     ORDER BY id
		     LIMIT ?
		 )
		 RETURNING id, video_id, channel_id, title, state, retry_count, last_error, next_retry_at, created_at, updated_at`,
		JobStateInProgress, JobStatePending, JobStateFailed, n,
	)
	if err != nil {
		return nil, fmt.Errorf("claiming fetch jobs: %w", err)
	}
	defer rows.Close()
	return scanFetchJobs(rows)
}

// CompleteFetchJob removes a successfully processed job.
// The stored transcript row is the durable record of success.
func (s *Store) CompleteFetchJob(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM fetch_jobs WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("completing fetch job %d: %w", id, err)
	}
	return nil
}

// FailFetchJob records a transient failure: increments retry_count, schedules
// the next retry with exponential backoff, and dead-letters at maxRetries.
func (s *Store) FailFetchJob(ctx context.Context, id int64, errMsg string, maxRetries int, baseRetryDelay time.Duration) error {
	baseSecs := int64(baseRetryDelay / time.Second)
	if baseSecs < 1 {
		baseSecs = 1
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE fetch_jobs SET
		     retry_count = retry_count + 1,
		     state = CASE WHEN retry_count + 1 >= ? THEN ? ELSE ? END,
		     next_retry_at = CASE WHEN retry_count + 1 >= ? THEN NULL
		         ELSE datetime('now', '+' || (? * (1 << min(retry_count, 10))) || ' seconds') END,
		     last_error = ?,
		     updated_at = datetime('now')
		 WHERE id = ?`,
		maxRetries, JobStateDead, JobStateFailed,
		maxRetries,
		baseSecs,
		errMsg, id,
	)
	if err != nil {
		return fmt.Errorf("failing fetch job %d: %w", id, err)
	}
	return nil
}

// MarkFetchJobNoTranscript marks a job as terminally without a transcript.
// The row persists so the video is never re-enqueued by feed discovery.
func (s *Store) MarkFetchJobNoTranscript(ctx context.Context, id int64, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE fetch_jobs SET state = ?, last_error = ?, next_retry_at = NULL, updated_at = datetime('now')
		 WHERE id = ?`,
		JobStateNoTranscript, errMsg, id,
	)
	if err != nil {
		return fmt.Errorf("marking fetch job %d no_transcript: %w", id, err)
	}
	return nil
}

// ReleaseFetchJobs returns claimed-but-unprocessed jobs to pending
// (graceful shutdown).
func (s *Store) ReleaseFetchJobs(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+2)
	args = append(args, JobStatePending)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, JobStateInProgress)
	_, err := s.db.ExecContext(ctx,
		`UPDATE fetch_jobs SET state = ?, updated_at = datetime('now')
		 WHERE id IN (`+strings.Join(placeholders, ",")+`) AND state = ?`,
		args...,
	)
	if err != nil {
		return fmt.Errorf("releasing fetch jobs: %w", err)
	}
	return nil
}

// ReclaimStaleFetchJobs resets in_progress jobs older than the given age back
// to pending (crash recovery). Returns the number of jobs reclaimed.
func (s *Store) ReclaimStaleFetchJobs(ctx context.Context, olderThan time.Duration) (int64, error) {
	secs := int64(olderThan / time.Second)
	res, err := s.db.ExecContext(ctx,
		`UPDATE fetch_jobs SET state = ?, updated_at = datetime('now')
		 WHERE state = ? AND updated_at < datetime('now', '-' || ? || ' seconds')`,
		JobStatePending, JobStateInProgress, secs,
	)
	if err != nil {
		return 0, fmt.Errorf("reclaiming stale fetch jobs: %w", err)
	}
	return res.RowsAffected()
}

// ListFetchJobs returns jobs, optionally filtered by state, newest first.
func (s *Store) ListFetchJobs(ctx context.Context, state string, limit int) ([]FetchJob, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, video_id, channel_id, title, state, retry_count, last_error, next_retry_at, created_at, updated_at
	          FROM fetch_jobs`
	args := []any{}
	if state != "" {
		query += " WHERE state = ?"
		args = append(args, state)
	}
	query += " ORDER BY updated_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing fetch jobs: %w", err)
	}
	defer rows.Close()
	return scanFetchJobs(rows)
}

// CountFetchJobsByState returns a state -> count map.
func (s *Store) CountFetchJobsByState(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT state, COUNT(*) FROM fetch_jobs GROUP BY state")
	if err != nil {
		return nil, fmt.Errorf("counting fetch jobs: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, fmt.Errorf("scanning fetch job count: %w", err)
		}
		counts[state] = count
	}
	return counts, rows.Err()
}

// RetryFetchJob resets a job (any state) to pending with a fresh retry budget.
func (s *Store) RetryFetchJob(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE fetch_jobs SET state = ?, retry_count = 0, next_retry_at = NULL, updated_at = datetime('now')
		 WHERE id = ?`,
		JobStatePending, id,
	)
	if err != nil {
		return fmt.Errorf("retrying fetch job %d: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("fetch job %d not found", id)
	}
	return nil
}

// RetryAllFailedFetchJobs makes all failed jobs immediately eligible again.
// Retry counts are kept so repeat offenders can still dead-letter.
func (s *Store) RetryAllFailedFetchJobs(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE fetch_jobs SET state = ?, next_retry_at = NULL, updated_at = datetime('now')
		 WHERE state = ?`,
		JobStatePending, JobStateFailed,
	)
	if err != nil {
		return 0, fmt.Errorf("retrying failed fetch jobs: %w", err)
	}
	return res.RowsAffected()
}

// ClearDeadFetchJobs removes dead and no_transcript jobs.
// Returns the number of jobs deleted.
func (s *Store) ClearDeadFetchJobs(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM fetch_jobs WHERE state IN (?, ?)",
		JobStateDead, JobStateNoTranscript,
	)
	if err != nil {
		return 0, fmt.Errorf("clearing dead fetch jobs: %w", err)
	}
	return res.RowsAffected()
}

func scanFetchJobs(rows *sql.Rows) ([]FetchJob, error) {
	var jobs []FetchJob
	for rows.Next() {
		var j FetchJob
		var lastError, nextRetryAt sql.NullString
		var createdAt, updatedAt string
		if err := rows.Scan(&j.ID, &j.VideoID, &j.ChannelID, &j.Title, &j.State, &j.RetryCount,
			&lastError, &nextRetryAt, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning fetch job: %w", err)
		}
		if lastError.Valid {
			j.LastError = &lastError.String
		}
		if nextRetryAt.Valid {
			t, _ := time.Parse("2006-01-02 15:04:05", nextRetryAt.String)
			j.NextRetryAt = &t
		}
		j.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		j.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
