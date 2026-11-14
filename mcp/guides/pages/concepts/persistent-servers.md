# Persistent servers

> Game servers that keep their saves, address and identity across restarts, for persistent and survival games.

A persistent server is a named game server that stays up. It keeps its saves, address and ID across restarts, and it runs until you stop it. Use persistent servers for survival and community games, and session-based game servers for matches.

## Create a persistent server

```json
POST /v0/persistent-servers
{
  "app": "brinewatch-linux",
  "branch": "live",
  "name": "EU Official PvE 1",
  "region": "europe-west",
  "size": { "cpu": 4, "memory_gb": 16 },
  "save_paths": ["/data/saves"],
  "backup": { "every": "10m", "keep_for": "30d" },
  "restart": { "schedule": "0 6 * * *", "timezone": "Europe/Berlin" },
  "start": true
}
```

- The reply has the server's `id` (`ps_...`), its `address` and `"status": "starting"`. It turns `running` when its ready check passes.
- A game whose port opens before its save has loaded needs a `log` ready check on a line it prints once loaded. A `port` check calls it ready too early, and a stop then saves nothing.
- `region` is required, and the server stays in that region.
- `version` or `branch` picks the version. With neither, the server runs the `live` branch's version.
- `save_paths` are the folders backups copy, and the only ones kept between runs. At least one is needed. For a Windows build they are relative to the build's top folder. For a Linux image they are absolute paths in the container, such as `/data/saves`. A Windows game that saves under its user profile (AppData or LocalLow, where Unity saves by default) shares those saves with your other game servers on that machine, and they are in no backup and do not move with the server. Point its saves into the build's folder with the game's own option ([Windows builds](https://studio.survivalservers.com/docs/game-server/windows-builds/)).
- `"start": false` creates the server without starting it.

### Copies of one

`POST /v0/persistent-servers/{id}/copies` with `{"count": 3, "name": "EU PvE {n}", "regions": ["europe-central", "europe-west"]}` makes up to 20 new persistent servers with this one's settings: its app and branch (or version), size, settings that are not secret, launch arguments, labels, save folders, backups, restart schedule and pause when empty, in the regions you name, in turn.

- Saves are not copied: each copy starts empty. To start one from a save, create it with `from_backup`.
- Secret settings are not copied. The reply's `secrets_not_copied` names them, to set on each copy before it starts; copies stay stopped unless you send `"start": true`.
- `"dry_run": true` answers each copy's name and region and makes nothing. The studio's limit on persistent servers is checked for all the copies at once.

## Start, stop and restart

- `POST /v0/persistent-servers/{id}/stop` saves, takes a backup and ends billing. The server keeps its address.
- `POST /v0/persistent-servers/{id}/start` starts it again from its saves, on the same machine and address while that machine is healthy and has room for it. Without room there, it reads `starting` until its machine has room, or after a few tries moves to another machine in the region that has room, with a new address.
- `POST /v0/persistent-servers/{id}/restart` is a graceful stop, then a start.
- `DELETE /v0/persistent-servers/{id}` works only on a stopped or failed server. Its last backup is kept 90 days, and its storage bills until then. `GET /v0/backups/deleted` lists these backups.

### How a stop works

