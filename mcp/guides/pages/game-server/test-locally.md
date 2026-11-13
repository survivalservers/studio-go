# Test on your own PC

> Run your game server on your own computer as our machines run it, with the command-line tool's dev command, before you upload a build.

`survivalservers dev`, a command of the [command-line tool](https://studio.survivalservers.com/docs/guides/command-line/), runs your server on your own computer the way our machines do. Your server gets the same `GS_*` variables, the same allocation file and the same [local API](https://studio.survivalservers.com/docs/game-server/local-api/) on `127.0.0.1`.

## Get it

It is part of the command-line tool, which builds with Go for Windows, Linux and macOS. Run this in the tool's source folder, the one with `client`, `localapi` and `mcp` beside it:

```bash
go build -o survivalservers ./cmd/survivalservers
```

Or download it: see [Get it](https://studio.survivalservers.com/docs/guides/command-line/#get-it).

## Run your server with it

Put your server's command after `--`:

```bash
survivalservers dev -- ./MyServer -log
survivalservers dev -dir ./WindowsServer -- ./BreakwireServer.exe -log
```

A server that makes a few calls, started with match settings, prints this (shortened: it also lists every `GS_*` variable):

```bash
survivalservers dev -external-id 88231 -label queue=ranked -env MATCH_MODE=ranked -arg -MaxPlayers=10 -stop local_api -- ./server.sh
```

```text
[dev 14:12:06.914] allocated: {"game_server_id":"gs_dev5936899229",...,"external_id":"88231",...}
[dev 14:12:06.914] starting ./server.sh as gs_dev5936899229 (local API http://127.0.0.1:7600, fresh start)
[dev 14:12:06.914]   GS_PORT_GAME=7777
[dev 14:12:06.929] ready after 15ms
[dev 14:12:07.005] players: 2 [p_1 p_2]
[dev 14:12:07.021] the server asked to stop: "match_over"
[dev 14:12:07.021] stop requested (self): shutdown notice sent, the program is ended in 30s if still running
[dev 14:12:07.147] the server exited after the stop request with code 0 after 233ms
```

- `-dir` is the server's folder and its working folder; the default is the current folder. The program after `--` is found relative to it.
- The allocation file is `<dir>/.gs/allocation.json`. `GS_LOG_DIR` and `GS_ARTIFACT_DIR` are under `<dir>/.gs` too.
- Your server gets `GS_PORT_GAME=7777`. Name your own ports with `-port-map game=7777/udp`, once per port; they replace the default.
- The local API listens on `127.0.0.1:7600`. If that port is taken, `-port 0` picks a free one.
- `GS_CPU` is `1` and `GS_MEMORY_MB` is `2048`; `-cpu 4 -memory-mb 8192` sets your version's size. `GS_PUBLIC_IP` is `127.0.0.1`, and the region and continent are `local`.
- `survivalservers dev` prints every ready, health, players, labels, tick, saved and stop call. It does not print the waits.

Connect your game client to `127.0.0.1:7777` and play.

## Test each part of a match

| To try | Flags |
|---|---|
| A fresh start | none (the default) |
| A start from a ready server | `-warm`, then Enter or `-allocate-after 20s` |
| Match settings | `-external-id 88231 -label queue=ranked` |
| The start's env and args | `-env MATCH_MODE=ranked -arg -MaxPlayers=10` |
| A stop request | Ctrl+C |
| A time limit | `-ttl 10m` |
| How the stop arrives | `-stop sigterm` |
| The shutdown time | `-shutdown-time 30s` |
| A persistent server | `-kind persistent_server` |
| Ready check | `-ready log:PATTERN`, `-ready port:game` or `-ready delay:20s` |
| No local API calls | `-ready none` |

- A fresh start is allocated before the server starts. `-env` values are also in its environment, and `-arg` values on its command line.
- With `-warm` the server starts first. Once it calls `POST /v0/self/ready`, press Enter, or wait for `-allocate-after`.
- Ctrl+C, `-ttl` and the server's own `POST /v0/self/stop` send the shutdown notice and the `-stop` method. After `-shutdown-time` (30 seconds by default) the program is ended.
- `-stop` is `sigterm` (the default), `sigint`, `local_api` or `kill` on Linux and macOS, and `local_api` (the default), `ctrl_break` or `kill` on Windows.
- A server that has asked for the shutdown notice gets `sigterm`, `sigint` or `ctrl_break` only if it still runs halfway through `-shutdown-time`, as on our machines.
- On Windows the server shares the console with `survivalservers dev`, so Ctrl+C also reaches the server directly. With `-stop ctrl_break` it runs in its own process group, as on our machines, and gets Ctrl+Break from `survivalservers dev` instead.
- `-ready` tries your version's ready check: `log:PATTERN` waits for a console line that matches the regular expression, `port:game` for the named port to be open, and `delay:20s` for that long. A udp port can be checked on Linux only.
- `-ready none` counts the server as ready at start, for a server that never calls the local API.
- A server that exits with a code other than 0 before any stop request gets the line: a hosting machine would report this run as crashed.

## In your build pipeline

`survivalservers dev` exits when your server does, with your server's exit code. A test step can start the server, play a scripted match against it and check how it ended:

```bash
survivalservers dev -ttl 5m -- ./MyServer -log &
./my-bot-client 127.0.0.1 7777        # your own test client
wait $!
```

## Upload the build

[Upload the build](https://studio.survivalservers.com/docs/guides/upload-builds/), create a version with the same ports, ready check and stop method, and start a game server. The [quick start](https://studio.survivalservers.com/docs/get-started/quick-start/) does those steps with the samples' script.
