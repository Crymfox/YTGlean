# YTGlean

Glean transcripts from YouTube channels, store them in SQLite, summarize via LLM, and expose the data as an MCP server.

## Features

- Track YouTube channels and automatically fetch new video transcripts
- Dual transcript providers: InnerTube API (fast, pure Go) with yt-dlp fallback
- Summarize transcripts via any OpenAI-compatible API (OpenAI, Ollama, LM Studio, etc.)
- Full-text search across stored transcripts
- MCP server for AI agent integration (Claude Desktop, Cursor, etc.)
- Automatic channel ID and name resolution from handles/URLs
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

```bash
# 1. Add a channel
./ytglean channel add @Fireship --name "Fireship"

# 2. Fetch transcripts
./ytglean fetch

# 3. Summarize
./ytglean summarize

# 4. Search transcripts via MCP server
./ytglean serve
```

## Configuration

YTGlean uses Viper for configuration. Config is loaded from (in order of priority):

1. `--config` flag
2. `YTGLEAN_CONFIG` environment variable
3. `~/.config/ytglean/config.yaml`
4. `./config.yaml`

### Environment Variables

| Variable | Description | Default |
|---|---|---|
| `YTGLEAN_API_KEY` | API key for OpenAI-compatible LLM | — |
| `YTGLEAN_ENDPOINT` | LLM API endpoint | `https://api.openai.com/v1` |
| `YTGLEAN_CONFIG` | Path to config file | — |

### Config File Example

```yaml
database:
  path: ~/.local/share/ytglean/ytglean.db
  retention_days: 30

transcript:
  provider: auto          # auto | innertube | ytdlp
  languages: [en]
  max_concurrent: 3

summarizer:
  endpoint: https://api.openai.com/v1
  model: gpt-4o-mini
  max_tokens: 1024

mcp:
  transport: stdio        # stdio | http
  port: 8080
```

Copy `.env.example` to `.env` and fill in your API key:

```bash
cp .env.example .env
```

## Usage

### Channel Management

```bash
ytglean channel add <url-or-id> [--name "Name"]
ytglean channel remove <url-or-id-or-name>
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
ytglean summarize                          # All unsummarized transcripts
ytglean summarize --video <id>             # Specific video
ytglean summarize --channel <id>           # All from a channel
ytglean summarize --re-summarize           # Regenerate existing summaries
ytglean summarize --prompt "Custom..."     # Custom prompt
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
```

MCP tools available: `list_channels`, `search_transcripts`, `get_transcript`, `get_summary`, `get_recent_videos`, `fetch_new`.

### Global Flags

| Flag | Description |
|---|---|
| `--config <path>` | Override config file |
| `--db <path>` | Override database path |
| `--log-level <level>` | debug, info, warn, error |
| `--quiet` | Suppress non-error output |

## License

MIT
