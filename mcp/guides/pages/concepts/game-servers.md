# Game servers

> The life of a game server for a match, from start to stopped: statuses, exit reasons, stops, time limits and crash restarts.

A game server is one running copy of a version, usually for one match. Your backend starts it, the reply includes its address, and it stops when the match ends.

```bash
curl -X POST "https://studio.survivalservers.com/v0/game-servers" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: match-88231" -H "Content-Type: application/json" \
  -d '{"app": "breakwire-windows", "branch": "live", "labels": {"match_id": "88231"}, "ttl": "2h"}'
```

```json
{
  "id": "gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2",
  "status": "running",
  "start_mode": "warm",
  "region": "europe-west",
  "address": {
    "ipv4": "203.0.113.24",
    "ipv6": null,
    "ports": { "game": { "port": 31042, "protocol": "udp" } }
  },
  "expires_at": "2026-10-09T20:22:04.512Z"
}
```

The start answers only after the machine has confirmed the game server, and the address in the reply is final. Each start gets fresh public ports. Game servers and persistent servers are reached over IPv4 only, so `ipv6` is always `null`. The full flow is on [start game servers from your backend](https://studio.survivalservers.com/docs/guides/start-matches-from-your-backend/).

- Send an `Idempotency-Key` on every start. A matchmaker's match ID makes a good one.
- A repeat with the same key and body returns the first reply with `Idempotent-Replayed: true` and starts nothing new.
- Keys are kept 24 hours. The same key with a different call or body answers `409 idempotency_key_reused`.

## Statuses

| Status | Meaning |
|---|---|
| `starting` | Launched and booting; address final |
| `running` | Ready check passed: joinable |
| `stopping` | Asked to stop; saving within its shutdown time |
| `stopped` | Ended normally |
| `failed` | Crashed, timed out, or machine lost |

Send players as soon as the reply arrives. A `starting` game server's address is already final, and players connect once it is up.

- The version's `launch.ready_check` decides when it turns `running`: `local_api`, `log`, `port`, `delay` or `none`.
- A game whose port opens before its world loads needs a `log` check on its loaded line. Examples are `Startup Completed` for V Rising and `Loading level: 100%` for Unturned on Linux.
- With a `port` check, such a game turns `running` too early, and a stop before its world has loaded saves nothing.
- A game server that is not ready within `boot_timeout`, 5 minutes by default, ends `failed`.

## Exit reasons

```json
"exit": { "reason": "crashed", "code": 3221225781, "crashed": true, "killed": false }
```

`exit` appears once the game server has ended, with `stopped_at`.

| `reason` | Status | Why |
|---|---|---|
| `api` | `stopped` | You stopped it |
| `bulk` | `stopped` | A stop of many |
| `self` | `stopped` | Exit code 0, or a self-stop |
| `ttl` | `stopped` | Its `ttl` ran out |
| `max_duration` | `stopped` | The version's limit ran out |
| `crashed` | `failed` | Non-zero exit or a crash dump |
| `out_of_memory` | `failed` | Used more than its memory size |
| `out_of_disk` | `stopped` | Its files went over its disk size |
| `boot_timeout` | `failed` | Not ready within `boot_timeout` |
| `health_timeout` | `failed` | No health call in `health_timeout` |
| `machine_failure` | `failed` | Its machine failed |
| `lost_contact` | `failed` | Its machine lost contact with us |
| `restart` | `stopped` | A persistent server's run ended for the next |

- `code` on Windows is the unsigned 32-bit code Windows reports: `3221225781` is `0xC0000135`, a missing DLL.
- `code` on Linux is the exit code, or 128 plus the signal number when a signal ended it.
- `killed` is true when we ended the program because it was still running when its stop's shutdown time ran out. That happens when the version's stop method or command did not end it, or the version stops with `kill`.
- A killed program's `code` is ours: 1 on Windows, 137 on Linux. The game had no chance to save on the way out, and `reason` still says why it was stopped.
- A non-zero exit during a stop you asked for is not a crash.
- `lost_contact` ends a persistent server's run on a machine cut off from us, so the server never runs twice.

## Stop a game server

```bash
curl -X POST "https://studio.survivalservers.com/v0/game-servers/gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2/stop" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d '{"shutdown_time": "30s", "reason": "match_over"}'
```

- `shutdown_time` defaults to the version's `launch.stop.shutdown_time`, 30 seconds unless the version sets one. After it, the whole process tree is killed.
- Once it has ended, its logs, collected files and crash dumps are kept, and `game_server.stopped` fires.
- `DELETE /v0/game-servers/{id}` does the same with the version's shutdown time. A game server that has ended answers `409 already_stopped`.
- `POST /v0/game-servers/stop` stops every running game server that matches all of `app`, `version`, `region` and `labels` you send. It answers with how many (`matched`).
- The game server can stop itself with `POST /v0/self/stop` on the [local API](https://studio.survivalservers.com/docs/game-server/local-api/).

### Shutdown notice and signal

A stop sends the version's save command first, if `launch.save` sets one. Then the game server gets the shutdown notice and the version's `launch.stop.method`.

- The shutdown notice is the answer to `GET /v0/self/shutdown` on the local API, with the deadline to exit by.
- A game that has asked for the notice gets the stop signal only if it still runs halfway through `shutdown_time`.
- A game that never asks for the notice, such as an Agones or PlayFab build, gets the notice and the signal together.
- The method is `ctrl_break` by default on Windows and `sigterm` on Linux. `local_api` sends the notice alone, and `kill` ends the game at once.
- `command` sends a console line, an RCON command or an HTTP call. If it cannot be delivered, the default signal goes instead and the timeline shows `stop_command_failed`.
- A typed console command reaches a game that reads whole lines. A game that reads single keys, such as Unturned, does not see it, so use its stop signal.
- On Linux, a launch script can exit before the game, as Unreal's does. Our init then waits up to 30 seconds for the game's other processes.
- In November 2026, an Unreal server on Linux stopped through the API exited with code 0 in 3.8 s. No signal was sent, and its log kept every line of the exit.

## Find a game server

- `labels` (up to 32) are filters: `GET /v0/game-servers?label=queue:ranked`. Repeat `label` to require several.
- `external_id` is your own ID for it, such as the match ID, and a filter too.
- Lists show `starting`, `running` and `stopping` by default. Add `status=stopped,failed` for ended ones.
- The game server reports its players with `PUT /v0/self/players`, or your backend does with `POST /v0/game-servers/{id}/players`.
- For a game that does neither, the machine counts its players through the version's [game profile](https://studio.survivalservers.com/docs/concepts/game-profiles/#players). `players.source` says where a count came from.
- With players reported, `players_below=10` finds game servers with room.

## Time limits

- `ttl` on the start stops it after that long, with reason `ttl`. The machine enforces it, so it holds even when the machine loses contact with us.
- In October 2026, on Windows and Linux, a game server cut off from us stopped within 0.2 s of its `ttl`.
- `max_duration` on the version is a hard limit on every match of that version. A longer `ttl` is cut to it.
- Neither is set by default, so a game server runs until it is stopped.
- `expires_at` says when it will stop.
- Not run on real machines yet: `max_duration`, `boot_timeout` and `health_timeout`.

## When it crashes

```json
"restart": { "policy": "on_failure", "max": 3 }
```

The version's `restart` sets what happens when a game server crashes: a non-zero exit code, or a crash dump.

- `never`, the default, leaves it `failed`.
- `on_failure` starts it again in place after a crash, with the same ID, address and ports, up to `max` times (1 to 100).
- Without `max`, it restarts after each crash until its third crash within 10 minutes, which ends it `failed`.
- Each restart fires `game_server.restarted` and adds one to `restarts`. Its `data.crash` says which run crashed, its exit code and signal or exception, and whether it left a crash dump. The timeline keeps that run's `exited`.
- A `boot_timeout` or `health_timeout` is not restarted.

## After it ends

Its record, timeline, logs and collected files stay readable for 90 days, its crash dumps 72 hours unless the studio sets otherwise. See [logs, crash dumps and metrics](https://studio.survivalservers.com/docs/concepts/logs-and-crash-dumps/).
