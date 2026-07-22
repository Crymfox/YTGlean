# YTGlean

Glean transcripts from YouTube channels, store them in SQLite, summarize via LLM, and expose the data as an MCP server.

## Features

- Track YouTube channels and automatically fetch new video transcripts
- Dual transcript providers: InnerTube API (fast, pure Go) with yt-dlp fallback
- Automatic channel ID and name resolution from handles/URLs
- Language fallback: tries all configured transcript languages before failing
- Durable fetch queue: failed fetches retry with exponential backoff and dead-letter after repeated failures
- Watch mode: continuous daemon that fetches on an interval and optionally auto-summarizes
- Retry with exponential backoff on transient failures
- Rate limiting protection with configurable fetch delay
- Summarize transcripts via any OpenAI-compatible API (OpenAI, Ollama, LM Studio, etc.)
- Full-text search across stored transcripts (SQLite FTS5, relevance-ranked)
- MCP server for AI agent integration (Claude Desktop, Cursor, etc.)
- Configurable data retention and pruning

## Prerequisites

- **yt-dlp** (optional, for transcript fallback): `pip install yt-dlp`
- **LLM API** (for summarization): OpenAI API key, or a local Ollama/LM Studio instance

## Install

### macOS (Homebrew)

```bash
brew install --cask Crymfox/tap/ytglean
```

### Windows (Scoop)

```powershell
scoop bucket add crymfox https://github.com/Crymfox/scoop-bucket
scoop install ytglean
```

### Arch Linux (AUR)

```bash
# Using yay
yay -S ytglean-bin

# Using paru
paru -S ytglean-bin
```

### Go

```bash
go install github.com/CrymfoxLabs/YTGlean@latest
```

### Build from source

```bash
git clone https://github.com/CrymfoxLabs/YTGlean.git
cd YTGlean
go build -o ytglean .
```

## Use with AI Agents

YTGlean runs as an MCP server, giving AI coding agents full access to YouTube transcript data. Add it to your agent's config:

### Claude Desktop

Edit `~/.config/claude/claude_desktop_config.json` (Linux) or `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS):

```json
{
  "mcpServers": {
    "ytglean": {
      "command": "ytglean",
      "args": ["serve"]
    }
  }
}
```

### Claude Code

```bash
claude mcp add ytglean -- ytglean serve
```

### Cursor

Create `.cursor/mcp.json` in your project root:

```json
{
  "mcpServers": {
    "ytglean": {
      "command": "ytglean",
      "args": ["serve"]
    }
  }
}
```

### Cline

Edit MCP settings (VS Code: Cline panel → MCP Servers → Configure MCP Servers):

```json
{
  "mcpServers": {
    "ytglean": {
      "command": "ytglean",
      "args": ["serve"],
      "disabled": false,
      "autoApprove": []
    }
  }
}
```

### OpenCode

Add to `opencode.json` in your project root:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "ytglean": {
      "type": "local",
      "command": ["ytglean", "serve"],
      "enabled": true
    }
  }
}
```

> **Tip:** Add `"args": ["serve", "--watch"]` to also run the background fetch loop inside the server process.
```

## Quick Start

On first run, YTGlean auto-generates a config file at `~/.config/ytglean/config.yaml`.
Edit it to set your API key and preferred settings.

```bash
# 1. Add a channel (name is auto-extracted from YouTube)
./ytglean channel add @Fireship

# 2. Fetch transcripts
./ytglean fetch

# 3. Summarize
./ytglean summarize

# 4. Search transcripts via MCP server
./ytglean serve
```

## Configuration

On first run, YTGlean creates `~/.config/ytglean/config.yaml` with sensible defaults. Edit this file to customize settings.

Config is loaded from (in order of priority):

1. `--config` flag
2. `~/.config/ytglean/config.yaml`
3. `./config.yaml`

### Config File Example

```yaml
database:
  path: ~/.local/share/ytglean/ytglean.db
  retention_days: 30

transcript:
  provider: auto          # auto | innertube | ytdlp
  languages: [en]         # tried in order, first available wins
  max_concurrent: 3
  fetch_delay: 2s         # delay between requests to avoid rate limiting

fetch:
  max_retries: 5          # attempts before a job is dead-lettered
  base_retry_delay: 30s   # doubles each retry (30s, 1m, 2m, 4m, 8m)

watch:
  fetch_interval: 30m     # how often watch mode checks for new videos
  auto_summarize: false
  summarize_threshold: 5  # min new transcripts before auto-summarize
  summarize_channel: ""   # optional channel filter for auto-digests

summarizer:
  endpoint: https://api.openai.com/v1
  api_key: your-api-key-here
  model: gpt-4o-mini
  max_tokens: 2048

