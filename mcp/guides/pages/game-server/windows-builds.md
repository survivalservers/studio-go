# Windows builds

> Upload your Windows server zip as it is: what the machine gives it, how it is limited and stopped, and where its logs and crash dumps go.

A Windows dedicated server runs here as you build it, with no container, no SDK and no changes to your game. Zip the game server folder, upload it, and name the program to start.

## The zip

Zip your game server folder as it is. When the upload is processed, the build lists every program in it (`executables`). The version's `executable` is one of those paths:

```json
"executable": "WindowsServer/BreakwireServer/Binaries/Win64/BreakwireServer-Win64-Shipping.exe"
```

- A zip can be up to your studio's limit, 30 GB by default. A larger one is refused with `413 too_large`; the limit is raised [on request](https://studio.survivalservers.com/docs/help/get-help/).
- Starts never download anything. A build is copied ahead of time to the machines in every region its versions target.
- A patched build fetches only the chunks that changed since your earlier builds. Your chunks are your studio's own, on our store and on machines.
- Each file keeps the date it has in the zip, as an unzip would.
- Uploading is on [upload builds](https://studio.survivalservers.com/docs/guides/upload-builds/).

## How it runs

- **Windows Server 2022** on every Windows machine. Build the server as you would for any dedicated server.
- **A Windows user per studio.** Your game servers run as a local user that belongs to your studio alone, never as an administrator.
- **Hard limits.** Each game server runs in a Windows Job Object that caps its vCPUs and committed memory at the version's size. `GS_CPU` and `GS_MEMORY_MB` give the size.
- **Its own copy of the folder.** Each game server writes in a fresh copy of the build. Your game servers on one machine all run as your studio's Windows user, so one can open another's folder there.
- **Files lists.** The [Files](https://studio.survivalservers.com/docs/guides/files/) list starts at that folder and leaves out the machine agent's own `.gs` folder.
- **Its own temp folder.** `TEMP` and `TMP` point at a folder of the game server's own.
- **Your game's user profile is shared.** Files a game writes under its Windows user profile (AppData, LocalLow, Documents) sit in one folder for all your game servers on that machine. Files there outside a persistent server's save path may be cleared once none of your game servers runs on that machine, so do not rely on them from one match to the next. Keep saves in the game server's folder with your server's own option, such as V Rising's `-persistentDataPath` or Valheim's `-savedir`. If your game can only save in the profile (Unity's default), name that folder as a persistent server's save path, starting with `%USERPROFILE%`, for example `%USERPROFILE%/AppData/LocalLow/<Company>/<Product>`: it is then backed up, restored and moved with the server. Two running persistent servers of your studio cannot share one profile folder: the second is refused until the first stops.
- **Outbound limits.** A game server cannot reach private networks, cloud metadata addresses or our own services, or send mail on port 25.

## What works differently from Linux

The price is the same on both. A few things are not:

- **One Windows user for your studio, not a container per game server.** Your game servers on one machine can read and change each other's folders.
- **Committed memory is capped.** Linux caps the memory a game uses; Windows caps the memory it commits, which is often more. The same game may need a bigger size on Windows.
- **Running out of memory.** On Linux a game that passes its size is ended. On Windows the game is refused the memory, and a game that does not handle that can hang and still show as running.
- **No port mapping.** Your server takes its ports from `GS_PORT_<NAME>` or `{{port.<name>}}` templates, as in Ports below.

## Ports

Each start gets fresh public ports, opened to your studio's Windows user. Read them from `GS_PORT_<NAME>`, or pass them with `{{port.<name>}}` templates in `args` (see [environment variables and allocation](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/)). In the game port range, 20000 to 48999, a game server cannot bind a port it was not given. Outside that range it can, but players cannot reach that port.

Name every port your server opens: a query port, a client port, a Steam port, a web or RCON port. A port missing from `ports` is refused to the server when it falls in 20000 to 48999, and the server usually quits. A fixed port outside that range, such as 7777, binds, and the server looks healthy while no player can join it. A Source server opens a client port and a Steam port beside its game port; this one also turns SourceTV off:

```json
"args": ["-game", "hl2mp", "-console", "-port", "{{port.game}}", "-clientport", "{{port.client}}", "-sport", "{{port.steam}}", "-nohltv"],
"ports": [{ "name": "game", "protocol": "udp" }, { "name": "client", "protocol": "udp" }, { "name": "steam", "protocol": "udp" }]
```

## Ready check

```json
"ready_check": { "type": "log", "pattern": "Startup Completed", "source": "logs/VRisingServer.log" }
```

- `type` is `local_api` (a ready call), `log` (a line on the console, or in the file `source` names), `port` (a bound port), `delay` (with `after`) or `none`.
- A server whose port opens before its world loads needs a `log` check. A `port` check would call it ready too early, and a stop then saves nothing.
- In November 2026, V Rising opened its RCON port before its world loaded, and a stop at that point saved nothing. Its ready check waits for `Startup Completed`.
- No readiness within `boot_timeout` (5 minutes by default) fails the start.

## What is installed

- Microsoft's Visual C++ runtime, 64-bit and 32-bit, version 14.51.36247.0 or newer. Unreal 5.8 game servers need 14.50 or newer.
- No .NET runtime. Publish .NET game servers self-contained (`dotnet publish --self-contained`).
- No Node.js. Put `node.exe` in the zip beside your server's files; it runs with no extra flags.
- No XNA Framework 4.0 and no legacy DirectX runtime.
- Anything else your server needs, such as other runtime DLLs, goes in the zip beside your program.

## The server console

The server reads a hidden console of its own as its standard input, as when you start it by hand in a console window. Game servers that insist on a console, such as a Source server with `-console`, run as they are. Console stop and save commands type into this console. Its output still goes to the log.

Typed commands reach a game that reads whole lines from its console, as Minecraft and Source servers do. A game that reads single key presses does not see them. In November 2026, Unturned took no typed command and stopped cleanly on Ctrl+Break. Stop such a game with its stop signal or RCON.

For a server that must see its input end at once, set `"stdin": "none"` in the version's launch. Console commands then cannot reach it.

## How it stops

When a game server is stopped, by the API, its time limit or its own `POST /v0/self/stop`, the version's `save` command runs first, when it has one. Then the shutdown notice goes out on the [local API](https://studio.survivalservers.com/docs/game-server/local-api/), and the version's `stop.method` is used:

| `stop.method` | What the server gets |
|---|---|
| `ctrl_break` (default) | A Ctrl+Break console event |
| `ctrl_c` | A Ctrl+Break console event |
| `wm_close` | A close message to each of its windows |
| `command` | A console line, an RCON command or an HTTP call |
| `local_api` | The shutdown notice only |
| `kill` | Ended at once |

```json
"stop": { "method": "command", "command": { "type": "console", "line": "stop" }, "shutdown_time": "60s" },
"save": { "command": { "type": "console", "line": "save-all" }, "wait": "30s", "done_log": "Saved the game" }
```

- A server that has asked for the shutdown notice (`GET /v0/self/shutdown`) gets the notice first. Its stop signal follows only if it still runs halfway through the shutdown time.
- Windows cannot send Ctrl+C to a server in its own process group, so `ctrl_c` arrives as Ctrl+Break.
- A Java server only prints a thread dump on Ctrl+Break. Give it `command` with its own stop line, as above for Minecraft.
- `wm_close` on a server with no window ends it after the shutdown time.
- A `command` that cannot be delivered falls back to Ctrl+Break, and the timeline says `stop_command_failed`.
- After `stop.shutdown_time` (30 seconds by default), the whole process tree is ended. Its `exit.killed` is then true.
- A server that runs as a window program, with no console, cannot take a console line or Ctrl+Break. Give it `wm_close`, or an `http` command on its own web interface.
- A token for that web interface goes in a secret `env` value. `header_env` sends it as a header, so it never sits in the version:

```json
"env": { "WEB_API_SECRET": { "value": "...", "secret": true } },
"stop": { "method": "command", "shutdown_time": "120s", "command": { "type": "http", "port": "web", "path": "/api/command",
  "body": "{\"command\": \"shutdown\"}", "headers": { "X-SDTD-API-TOKENNAME": "admin" },
  "header_env": { "X-SDTD-API-SECRET": "WEB_API_SECRET" } } }
```

## Exit codes

- Exiting on its own with code 0 is recorded as `self`.
- Exiting on its own with any other code, or leaving a crash dump, is recorded as `crashed`.
- After a stop request, any exit is recorded with the stop's reason.

## Logs and crash dumps

- **The console** (stdout and stderr) is the log, kept for 90 days. Have the server log to its console where the engine can, as Unreal does with `-log`.
- Give a Unity server `-logFile` with a path in its folder, and list that file in `log_paths`.
- An Unreal Shipping server logs nothing unless its target sets `bUseLoggingInShipping`.
- **Log files** the server writes: list them in the version's `log_paths`, or write them to `GS_LOG_DIR`.
- **Crash dumps** are caught by Windows Error Reporting and kept with the game server, encrypted, for 72 hours unless the studio sets otherwise. `crash_dump` is `mini` (the default), `full` or `off`. The crash's stack trace is kept with the logs.
- **Other files to keep**, such as match results: write them to `GS_ARTIFACT_DIR`, or list them in `artifact_paths`.

Read the logs and collected files after the game server has exited, in the panel or with `GET /v0/game-servers/{id}/logs`, `/files` and `/artifacts`. A crash dump downloads only in the panel, for owners and admins with the crash-dump permission. See [logs, crash dumps and metrics](https://studio.survivalservers.com/docs/concepts/logs-and-crash-dumps/).
