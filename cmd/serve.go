package cmd

import (
	"fmt"

	"github.com/CrymfoxLabs/YTGlean/internal/db"
	mcpserver "github.com/CrymfoxLabs/YTGlean/internal/mcp"
	"github.com/CrymfoxLabs/YTGlean/internal/transcript"
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

		provider := transcript.NewProvider(cfg.Transcript.Provider, cfg.Transcript.CookieFile)
		s := mcpserver.NewServer(store, provider, cfg.Transcript.Languages, Version, &cfg.Summarizer)

		switch transport {
		case "http":
			addr := fmt.Sprintf(":%d", port)
			fmt.Printf("Starting MCP server on http://localhost%s\n", addr)
			httpServer := server.NewStreamableHTTPServer(s)
			return httpServer.Start(addr)
		default: // stdio
			return server.ServeStdio(s)
		}
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)

	serveCmd.Flags().String("transport", "stdio", "transport type (stdio or http)")
	serveCmd.Flags().Int("port", 8080, "port for HTTP transport")
}
