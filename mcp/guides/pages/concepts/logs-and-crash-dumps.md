# Logs, crash dumps and metrics

> What every game server leaves behind, how long each part is kept, and how to read it while it runs and after it stops.

Every game server leaves a record you can read while it runs and after it ends: the console output, log files, collected files, the timeline, metrics and crash dumps. Logs, files, the timeline and metrics are kept 90 days. Crash dumps are kept 72 hours unless you change it. Only frame times need code in your game.

## Logs

```bash
curl "https://studio.survivalservers.com/v0/game-servers/gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2/logs/stream?source=console&tail=200&follow=true" \
  -H "Authorization: Bearer $API_KEY"
```

| Call | Returns |
|---|---|
| `GET .../logs` | Each source with its size |
| `GET .../logs/stream` | Plain text; `follow=true` stays open |
| `GET .../logs/archive` | A zip of every source |

- `.../` stands for `/v0/game-servers/{id}/`.
- The console (stdout and stderr together) is always kept.
- Add file logs with the version's `launch.log_paths`: globs inside the game server's folder, where `**` matches any folders. Anything the game writes to `GS_LOG_DIR` is kept too.
- `source` is `console`, `stdout`, `stderr`, or a file's source name from `GET .../logs`.
- `tail` starts that many lines from the end, up to 100,000.
- The archive answers with a redirect to a link valid 15 minutes. A request that accepts JSON gets the link as JSON.
- A studio can hold 100 streams open at once at each API location. Event streams count toward the same 100.
- From a terminal, follow the console with `curl -N "https://studio.survivalservers.com/v0/game-servers/ID/logs/stream?follow=true" -H "Authorization: Bearer $API_KEY"`.
- Or with the command-line tool: `survivalservers logs ID --follow`.

### While it runs

- A running game server's logs go up in parts: after 16 MiB of new output, or after 15 minutes with anything new.
- `GET .../logs` marks those sources `live`. A file log is listed once its first part has gone up.
- A persistent server that runs for months has its logs readable all along.
- Each part is kept 90 days from when it arrived.
- One run keeps at most 1,000 files and 8 GiB, console logs first.

## Crash dumps

- A crash dump is caught with no code in your game: Windows Error Reporting on Windows, the kernel's core dump on Linux.
- Every crashed run keeps its own dump, also a run that restarts in place.
- `launch.crash_dump` is `mini` (the default) or `full` on Windows. Linux keeps the kernel's core dump for either.
- `off` keeps no dump.
- Dumps are kept up to 4 GiB. On Linux a larger core dump is cut off at 4 GiB, and its file name then ends in `.cut.core`. On Windows a larger dump is left out.

### The stack trace

- The crash's stack trace is kept with the logs, as the source `crash-trace-<run>`. Anyone who can read logs can read it.
- It names the signal or exception and the thread, then each frame as module and offset, for example `RustDedicated+0x1a2b3c`. Open the frames with your build's symbols.
- A frame marked `?` was found by scanning the stack, and may be a stale value rather than a caller.

### Who can reach a dump

A dump holds the game's whole memory, so it is handled more strictly than a log:

- It is encrypted on its machine, with a key of its own, before it leaves. It stays encrypted in transit and in storage.
- It is kept 72 hours by default. Owners and admins can set 1 hour to 14 days in the studio's settings (`crash_dump_retention_hours` on `PATCH /v0/studio`). Lowering it shortens the dumps already kept too: each goes at the time it was stored plus the new hours, never later than before. Raising it moves nothing back. A dump that arrives while a new setting is still reaching its region is kept the default 72 hours.
- It is deleted when its persistent server or the studio is deleted, within the hour. Deletion takes up to 1,000 dumps an hour, so a studio with more than that takes an hour more for each 1,000.
- Only owners and admins with the crash-dump permission can download one. An owner turns it on for another owner or an admin on the Members page; nobody can turn it on for themselves. Anyone with the right to manage members can turn it off. It goes off when the member's role drops below admin.
- No API key can list or download a dump, and `logs:read` never reaches one.
- In the panel, a game server's page shows a Crash dumps card to people with the permission. Its Download button makes a link and starts the download at once.
- A link works once, for 2 minutes, only in the browser of the person who made it, signed in with 2FA, and only from the address it was made from. From anywhere else, make a new link. A refused try uses the link up too, even for the browser that asked for it. It works only in the panel at its own address, never from a tool that calls the API from somewhere else.
- At download time the person, the permission, their 2FA and the studio are checked again. A suspended or closed studio makes no links.
- From a signed-in panel session, `GET .../crash-dumps` lists the dumps (`id`, `name`, `run`, `bytes`, `created_at`, `expires_at`), and `POST .../crash-dumps/{dump}/link` makes a link.
- Every link, every download and every refused try is written to the studio's audit log. A refused try records the person the link was made for, the dump, the address that asked for it, the address refused and the reason.
- Our staff cannot open a dump through any staff tool. The key that opens a dump and the storage that holds it are kept apart. If our staff ever open one for a studio, the studio's audit log says "Platform staff opened crash dump" and names it.