- A stop sends the version's save command first, if `launch.save` sets one. Then it sends the shutdown notice, then the version's stop method.
- A game that listens for the shutdown notice gets the stop method only if it still runs halfway through the shutdown time. See [stop a game server](https://studio.survivalservers.com/docs/concepts/game-servers/#stop-a-game-server).
- `shutdown_time` in the body of a stop or restart sets the shutdown time for that stop. Without it, the version's `stop.shutdown_time` applies: 30 seconds unless the version sets one.
- Once the game has exited, or has been ended at the end of the shutdown time, the server takes its last backup.

### Runs and crashes

- A persistent server's status is `starting`, `running`, `stopping`, `stopped`, `moving`, `failed`, `paused` or `resuming`.
- Each run is a game server record, so its logs, files and crash dumps use the game server calls. `GET /v0/persistent-servers/{id}/runs` lists them.
- A run that ended for the next one has the exit reason `restart`.
- A crashed run restarts in place after 1 second, then after 4 seconds. Three crashes, or three runs ending on their own, within 10 minutes leave the server `failed` until you start it.

## Changes and restarts

```json
PATCH /v0/persistent-servers/ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P
{ "version": "2.0.2", "apply": "next_restart" }
```

- `PATCH /v0/persistent-servers/{id}` changes `version` or `branch`, `size`, `env`, `args`, `labels`, `backup`, `restart` and `when_empty`. A stopped server also takes new `save_paths` and `save_files`; see [set the save folders](https://studio.survivalservers.com/docs/guides/back-up-and-restore/#set-the-save-folders).
- A new version is copied to the server's machine before it is applied.
- A running server takes it at its next restart (`"apply": "next_restart"`, the default). `"apply": "now"` restarts it gracefully now. A stopped server takes it at once.
- A new version starts only once the backup taken at the stop is stored, as a `before_update` backup kept for 30 days. If it cannot be stored, the server starts again on its old version and the new one waits for its next restart. See [the backup before an update](https://studio.survivalservers.com/docs/guides/back-up-and-restore/#the-backup-before-an-update).
- A server keeps its tier and system. A version of another tier or system answers `422`.
- A server on a `branch` follows it. When the branch gets a new version, the server takes it at its next restart, or at once when stopped. A server given a `version` stays on it.
- Changes to `size`, `env`, `args` and `when_empty` apply at the server's next run.
- `size` takes the same limits as at create. More vCPUs on a running server must fit your studio's limit on running vCPUs.
- In November 2026, a running 1 vCPU, 2 GB server changed with `"apply": "now"` came back as a new run at 2 vCPUs and 4 GB.
- [Shared settings](https://studio.survivalservers.com/docs/guides/bulk-actions/#shared-settings) (`POST /v0/config-sets/{id}/apply`) put the same settings on many persistent servers. Their env goes over each persistent server's own, and its files are written before every run. See [bulk actions](https://studio.survivalservers.com/docs/guides/bulk-actions/).

### Scheduled restarts

- `restart.schedule` is a five-field cron schedule in `restart.timezone` (default UTC), for the daily restart many games need. `D#N` in the day-of-week field is the Nth such weekday of the month: `0 6 * * 1#1` is the first Monday.
- On the days the clocks change, a restart at a time the clocks skip runs once when they jump (02:30 runs at 03:00), and one at a time that happens twice runs only the first time.
- A scheduled restart that cannot run within an hour of its time (we could not act on it then, or a new version was still copying to its machine) is skipped, and the next one runs as usual. A restart you ask for always runs.
- `"restart": null` removes it.
- A scheduled restart sends players no warning in the game first. To warn them, [schedule maintenance](https://studio.survivalservers.com/docs/guides/bulk-actions/#schedule-maintenance) instead.

## Pause when empty

```json
"when_empty": { "after": "10m", "then": "pause", "stop_after": "1h" }
```

With `when_empty` set, a server pauses when nobody has played for a while. A player's traffic resumes it: a paused server on the first packet, a stopped one when the same player sends again within 5 seconds.

| Field | Range | Default |
|---|---|---|
| `after` | 2 minutes to 24 hours | 10 minutes |
| `then` | `pause` or `stop` | `pause` |
| `stop_after` | 10 minutes to 7 days, or `0` | 1 hour |
| `enabled` | `true` or `false` | `true` |

- `after`: how long with no players before it pauses.
- `then`: `pause` suspends the process and keeps its memory. `stop` takes a backup and ends the process, so resuming is a fresh start.
- `stop_after`: a paused server stops after this long. `0` means never.
- `{"enabled": false}` turns it off. A change to `when_empty` applies at the server's next run.

### How a paused server resumes

- The machine holds the server's public ports, and the game listens on private ones.
- Packets that arrive while it resumes are held and handed over in order. TCP connections are held open.
- A paused server resumes on the first packet and answers it once it has resumed.
- A stopped server starts when the same player sends again within 5 seconds. That is a second packet from the same address, or data on a TCP connection. Game clients usually retry on their own, and the first packets are held, so the first attempt is answered. A single packet or a connect alone starts nothing.
- While `when_empty` is on, the game sees players' traffic arrive from the machine, not from each player's own address.
- A game that players join only through a relay service, such as Steam Datagram Relay, cannot be resumed by a packet.
- No player's traffic is turned away. A resume that brings no player pauses again after about 20 seconds, and after 10 when it keeps happening.
- Once a player has connected, the server keeps running at least 2 minutes.
- A game whose client waits for the server to speak first over TCP cannot start a stopped server. Its client sends nothing until the game answers. Use `pause` with `stop_after` set to `0` for such a game.

### How fast a resume is

In October 2026, we resumed each of these games 32 to 36 times, about 22 seconds into a pause. The table gives the time from a player's first packet arriving to the game's first answer. Add the player's own network time. The first answer is the first login step, not a full join.

| Game | System | Median | Range |
|---|---|---|---|
| Valheim | Linux | 22 ms | 9 to 41 ms |
| Valheim | Windows | 20 ms | 16 to 38 ms |
| Terraria | Linux | 28 ms | 17 to 44 ms |
| Minecraft, 1 GB | Linux | 24 ms | 10 to 40 ms |
| Minecraft, 3 GB | Linux | 19 ms | 10 to 51 ms |
| Minecraft, 1 GB | Windows | 25 ms | 21 to 42 ms |
| Minecraft, 3 GB | Windows | 26 ms | 20 to 50 ms |

About 10 minutes into a pause, the same answer took 16.5 to 21 ms on Valheim and 22 ms on Terraria on Linux. Minecraft took 20 to 33 ms on Windows, and 17 ms on Linux with the setting below. A stopped server's resume is a fresh start, so it takes the game's own start time.

### While it is paused

- Scheduled restarts are skipped. A new version or a restart you ask for applies at once, since nobody is on it.
- A paused server keeps its machine, address and ports.
- Before each pause the server saves, with the version's `save` command if it has one, and takes a backup if the saves changed.
- A game that exits by itself within 30 seconds of resuming restarts in place from its saves. That version then stops when empty instead of pausing, whatever `then` says, until a new version starts clean. `stops_instead` on the persistent server says so, and the panel shows a line on its page.
- A game with its own watchdog can do this. Minecraft on Linux does after about a minute paused: set `max-tick-time=-1` in `server.properties`.
- A stopped server gives back its vCPUs and memory. If they are taken when a player resumes it, it moves to another machine in the region from its backup, with a new address. When no other machine of its system in the region has room, as can happen today on Windows, there is nowhere to move it: it waits until its own machine has room again and starts there, at its address, from the saves in its folder. Only a player who sent again can move it: other traffic leaves it stopped where it is.
- The run's timeline shows `paused`, `resuming` and `resumed`. A resume that may not bring a player marks its `resuming` and `resumed` with `data.trial`, and a `resumed` with `data.trial_passed` follows if a player comes. The event stream shows `persistent_server.paused`, then `persistent_server.resumed` once a player has resumed it. A resume that brings no player adds nothing to it.
- After a pause, billing starts when a player connects. A resume that brings no player bills as the pause it interrupted.
- One exception: a stopped server on a full machine that moves because a sender tried again. The moved server bills as running from the move until it pauses, even if no player comes: 10 to 12 minutes with the default settings.

## Backups

- Backups copy the server's `save_paths` every `backup.every` while it runs, at every stop, and when you ask with `POST /v0/persistent-servers/{id}/backups`.
- `save_files` names single files kept between runs and copied into every backup beside the save folders, such as `/data/server.properties` next to `/data/world`, for Linux images for now. Each is one entry at its own path in a backup, and a restore writes it only when the backup has it. On its first run a file is the image's own copy, or empty when the image has none. A game that saves such a file by renaming a new file over it cannot do that here: keep that file in a save folder.
- `backup.every` is 1 minute to 24 hours. The default is 10 minutes.
- `keep_for` is `7d`, `30d` (the default) or `1y`.
- `30d` keeps one backup per 10 minutes for 6 hours, one per hour for 2 days and one per day for 30 days.
- `7d` keeps the same with daily backups for a week only. `1y` adds one a week for 3 months and one a month for a year.
- Backups you take by hand, upload or lock stay until you delete them.
- Restoring a backup is on [back up and restore a persistent server](https://studio.survivalservers.com/docs/guides/back-up-and-restore/). Uploading your own saves is on [bring your saves from another provider](https://studio.survivalservers.com/docs/guides/bring-your-saves/).

## Files

- `GET /v0/persistent-servers/{id}/files` lists the server's folder: live while it runs, and from its folder on the machine while it is stopped.
- Paths are the ones the game sees. On Windows they start at the server's own folder. On Linux they start at the container's `/`, such as `/data/saves`.
- The list leaves out the machine agent's own folders.
- You can upload a file only while the server is stopped. See [read and replace files](https://studio.survivalservers.com/docs/guides/files/).

## When a machine fails

- When a server's machine stops answering, the server starts again from its newest backup on another machine in the same region.
- In a region with only one machine of the server's system, there is nowhere to move it: it waits for its machine to come back and starts again there, from the saves in its folder.
- The move waits until the copy on the old machine has surely stopped, so the server never runs twice. A longer shutdown time makes the wait longer.
- In October 2026, players were back 102 seconds after their machine was lost. That was a sample game that starts in about a second. Add your game's own start time: about 30 seconds for the Valheim dedicated server.
- A big save adds the time to fetch it. In November 2026 a server with a 2 GiB save was running again 93 seconds after its machine was cut off; fetching and unpacking the save took 13 of those seconds.
- Its status passes through `moving`, its address changes, and `persistent_server.moved` fires with the new address.
- Saves written after that backup are not in the new copy. If the old machine comes back with newer saves, they appear as a `recovered` backup, kept 30 days.
- A server whose machine loses contact with the platform stops itself with a backup, ahead of any move. In October 2026, a 60-second outage on our side stopped every persistent server 44 seconds in, and link drops of 20 seconds stopped nothing.
- When we take a machine out of service, its persistent servers move the same way, from the backup taken as each one stops.
- A persistent server cannot move to another region.

## Billing

- A persistent server bills per second while it runs, at $0.025 per vCPU-hour.
- Billing for its vCPUs ends when it stops.
- The first hour of each pause is free (`paused_free_minutes` in `GET /v0/prices`). A longer pause bills at `paused_cpu_hour`, the reserved rate.
- A server that stopped when empty bills no vCPU time.
- Backup storage is free up to 100 GB a month, then $0.05 per GB a month (`backup_gb_month`). Restores are free.
