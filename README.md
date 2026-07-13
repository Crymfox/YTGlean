# YTGlean

Glean transcripts from YouTube channels, store them in SQLite, summarize via LLM, and expose the data as an MCP server.

## Features

- Track YouTube channels and automatically fetch new video transcripts
- Dual transcript providers: InnerTube API (fast, pure Go) with yt-dlp fallback
- Automatic channel ID and name resolution from handles/URLs
- Language fallback: tries all configured transcript languages before failing
- Retry with exponential backoff on transient failures
- Rate limiting protection with configurable fetch delay
- Summarize transcripts via any OpenAI-compatible API (OpenAI, Ollama, LM Studio, etc.)
- Full-text search across stored transcripts
- MCP server for AI agent integration (Claude Desktop, Cursor, etc.)
- Configurable data retention and pruning

## Prerequisites

- **yt-dlp** (optional, for transcript fallback): `pip install yt-dlp`
- **LLM API** (for summarization): OpenAI API key, or a local Ollama/LM Studio instance

## Install

```bash
go install github.com/CrymfoxLabs/YTGlean@latest
```

Or build from source:

```bash
git clone https://github.com/CrymfoxLabs/YTGlean.git
cd YTGlean
go build -o ytglean .
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
```

MCP tools available: `list_channels`, `search_transcripts`, `get_transcript`, `get_summary`, `get_recent_videos`, `fetch_new`.

### Global Flags

| Flag | Description |
|---|---|
| `--db <path>` | Override database path |
| `--log-level <level>` | debug, info, warn, error |
| `--quiet` | Suppress non-error output |

## License

MIT