## Files kept at exit

```bash
curl "https://studio.survivalservers.com/v0/game-servers/gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2/artifacts" -H "Authorization: Bearer $API_KEY"
```

- Files matching the version's `launch.artifact_paths`, and anything written to `GS_ARTIFACT_DIR`, are collected when the game server exits: match results, saves, reports.
- A crash dump is never kept or served as a file, a game's own `.dmp` or core included: `artifact_paths` refuses a pattern for dumps (`*.dmp`, `*.mdmp`, `core`, `core.*`, `*.core`), the machine leaves out any dump that a broader pattern matches, and a file download of one is refused.
- Each entry has `kind` (`log` or `file`), `path`, `bytes`, `download_url` and `expires_at`. Crash dumps are not listed here: see [who can reach a dump](#who-can-reach-a-dump).
- A `download_url` works for 15 minutes. Call again for fresh ones.
- After the game server ends, `GET .../files` lists the collected files and `GET .../files/download?path=...` fetches one.
- While it runs, the same two calls read its folder live, files up to 2 GiB: see [read and replace files](https://studio.survivalservers.com/docs/guides/files/).
- A live `path` is the path the game sees. On Windows it starts at the game server's own folder. On Linux it starts at the container's `/`, and the leading `/` is optional.
- The machine agent's own folders are left out of a live list: `.gs` on Windows, and `dev`, `proc`, `sys`, `run`, `gs` and `.gs-init` on Linux.

## Metrics

```bash
curl "https://studio.survivalservers.com/v0/game-servers/gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2/metrics?step=10s&from=2026-10-09T18:00:00Z" \
  -H "Authorization: Bearer $API_KEY"
```

```json
{
  "step": "10s",
  "series": {
    "cpu_percent": [["2026-10-09T18:30:00Z", 61.2], ["2026-10-09T18:30:10Z", 63.8]],
    "memory_committed_mb": [["2026-10-09T18:30:00Z", 3010]],
    "net_out_kbps": [["2026-10-09T18:30:00Z", 2410]],
    "tick_p99_ms": [["2026-10-09T18:30:00Z", 3.9]]
  },
  "tick_budget_ms": 15.6
}
```

| Series | Value |
|---|---|
| `cpu_percent` | Share of its own vCPUs |
| `memory_committed_mb` | Committed memory, MB |
| `net_in_kbps`, `net_out_kbps` | Network in and out, kbps |
| `tick_p99_ms` | Slowest 1% of frames, ms |

- The machine samples every game server once a second.
- `step=1s` reads 1-second points for the last 48 hours.
- `step=10s`, the default, reads 10-second averages for 90 days. The longer steps `1m`, `5m`, `15m`, `1h`, `6h` and `1d` reach back 90 days too.
- With no `from`, the answer covers the last hour.
- One answer holds at most 20,000 points per series: about 5.5 hours at `1s`, 2.3 days at `10s` and a week at `1m`. A longer span answers `422`.
- `tick_p99_ms` appears when the game sends its frame times once a second with `POST /v0/self/tick`.
- `tick_budget_ms` is one frame's time at the rate the game sends with them: 1000 divided by `hz`. It is 50 at 20 frames a second and 15.6 at 64, and one number, the latest in the period. Compare `tick_p99_ms` with it.
- In the panel, the Frame time tile shows the latest frame time against the budget, such as 30.0 ms "of 50 ms". It is green under 80% of the budget, amber from 80% and red over it.
- A game that sends no `hz` has no budget. `tick_budget_ms` is left out, and the panel shows frame times with no color.

## The timeline

```bash
curl "https://studio.survivalservers.com/v0/game-servers/gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2/events" -H "Authorization: Bearer $API_KEY"
```

The timeline lists what happened, in order of `seq`: `created`, `allocated`, `launched`, `ready`, `players_changed`, `stop_requested`, `exited`, `restarted` and `artifacts_collected`.

- `exited` says how a run ended: `data.code`, `data.reason` and `data.crashed`. A crash adds `data.signal` on Linux (such as `SIGSEGV`) or `data.exception` on Windows (such as `STATUS_ACCESS_VIOLATION`), `data.run`, and `data.crash_dump`: whether the machine found a dump.
- A run that crashes and starts again in place gets its own `exited` with `data.restarting` set to `true`, then `launched` and `restarted` for the next run. The game server stays `running` throughout.
- `game_server.restarted` names the crash in `data.crash`: the run, `exit_code`, `signal` or `exception`, `reason` and `crash_dump`.

- `save_failed`: the save command before a backup, a pause or a stop failed or ran out of time (`data.reason`).
- `stop_command_failed`: the stop command could not be delivered, so the default signal went instead.
- `memory_high`: it has used more than 90% of its memory size for over a minute (`data.memory_mb`, `data.size_mb`, `data.since`). Going over the size stops it.
- `disk_high`: its files have grown past its disk size, or it has written the most on a nearly full machine disk. Its data has `used_mb`, `size_mb`, `free_mb` and `since`.
- `profile_written`: Windows only, just before the run's `exited`. The run wrote files under your studio's Windows user profile, which all your game servers on that machine share, so those files are not in save paths or backups and do not move with a persistent server. `data.files` counts them and `data.folders` names up to five of their folders, such as `AppData\LocalLow\Acme\Game`. Keep saves in the game server's folder with your game's own option.
- `update_held`: this run started on the version the last run had, because the backup at that run's stop was not stored. The new version, `data.version`, waits for the next restart; `data.reason` says why.
- A persistent server's run adds `paused` (`data.then` is `pause` or `stop`), `resuming` and `resumed` (`data.from` is `paused` or `stopped`).
- `resume_failed`: a persistent server's run could not resume, so it stays paused as it was. `data.then` is the depth it stays at, and `data.reason` is `start_failed` (the game did not start again) or `refused` (its machine was not taking starts then).
- A resume that may not bring a player, such as a single packet, marks its `resuming` and `resumed` with `data.trial`. A `resumed` with `data.trial_passed` follows once a player connects, and billing starts then.
- A `resuming` with `data.refused` is such a resume refused for want of room on its machine.

## Find out why it failed

1. Read `status` and `exit`. `stopped` is a normal end; for `failed`, `exit.reason` says why:
   - `crashed`: read its stack trace, the log source `crash-trace-<run>`. An owner or admin with the crash-dump permission can download the dump.
   - `out_of_memory`: it went over its version's memory size. `GET /v0/apps/{app}/versions/{version}` has `usage.peak_memory_mb` and `usage.p95_memory_mb` to size the next version by.
   - `boot_timeout`: the ready check never passed. Check it matches what the game server does: the log line, the port or the call. A game whose port opens before its world loads needs a `log` check.
   - `health_timeout`: the game server stopped calling `POST /v0/self/health`.
   - `machine_failure` or `lost_contact`: the machine failed or lost contact with us, not your build. Start another.
2. Read the end of the console, then the timeline.
3. Open the dump while it is kept (72 hours unless the studio sets otherwise): Visual Studio or WinDbg with your symbols on Windows, `gdb` or `lldb` with your build's binary on Linux.

Steps 1 and 2 with curl:

```bash
curl "https://studio.survivalservers.com/v0/game-servers/ID" -H "Authorization: Bearer $API_KEY"
curl "https://studio.survivalservers.com/v0/game-servers/ID/logs/stream?tail=200" -H "Authorization: Bearer $API_KEY"
curl "https://studio.survivalservers.com/v0/game-servers/ID/events" -H "Authorization: Bearer $API_KEY"
```

`survivalservers game-servers get ID`, `survivalservers logs ID` and `survivalservers game-servers events ID` do steps 1 and 2. In an AI coding tool, the MCP server's `diagnose_game_server` returns all of it in one call.
