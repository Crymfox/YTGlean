package transcript

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lrstanley/go-ytdlp"
)

// YtDlpProvider fetches transcripts using yt-dlp via the go-ytdlp wrapper.
type YtDlpProvider struct {
	cookieFile string
	once       sync.Once
}

// NewYtDlpProvider creates a new yt-dlp based transcript provider.
func NewYtDlpProvider(cookieFile string) *YtDlpProvider {
	return &YtDlpProvider{cookieFile: cookieFile}
}

func (p *YtDlpProvider) Name() string { return "ytdlp" }

func (p *YtDlpProvider) ensureInstalled(ctx context.Context) {
	p.once.Do(func() {
		slog.Info("ensuring yt-dlp is installed")
		ytdlp.MustInstall(ctx, nil)
	})
}

func (p *YtDlpProvider) FetchTranscript(ctx context.Context, videoID string, languages []string) (*Transcript, error) {
	p.ensureInstalled(ctx)

	tmpDir, err := os.MkdirTemp("", "ytglean-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	lang := "en"
	if len(languages) > 0 {
		lang = strings.Join(languages, ",")
	}

	dl := ytdlp.New().
		WriteSubs().
		WriteAutoSubs().
		SubLangs(lang).
		SubFormat("json3").
		SkipDownload().
		Output("%(id)s")

	if p.cookieFile != "" {
		dl.Cookies(p.cookieFile)
	}

	url := "https://www.youtube.com/watch?v=" + videoID

	slog.Debug("fetching transcript via yt-dlp", "video", videoID, "lang", lang)

	result, err := dl.Run(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed for %s: %w", videoID, err)
	}

	_ = result // result contains metadata but we need the subtitle file

	// Find the json3 file in the temp dir
	subtitleFile, err := findSubtitleFile(tmpDir, videoID)
	if err != nil {
		// Also check current directory as fallback
		cwd, _ := os.Getwd()
		subtitleFile, err = findSubtitleFile(cwd, videoID)
		if err != nil {
			return nil, fmt.Errorf("subtitle file not found for %s: %w", videoID, err)
		}
	}
	defer os.Remove(subtitleFile) // clean up the subtitle file

	data, err := os.ReadFile(subtitleFile)
	if err != nil {
		return nil, fmt.Errorf("reading subtitle file: %w", err)
	}

	segments, err := parseJSON3(data)
	if err != nil {
		return nil, fmt.Errorf("parsing json3 for %s: %w", videoID, err)
	}

	fullText := buildFullText(segments)
	actualLang := lang
	if idx := strings.Index(lang, ","); idx > 0 {
		actualLang = lang[:idx]
	}

	return &Transcript{
		VideoID:  videoID,
		Language: actualLang,
		Segments: segments,
		RawJSON:  string(data),
		FullText: fullText,
		Provider: "ytdlp",
	}, nil
}

func findSubtitleFile(dir, videoID string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, videoID) && strings.HasSuffix(name, ".json3") {
			return filepath.Join(dir, name), nil
		}
	}
	return "", fmt.Errorf("no .json3 file found for %s in %s", videoID, dir)
}

// json3Event represents an event in YouTube's json3 subtitle format.
type json3Event struct {
	TStartMs    int64      `json:"tStartMs"`
	DDurationMs int64      `json:"dDurationMs"`
	Segs        []json3Seg `json:"segs"`
}

type json3Seg struct {
	UTF8 string `json:"utf8"`
}

type json3Root struct {
	Events []json3Event `json:"events"`
}

func parseJSON3(data []byte) ([]Segment, error) {
	var root json3Root
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("unmarshaling json3: %w", err)
	}

	var segments []Segment
	for _, ev := range root.Events {
		if len(ev.Segs) == 0 {
			continue
		}
		var text strings.Builder
		for _, seg := range ev.Segs {
			text.WriteString(seg.UTF8)
		}
		t := strings.TrimSpace(text.String())
		if t == "" {
			continue
		}
		segments = append(segments, Segment{
			Text:    t,
			StartMs: ev.TStartMs,
			EndMs:   ev.TStartMs + ev.DDurationMs,
		})
	}
	return segments, nil
}

func buildFullText(segments []Segment) string {
	var b strings.Builder
	for i, seg := range segments {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(seg.Text)
	}
	return b.String()
}
