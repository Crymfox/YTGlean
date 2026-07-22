package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/CrymfoxLabs/YTGlean/internal/channel"
	"github.com/CrymfoxLabs/YTGlean/internal/db"
	"github.com/spf13/cobra"
)

var channelCmd = &cobra.Command{
	Use:   "channel",
	Short: "Manage tracked YouTube channels",
}

var channelAddCmd = &cobra.Command{
	Use:   "add <url-or-id>",
	Short: "Add a YouTube channel to track",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := db.Open(cfg.Database.Path)
		if err != nil {
			return err
		}
		defer store.Close()

		ctx := cmd.Context()
		input := args[0]
		name, _ := cmd.Flags().GetString("name")

		channelID, channelURL, resolvedName, err := channel.Resolve(ctx, input)
		if err != nil {
			return fmt.Errorf("resolving channel %q: %w", input, err)
		}

		if name == "" {
			name = resolvedName
		}
		if name == "" {
			name = channelID
		}

		if err := store.AddChannel(ctx, channelID, name, channelURL); err != nil {
			return err
		}

		fmt.Printf("Added channel: %s (%s)\n", name, channelID)
		return nil
	},
}

var channelRemoveCmd = &cobra.Command{
	Use:   "remove <url-or-id>",
	Short: "Remove a tracked channel",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := db.Open(cfg.Database.Path)
		if err != nil {
			return err
		}
		defer store.Close()

		ctx := cmd.Context()
		input := args[0]

		// Try as channel ID first
		channelID := input
		if !strings.HasPrefix(input, "UC") {
			// Try resolving as URL/handle
			resolved, _, _, resolveErr := channel.Resolve(ctx, input)
			if resolveErr != nil {
				// Try looking up by name in the database
				ch, nameErr := store.GetChannelByName(ctx, input)
				if nameErr != nil || ch == nil {
					return fmt.Errorf("channel %q not found by URL, handle, or name", input)
				}
				channelID = ch.ChannelID
			} else {
				channelID = resolved
			}
		}

		if err := store.RemoveChannel(ctx, channelID); err != nil {
			return err
		}

		fmt.Printf("Removed channel: %s\n", channelID)
		return nil
	},
}

var channelListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tracked channels",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := db.Open(cfg.Database.Path)
		if err != nil {
			return err
		}
		defer store.Close()

		channels, err := store.ListChannels(cmd.Context())
		if err != nil {
			return err
		}

		asJSON, _ := cmd.Flags().GetBool("json")
		if asJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(channels)
		}

		if len(channels) == 0 {
			fmt.Println("No channels tracked. Use 'ytglean channel add' to add one.")
			return nil
		}

		for _, ch := range channels {
			lastChecked := "never"
			if ch.LastChecked != nil {
				lastChecked = ch.LastChecked.Format("2006-01-02 15:04")
			}
			fmt.Printf("%-24s  %-30s  checked: %s\n", ch.ChannelID, ch.Name, lastChecked)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(channelCmd)
	channelCmd.AddCommand(channelAddCmd)
	channelCmd.AddCommand(channelRemoveCmd)
	channelCmd.AddCommand(channelListCmd)

	channelAddCmd.Flags().String("name", "", "display name for the channel")
	channelListCmd.Flags().Bool("json", false, "output as JSON")
}
