# studio-go

The Go client for the [Survival Servers Studio](https://studio.survivalservers.com/) API, game server hosting for
game studios. This repository also holds the `survivalservers` command-line tool, the MCP server for AI coding tools,
and gsbench. Standard library only. Apache-2.0.

## Install

```bash
go get github.com/survivalservers/studio-go
```

Requires Go 1.25 or newer.

## Start a game server

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	client "github.com/survivalservers/studio-go"
)

func main() {
	ctx := context.Background()
	c, err := client.New(client.DefaultURL, os.Getenv("GS_API_KEY"), nil, "")
	if err != nil {
		log.Fatal(err)
	}

	// Start a game server from the app's live branch.
	var gs map[string]any
	err = c.JSON(ctx, client.Call{Method: http.MethodPost, Path: "/v0/game-servers",
		Body: map[string]any{"app": "my-game", "branch": "live"}}, &gs)
	if err != nil {
		log.Fatal(err) // a *client.Problem says what to do next
	}

	// Wait until it is running, then print the address players join.
	gs, _, err = client.Wait(ctx, 5*time.Minute, 0, func(ctx context.Context) (map[string]any, error) {
		return c.Get(ctx, "/v0/game-servers/"+gs["id"].(string), nil)
	}, client.GameServerSettled, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(gs["status"], gs["address"])
}
```

## What it handles

- **Idempotency keys.** Every `POST` and `PATCH` gets an `Idempotency-Key`, random or yours.
- **Retries.** Up to 3 tries on `429 rate_limited` and `503 unavailable`, waiting `Retry-After` up to 30 seconds.
  `SetRetry` changes both.
- **Errors.** API errors come back as a `*Problem` with the error code, the request ID and the next step.
- **Uploads.** `UploadChunks` sends a Windows build as only the parts your studio doesn't have yet, so a new build of
  the same game uploads little. `UploadZip` sends a whole zip in parts.
- **Waiting.** `Wait` polls with backoff until a build, version, game server or persistent server settles.
- **Version checks.** `CheckVersion` checks a version's settings before you send them.
- **Logs and downloads.** `Stream` follows logs. `DownloadLink` gets a file's download link.

## In this repository

| Folder | What it is |
|---|---|
| [`cli`](cli/) | `survivalservers`, the command-line tool: upload builds, start game servers, follow logs |
| [`mcp`](mcp/) | The MCP server that lets Claude Code, Cursor and other AI tools run your game servers |
| [`localapi`](localapi/) | The local API, and `survivalservers dev`, which runs your game server on your computer the way our machines do |
| [`bench`](bench/) | gsbench: how many game servers a machine fits at 64 and 128 Hz, and how good its network is |

## Links

- [API reference](https://studio.survivalservers.com/docs/reference/overview/)
- [API clients for other languages](https://studio.survivalservers.com/docs/guides/api-clients/)
- [Changelog](CHANGELOG.md)

## License

Apache-2.0. See [LICENSE](LICENSE).
