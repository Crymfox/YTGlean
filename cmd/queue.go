package cmd

import (
	"fmt"
	"strings"

	"github.com/CrymfoxLabs/YTGlean/internal/db"
	"github.com/spf13/cobra"
)

var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Inspect and manage the fetch job queue",
}

var queueListCmd = &cobra.Command{
	Use:   "list",
	Short: "List fetch jobs",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := db.Open(cfg.Database.Path)
		if err != nil {
			return err
		}
		defer store.Close()

		ctx := cmd.Context()
		state, _ := cmd.Flags().GetString("state")
		limit, _ := cmd.Flags().GetInt("limit")

		jobs, err := store.ListFetchJobs(ctx, state, limit)
		if err != nil {
			return err
		}

		counts, err := store.CountFetchJobsByState(ctx)
		if err != nil {
			return err
		}

		if len(jobs) == 0 {
			fmt.Println("Queue is empty.")
			return nil
		}

		fmt.Printf("%-6s %-13s %-14s %-8s %-20s %s\n", "ID", "VIDEO", "STATE", "RETRIES", "NEXT RETRY", "ERROR")
		for _, j := range jobs {
			nextRetry := "-"
			if j.NextRetryAt != nil {
				nextRetry = j.NextRetryAt.Format("2006-01-02 15:04:05")
			}
			lastErr := "-"
			if j.LastError != nil {
				lastErr = *j.LastError
				if len(lastErr) > 60 {
					lastErr = lastErr[:57] + "..."
				}
			}
			fmt.Printf("%-6d %-13s %-14s %-8d %-20s %s\n",
				j.ID, j.VideoID, j.State, j.RetryCount, nextRetry, lastErr)
		}

		var summary []string
		for _, s := range []string{db.JobStatePending, db.JobStateInProgress, db.JobStateFailed, db.JobStateDead, db.JobStateNoTranscript} {
			if counts[s] > 0 {
				summary = append(summary, fmt.Sprintf("%d %s", counts[s], s))
			}
		}
		fmt.Printf("\nTotal: %s\n", strings.Join(summary, ", "))
		return nil
	},
}

var queueRetryCmd = &cobra.Command{
	Use:   "retry",
	Short: "Reset a job to pending with a fresh retry budget",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := db.Open(cfg.Database.Path)
		if err != nil {
			return err
		}
		defer store.Close()

		id, _ := cmd.Flags().GetInt64("id")
		if id == 0 {
			return fmt.Errorf("--id is required")
		}
		if err := store.RetryFetchJob(cmd.Context(), id); err != nil {
			return err
		}
		fmt.Printf("Job %d reset to pending.\n", id)
		return nil
	},
}

var queueRetryAllCmd = &cobra.Command{
	Use:   "retry-all",
	Short: "Make all failed jobs immediately eligible for retry",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := db.Open(cfg.Database.Path)
		if err != nil {
			return err
		}
		defer store.Close()

		n, err := store.RetryAllFailedFetchJobs(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Printf("%d failed job(s) reset to pending.\n", n)
		return nil
	},
}

var queueClearDeadCmd = &cobra.Command{
	Use:   "clear-dead",
	Short: "Remove dead and no-transcript jobs from the queue",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := db.Open(cfg.Database.Path)
		if err != nil {
			return err
		}
		defer store.Close()

		n, err := store.ClearDeadFetchJobs(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Printf("%d job(s) removed.\n", n)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(queueCmd)
	queueCmd.AddCommand(queueListCmd, queueRetryCmd, queueRetryAllCmd, queueClearDeadCmd)

	queueListCmd.Flags().String("state", "", "filter by state (pending|in_progress|failed|dead|no_transcript)")
	queueListCmd.Flags().Int("limit", 50, "maximum jobs to show")
	queueRetryCmd.Flags().Int64("id", 0, "job ID to retry (see 'queue list')")
}
