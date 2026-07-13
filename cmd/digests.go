package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/CrymfoxLabs/YTGlean/internal/db"
	"github.com/spf13/cobra"
)

var digestsCmd = &cobra.Command{
	Use:   "digests",
	Short: "Manage stored digest summaries",
}

var digestsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all stored digests",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := db.Open(cfg.Database.Path)
		if err != nil {
			return err
		}
		defer store.Close()

		digests, err := store.ListDigests(cmd.Context())
		if err != nil {
			return err
		}

		if len(digests) == 0 {
			fmt.Println("No digests found. Run 'ytglean summarize' to create one.")
			return nil
		}

		asJSON, _ := cmd.Flags().GetBool("json")
		if asJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(digests)
		}

		for _, d := range digests {
			channel := d.ChannelFilter
			if channel == "" {
				channel = "all"
			}
			fmt.Printf("#%-3d  %s  %d videos  channel=%s  model=%s\n",
				d.ID,
				d.CreatedAt.Format("2006-01-02 15:04"),
				d.VideoCount,
				channel,
				d.Model,
			)
		}
		return nil
	},
}

var digestsExportCmd = &cobra.Command{
	Use:   "export <id>",
	Short: "Export a digest to a markdown file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := db.Open(cfg.Database.Path)
		if err != nil {
			return err
		}
		defer store.Close()

		var id int64
		if _, err := fmt.Sscanf(args[0], "%d", &id); err != nil {
			return fmt.Errorf("invalid digest ID: %s", args[0])
		}

		digest, err := store.GetDigest(cmd.Context(), id)
		if err != nil {
			return err
		}
		if digest == nil {
			return fmt.Errorf("digest #%d not found", id)
		}

		outputFile, _ := cmd.Flags().GetString("output")
		if outputFile == "" {
			outputFile = fmt.Sprintf("digest-%d.md", id)
		}

		// Build markdown content
		var md strings.Builder
		fmt.Fprintf(&md, "# YTGlean Digest #%d\n\n", digest.ID)
		fmt.Fprintf(&md, "- **Created:** %s\n", digest.CreatedAt.Format("2006-01-02 15:04"))
		fmt.Fprintf(&md, "- **Window:** %s to %s\n", digest.WindowStart.Format("2006-01-02 15:04"), digest.WindowEnd.Format("2006-01-02 15:04"))
		fmt.Fprintf(&md, "- **Videos:** %d\n", digest.VideoCount)
		fmt.Fprintf(&md, "- **Model:** %s\n", digest.Model)
		if digest.ChannelFilter != "" {
			fmt.Fprintf(&md, "- **Channel:** %s\n", digest.ChannelFilter)
		}
		if digest.PromptTemplate != "" {
			fmt.Fprintf(&md, "- **Custom prompt:** yes\n")
		}
		fmt.Fprintf(&md, "\n---\n\n")
		md.WriteString(digest.DigestText)
		md.WriteString("\n")

		if err := os.WriteFile(outputFile, []byte(md.String()), 0o644); err != nil {
			return fmt.Errorf("writing file: %w", err)
		}

		fmt.Printf("Exported digest #%d to %s\n", id, outputFile)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(digestsCmd)
	digestsCmd.AddCommand(digestsListCmd)
	digestsCmd.AddCommand(digestsExportCmd)

	digestsListCmd.Flags().Bool("json", false, "output as JSON")
	digestsExportCmd.Flags().String("output", "", "output file path (default: digest-<id>.md)")
}
