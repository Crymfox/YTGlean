package feed

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"time"
)

const feedURL = "https://www.youtube.com/feeds/videos.xml?channel_id=%s"

// Entry represents a video from a YouTube channel's RSS feed.
type Entry struct {
	VideoID     string
	Title       string
	Published   time.Time
	ChannelName string
}

// atomFeed is the XML structure for YouTube's Atom feed.
type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Title   string      `xml:"title"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	VideoID   string `xml:"http://www.youtube.com/xml/schemas/2015 videoId"`
	Title     string `xml:"title"`
	Published string `xml:"published"`
	Author    struct {
		Name string `xml:"name"`
	} `xml:"author"`
}

// FetchNewVideos retrieves videos from a channel's RSS feed published after `since`.
func FetchNewVideos(ctx context.Context, channelID string, since time.Time) ([]Entry, error) {
	url := fmt.Sprintf(feedURL, channelID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "YTGlean/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching feed for %s: %w", channelID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feed returned status %d for channel %s", resp.StatusCode, channelID)
	}

	var f atomFeed
	if err := xml.NewDecoder(resp.Body).Decode(&f); err != nil {
		return nil, fmt.Errorf("parsing feed for %s: %w", channelID, err)
	}

	var entries []Entry
	for _, e := range f.Entries {
		pub, err := time.Parse(time.RFC3339, e.Published)
		if err != nil {
			continue
		}
		if pub.Before(since) {
			continue
		}
		entries = append(entries, Entry{
			VideoID:     e.VideoID,
			Title:       e.Title,
			Published:   pub,
			ChannelName: e.Author.Name,
		})
	}

	return entries, nil
}

// FetchAllVideos retrieves all videos from a channel's RSS feed (up to 15 most recent).
func FetchAllVideos(ctx context.Context, channelID string) ([]Entry, error) {
	return FetchNewVideos(ctx, channelID, time.Time{})
}
