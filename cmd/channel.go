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

		channelID, channelURL, err := resolveChannel(ctx, input)
		if err != nil {
			return fmt.Errorf("resolving channel %q: %w", input, err)
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

		// Try as-is first (might be a channel ID)
		channelID := input
		if !strings.HasPrefix(input, "UC") {
			resolved, _, resolveErr := resolveChannel(ctx, input)
			if resolveErr != nil {
				return fmt.Errorf("resolving channel %q: %w", input, resolveErr)
			}
			channelID = resolved
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

var (
	channelIDRegex = regexp.MustCompile(`^UC[\w-]{22}$`)
	handleRegex    = regexp.MustCompile(`^@[\w.-]+$`)
)

// resolveChannel takes a URL, handle, or channel ID and returns (channelID, channelURL).
func resolveChannel(ctx context.Context, input string) (string, string, error) {
	// Bare channel ID
	if channelIDRegex.MatchString(input) {
		return input, "https://www.youtube.com/channel/" + input, nil
	}

	// Handle without URL
	if handleRegex.MatchString(input) {
		input = "https://www.youtube.com/" + input
	}

	// URL — fetch the page and extract channel ID
	if strings.Contains(input, "youtube.com") || strings.Contains(input, "youtu.be") {
		return resolveFromURL(ctx, input)
	}

	return "", "", fmt.Errorf("unrecognized input format: %q (use a URL, @Handle, or UCxxxx channel ID)", input)
}

func resolveFromURL(ctx context.Context, url string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	// Bypass YouTube consent page
	req.Header.Set("Cookie", "CONSENT=PENDING+999; SOCS=CAISEwgDEgk0ODE3Nzk3MjQaAmVuIAEaBgiA_LyaBg")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetching channel page: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return "", "", fmt.Errorf("reading channel page: %w", err)
	}

	html := string(body)

	// Try to extract externalId from ytInitialData
	externalIDRe := regexp.MustCompile(`"externalId"\s*:\s*"(UC[\w-]{22})"`)
	if m := externalIDRe.FindStringSubmatch(html); len(m) > 1 {
		channelID := m[1]
		channelURL := "https://www.youtube.com/channel/" + channelID
		// Try to extract channel name
		nameRe := regexp.MustCompile(`<meta\s+property="og:title"\s+content="([^"]+)"`)
		if nm := nameRe.FindStringSubmatch(html); len(nm) > 1 {
			slog.Debug("resolved channel", "name", nm[1], "id", channelID)
		}
		return channelID, channelURL, nil
	}

	// Try canonical link
	canonicalRe := regexp.MustCompile(`<link\s+rel="canonical"\s+href="https://www\.youtube\.com/channel/(UC[\w-]{22})"`)
	if m := canonicalRe.FindStringSubmatch(html); len(m) > 1 {
		return m[1], "https://www.youtube.com/channel/" + m[1], nil
	}

	return "", "", fmt.Errorf("could not extract channel ID from %s", url)
}
