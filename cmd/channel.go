package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

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

		channelID, channelURL, resolvedName, err := resolveChannel(ctx, input)
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
			resolved, _, _, resolveErr := resolveChannel(ctx, input)
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

var channelIDRegex = regexp.MustCompile(`^UC[\w-]{22}$`)

// resolveChannel takes a URL, handle, or channel ID and returns (channelID, channelURL, name, error).
func resolveChannel(ctx context.Context, input string) (string, string, string, error) {
	// Bare channel ID
	if channelIDRegex.MatchString(input) {
		return input, "https://www.youtube.com/channel/" + input, "", nil
	}

	// Normalize URL
	if strings.HasPrefix(input, "@") {
		input = "https://www.youtube.com/" + input
	}

	return resolveWithHTTP(ctx, input)
}

// resolveWithHTTP fetches the YouTube page HTML and extracts the channel ID and name.
func resolveWithHTTP(ctx context.Context, url string) (string, string, string, error) {
	slog.Debug("resolving channel via HTTP", "url", url)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", "", "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("fetching page %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return "", "", "", fmt.Errorf("reading page body: %w", err)
	}
	html := string(body)

	// Extract channel ID
	var channelID string
	if m := regexp.MustCompile(`"browseId"\s*:\s*"(UC[\w-]{22})"`).FindStringSubmatch(html); len(m) > 1 {
		channelID = m[1]
	} else if m := regexp.MustCompile(`"externalId"\s*:\s*"(UC[\w-]{22})"`).FindStringSubmatch(html); len(m) > 1 {
		channelID = m[1]
	} else if m := regexp.MustCompile(`channel_id=(UC[\w-]{22})`).FindStringSubmatch(html); len(m) > 1 {
		channelID = m[1]
	}
	if channelID == "" {
		return "", "", "", fmt.Errorf("could not extract channel ID from %s", url)
	}

	// Extract channel name
	var name string
	if m := regexp.MustCompile(`"channelMetadataRenderer"\s*:\s*\{[^}]*"title"\s*:\s*"([^"]+)"`).FindStringSubmatch(html); len(m) > 1 {
		name = m[1]
	} else if m := regexp.MustCompile(`<title>([^<]+)\s*-\s*YouTube</title>`).FindStringSubmatch(html); len(m) > 1 {
		name = strings.TrimSpace(m[1])
	}

	return channelID, "https://www.youtube.com/channel/" + channelID, name, nil
}