mcp:
  transport: stdio        # stdio | http
  port: 8080
```

## Usage

### Channel Management

```bash
ytglean channel add <url-or-id>              # Name auto-extracted from YouTube
ytglean channel add @Fireship --name "Fire"  # Override display name
ytglean channel remove <url-or-id-or-name>   # Remove by URL, ID, or name
ytglean channel list [--json]
```

Channel inputs accept:
- Handles: `@Fireship`
- Full URLs: `https://www.youtube.com/@Fireship`
- Channel IDs: `UCsBjURrPoezykLs9EqgamOA`
- Names (for remove): `Fireship`

### Fetch Transcripts

```bash
ytglean fetch                              # All channels, last 24h
ytglean fetch --channel <id>               # Specific channel
ytglean fetch --since 7d                   # Last 7 days
ytglean fetch --all                        # All videos in feed
ytglean fetch --dry-run                    # Preview without fetching
```

Fetches are backed by a durable job queue: transient failures are retried with exponential backoff on subsequent runs, and jobs that keep failing are dead-lettered. Videos with no transcript available are remembered so they aren't re-attempted every run.

### Fetch Queue

```bash
ytglean queue list                         # Show queued/failed/dead jobs
ytglean queue list --state failed          # Filter by state
ytglean queue retry --id 42                # Reset a job with a fresh retry budget
ytglean queue retry-all                    # Make all failed jobs retry now
ytglean queue clear-dead                   # Remove dead and no-transcript jobs
```

### Watch Mode

```bash
ytglean watch                              # Fetch every 30m (config: watch.fetch_interval)
ytglean watch --fetch-interval 15m         # Custom interval
ytglean watch --auto-summarize             # Digest after enough new transcripts
ytglean watch --auto-summarize --summarize-threshold 10
ytglean watch --channel <id>               # Watch a single channel
```

Runs until interrupted (SIGINT/SIGTERM); shuts down gracefully, releasing any in-flight queue claims.

### Summarize

```bash
ytglean summarize                          # Summarize transcripts from last 24h
ytglean summarize --since 7d               # Last 7 days
ytglean summarize --channel <id>           # Filter to specific channel
ytglean summarize --video <id>             # Summarize a specific video
ytglean summarize --re-summarize           # Force re-summarize even if videos unchanged
ytglean summarize --prompt "Custom..."     # Custom system prompt
```

Skips automatically if the same set of videos was already summarized (compare with `--re-summarize` to force).

### Digests

```bash
ytglean digests list                       # List all stored digests
ytglean digests list --json                # Output as JSON
ytglean digests export <id>                # Export digest to markdown (digest-<id>.md)
ytglean digests export <id> -o summary.md  # Export to custom file
```

### Prune Old Data

```bash
ytglean prune                              # Use config retention (default 30d)
ytglean prune --older-than 60d             # Override retention period
ytglean prune --dry-run                    # Preview what would be deleted
ytglean prune --vacuum                     # Compact DB after pruning
```

### MCP Server

```bash
ytglean serve                              # stdio (default, for Claude Desktop / Cursor)
ytglean serve --transport http             # HTTP transport
ytglean serve --port 8080                  # Custom port
ytglean serve --watch                      # Also run the watch loop in-process
```

MCP tools available:

| Tool | Description |
|---|---|
| `list_channels` | List all tracked channels |
| `add_channel` | Add a YouTube channel by handle, URL, or ID (name auto-resolved) |
| `remove_channel` | Remove a tracked channel by ID or name |
| `search_transcripts` | Full-text search (FTS5, bm25-ranked, `term*` prefix matching) with video titles, channel names, word-bounded excerpts |
| `get_transcript` | Get full transcript (supports language, max_chars, timestamped format) |
| `get_video_info` | Video metadata without full transcript (title, channel, word count) |
| `get_recent_videos` | List recent videos from tracked channels |
| `list_videos` | Browse all stored videos with transcript status |
| `fetch_new` | Fetch new transcripts from YouTube (durable queue, rate-limited, retries, dry-run preview) |
| `list_digests` | List stored summaries with metadata |
| `get_digest` | Read a specific digest's full text |
| `summarize` | Summarize via LLM (requires API key) or guide agent to self-summarize |
| `queue_list` | Inspect fetch queue jobs (filter by state, see errors and retry info) |
| `queue_retry` | Reset a specific failed job with a fresh retry budget |
| `queue_retry_all` | Make all failed jobs immediately eligible for retry |

### Global Flags

| Flag | Description |
|---|---|
| `--db <path>` | Override database path |
| `--log-level <level>` | debug, info, warn, error |
| `--quiet` | Suppress non-error output |

## License

MIT
