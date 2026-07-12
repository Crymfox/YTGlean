package cmd

import (
	"fmt"
	"time"

	"github.com/CrymfoxLabs/YTGlean/internal/db"
	"github.com/spf13/cobra"
)

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove old transcripts and data",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := db.Open(cfg.Database.Path)
		if err != nil {
			return err
		}
		defer store.Close()

		ctx := cmd.Context()
		olderThanStr, _ := cmd.Flags().GetString("older-than")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		vacuum, _ := cmd.Flags().GetBool("vacuum")

		retention := time.Duration(cfg.Database.RetentionDays) * 24 * time.Hour
		if olderThanStr != "" {
			d, err := time.ParseDuration(olderThanStr)
			if err != nil {
				return fmt.Errorf("invalid --older-than duration: %w", err)
			}
			retention = d
		}

		cutoff := time.Now().Add(-retention)

		if dryRun {
			count, err := store.CountOlderThan(ctx, cutoff)
			if err != nil {
				return err
			}
			fmt.Printf("Would delete %d video(s) older than %s (before %s)\n",
				count, retention, cutoff.Format("2006-01-02 15:04"))
			return nil
		}

		deleted, err := store.PruneOlderThan(ctx, cutoff)
		if err != nil {
			return err
		}

		fmt.Printf("Pruned %d video(s) older than %s\n", deleted, retention)

		if vacuum {
			fmt.Println("Running VACUUM...")
			if err := store.Vacuum(ctx); err != nil {
				return fmt.Errorf("vacuum failed: %w", err)
			}
			fmt.Println("VACUUM complete.")
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(pruneCmd)

	pruneCmd.Flags().String("older-than", "", "retention period override (e.g. 30d, 720h)")
	pruneCmd.Flags().Bool("dry-run", false, "show what would be deleted")
	pruneCmd.Flags().Bool("vacuum", false, "run VACUUM after pruning to reclaim disk space")
}
