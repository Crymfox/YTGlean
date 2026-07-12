package config

import (
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

type Config struct {
	Database   DatabaseConfig   `yaml:"database"   mapstructure:"database"`
	Transcript TranscriptConfig `yaml:"transcript" mapstructure:"transcript"`
	Summarizer SummarizerConfig `yaml:"summarizer" mapstructure:"summarizer"`
	MCP        MCPConfig        `yaml:"mcp"        mapstructure:"mcp"`
}

type DatabaseConfig struct {
	Path          string `yaml:"path"           mapstructure:"path"`
	RetentionDays int    `yaml:"retention_days" mapstructure:"retention_days"`
}

type TranscriptConfig struct {
	Provider      string   `yaml:"provider"       mapstructure:"provider"`
	Languages     []string `yaml:"languages"      mapstructure:"languages"`
	YTDLPVersion  string   `yaml:"ytdlp_version"  mapstructure:"ytdlp_version"`
	CookieFile    string   `yaml:"cookie_file"    mapstructure:"cookie_file"`
	MaxConcurrent int      `yaml:"max_concurrent" mapstructure:"max_concurrent"`
}

type SummarizerConfig struct {
	Endpoint  string `yaml:"endpoint"   mapstructure:"endpoint"`
	APIKey    string `yaml:"api_key"    mapstructure:"api_key"`
	Model     string `yaml:"model"      mapstructure:"model"`
	MaxTokens int    `yaml:"max_tokens" mapstructure:"max_tokens"`
}

type MCPConfig struct {
	Transport string `yaml:"transport" mapstructure:"transport"`
	Port      int    `yaml:"port"      mapstructure:"port"`
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".local", "share", "ytglean")
}

func defaultConfigDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "ytglean")
}

func Load() (*Config, error) {
	cfg := &Config{
		Database: DatabaseConfig{
			Path:          filepath.Join(defaultDataDir(), "ytglean.db"),
			RetentionDays: 30,
		},
		Transcript: TranscriptConfig{
			Provider:      "auto",
			Languages:     []string{"en"},
			MaxConcurrent: 3,
		},
		Summarizer: SummarizerConfig{
			Endpoint:  "https://api.openai.com/v1",
			Model:     "gpt-4o-mini",
			MaxTokens: 1024,
		},
		MCP: MCPConfig{
			Transport: "stdio",
			Port:      8080,
		},
	}

	if err := viper.Unmarshal(cfg); err != nil {
		return nil, err
	}

	// API key from env takes precedence
	if key := os.Getenv("YTGLEAN_API_KEY"); key != "" {
		cfg.Summarizer.APIKey = key
	}

	return cfg, nil
}

// EnsureConfigDir creates the config directory and a default config file if they don't exist.
func EnsureConfigDir() error {
	cfgDir := defaultConfigDir()
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return err
	}

	cfgFile := filepath.Join(cfgDir, "config.yaml")
	if _, err := os.Stat(cfgFile); os.IsNotExist(err) {
		defaultContent := `# YTGlean Configuration
# See README.md for options.

database:
  # path: ~/.local/share/ytglean/ytglean.db
  retention_days: 30

transcript:
  provider: auto        # auto | innertube | ytdlp
  languages: [en]
  # cookie_file: ""
  max_concurrent: 3

summarizer:
  # endpoint: https://api.openai.com/v1
  # api_key: set via YTGLEAN_API_KEY env var
  model: gpt-4o-mini
  max_tokens: 1024

mcp:
  transport: stdio      # stdio | http
  port: 8080
`
		if err := os.WriteFile(cfgFile, []byte(defaultContent), 0o644); err != nil {
			return err
		}
	}

	return nil
}
