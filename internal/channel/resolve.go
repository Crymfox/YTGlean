// Package channel resolves YouTube channel handles, URLs, and IDs into a
// canonical channel ID, URL, and display name. Shared by the CLI channel
// commands and the MCP server.
package channel

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

var idRegex = regexp.MustCompile(`^UC[\w-]{22}$`)

// IsID reports whether input is already a canonical YouTube channel ID.
func IsID(input string) bool {
	return idRegex.MatchString(input)
}

// Resolve takes a URL, handle (@name), or channel ID and returns
// (channelID, channelURL, name, error).
func Resolve(ctx context.Context, input string) (string, string, string, error) {
	// Bare channel ID
	if idRegex.MatchString(input) {
		return input, "https://www.youtube.com/channel/" + input, "", nil
	}

	// Normalize handle to URL
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
