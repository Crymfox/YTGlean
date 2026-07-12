package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/CrymfoxLabs/YTGlean/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	dbPath   string
	logLevel string
	quiet    bool

	cfg *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "ytglean",
	Short: "Glean transcripts from YouTube channels",
	Long:  `YTGlean fetches transcripts from tracked YouTube channels, stores them in SQLite, summarizes them via LLM, and optionally exposes the data as an MCP server.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		setupLogging()
		if err := config.EnsureConfigDir(); err != nil {
			slog.Warn("could not create config directory", "error", err)
		}
		var err error
		cfg, err = config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		if dbPath != "" {
			cfg.Database.Path = dbPath
		}
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "", "database path override")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().BoolVar(&quiet, "quiet", false, "suppress non-error output")
}

func initConfig() {
	configDir, err := os.UserConfigDir()
	if err == nil {
		viper.AddConfigPath(filepath.Join(configDir, "ytglean"))
	}
	viper.AddConfigPath(".")
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			slog.Warn("error reading config file", "error", err)
		}
	}
}

func setupLogging() {
	var level slog.Level
	switch logLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	if quiet {
		opts.Level = slog.LevelError
	}
	handler := slog.NewTextHandler(os.Stderr, opts)
	slog.SetDefault(slog.New(handler))
}
