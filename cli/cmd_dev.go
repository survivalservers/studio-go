package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/survivalservers/studio-go/localapi/dev"
	"github.com/survivalservers/studio-go/mcp/server"
)

// runDev is "survivalservers dev": the game server on this computer with the same GS_* variables, allocation file and
// local API as on our machines. Ctrl+C stops it as an API stop would.
func runDev(ctx context.Context, args []string, env Env) int {
	stop := make(chan os.Signal, 1)
	go func() {
		<-ctx.Done()
		stop <- os.Interrupt
	}()
	return dev.Run(args, env.Stdin, env.Stdout, env.Stderr, stop)
}

// runMCP is "survivalservers mcp": the MCP server for AI tools, over stdio (what an editor starts) or, with --http,
// hosted.
func runMCP(ctx context.Context, args []string, env Env) int {
	err := server.Serve(ctx, args, env.Stdout, env.Stderr)
	switch {
	case errors.Is(err, server.ErrUsage):
		return exitUsage
	case err != nil:
		fmt.Fprintln(env.Stderr, "survivalservers mcp:", err)
		return exitFailed
	}
	return exitOK
}
