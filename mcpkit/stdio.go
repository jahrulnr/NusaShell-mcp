package mcpkit

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/mark3labs/mcp-go/server"
)

// ServeStdio starts a stdio MCP server with graceful shutdown on SIGINT/SIGTERM.
// Diagnostics go to stderr; stdout is reserved for MCP protocol traffic.
// Returns nil on clean shutdown, non-nil on startup or runtime errors.
func ServeStdio(srv *server.MCPServer, name string) error {
	logger := log.New(os.Stderr, fmt.Sprintf("[%s] ", name), log.LstdFlags|log.Lmsgprefix)
	logger.Printf("starting stdio MCP server")

	if err := server.ServeStdio(srv, server.WithErrorLogger(logger)); err != nil {
		// context.Canceled is expected on graceful shutdown.
		if err == context.Canceled {
			logger.Printf("shutting down")
			return nil
		}
		return fmt.Errorf("stdio serve: %w", err)
	}
	logger.Printf("shutting down")
	return nil
}
