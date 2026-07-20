package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/CrymfoxLabs/YTGlean/internal/db"
	mcpserver "github.com/CrymfoxLabs/YTGlean/internal/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the MCP server",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := db.Open(cfg.Database.Path)
		if err != nil {
			return err
		}
		defer store.Close()

		transport, _ := cmd.Flags().GetString("transport")
		port, _ := cmd.Flags().GetInt("port")
		watch, _ := cmd.Flags().GetBool("watch")

		// The fetcher is shared by the MCP fetch_new tool and the watch
		// loop so rate limits are enforced process-wide.
		f := newFetcher(store)
		s := mcpserver.NewServer(store, f, cfg.Transcript.Languages, Version, &cfg.Summarizer)

		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		var watchDone chan struct{}
		if watch {
			w, err := buildWatcherWithFetcher(ctx, store, f, watcherOptions{
				interval:         cfg.Watch.FetchInterval,
				since:            24 * time.Hour,
				autoSummarize:    cfg.Watch.AutoSummarize,
				threshold:        cfg.Watch.SummarizeThreshold,
				summarizeChannel: cfg.Watch.SummarizeChannel,
			})
			if err != nil {
				return err
			}
			watchDone = make(chan struct{})
			go func() {
				defer close(watchDone)
				_ = w.Run(ctx)
			}()
		}

		var serveErr error
		switch transport {
		case "http":
			addr := fmt.Sprintf(":%d", port)
			fmt.Printf("Starting MCP server on http://localhost%s\n", addr)
			httpServer := server.NewStreamableHTTPServer(s)
			serveErr = httpServer.Start(addr)
		default: // stdio
			serveErr = server.ServeStdio(s)
		}

		// Give the watcher a chance to release claimed jobs
		if watchDone != nil {
			stop()
			select {
			case <-watchDone:
			case <-time.After(10 * time.Second):
				slog.Warn("watcher did not stop in time")
			}
		}
		return serveErr
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)

	serveCmd.Flags().String("transport", "stdio", "transport type (stdio or http)")
	serveCmd.Flags().Int("port", 8080, "port for HTTP transport")
	serveCmd.Flags().Bool("watch", false, "run the watch loop inside the server process")
}
