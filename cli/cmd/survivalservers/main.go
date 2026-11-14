// Command survivalservers runs a game's dedicated servers from a studio's own computer or its CI: builds, versions, game
// servers, logs, persistent servers and ready servers through the public API. survivalservers help lists the commands.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/survivalservers/studio-go/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], cli.Env{Stdout: os.Stdout, Stderr: os.Stderr})
	stop()
	os.Exit(code)
}
