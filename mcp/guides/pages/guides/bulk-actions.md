# Bulk actions

> Send a command, change settings, restart or update hundreds of persistent servers in batches, with a dry run first and an undo after.

A bulk action applies one action to every persistent server or game server that a selector matches. It can send a game command, apply shared settings, restart, or move to a new version. It works in batches, watches each one come back, and stops on its own when too many fail. Every call that makes a bulk action accepts `"dry_run": true`, which returns the plan without doing anything.

## Choose the targets

Every bulk action takes the same `selector`. Each filter you give must match:

```json
{"target": "persistent_servers", "app": "brinewatch-linux", "continents": ["europe"], "labels": {"mode": "pvp"}}
```

`target` is `persistent_servers`, `game_servers` (session-based game servers, not persistent server runs) or `pools` (rollouts only). Filter by `app`, `versions`, `branch`, `regions`, `continents`, `labels`, `players`, `status`, or list `ids`. `"players": {"max": 0}` reaches only empty ones. A bulk action takes up to 2,000 targets.

The dry run lists every target with its region, version, players now and batch. It also lists every target it leaves alone, with the reason:

- `not_running`, `paused` or `already_current`.
- `busy`: another bulk action is changing it.
- `no_command`: its version has no such game command.
- `no_setting`: its version has no such setting.

## Send a command

A console line, a Source RCON command or an HTTP call, the same shapes a version uses for its stop and save commands:

```bash
curl -X POST "https://studio.survivalservers.com/v0/bulk-actions/commands" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d '{"selector": {"target": "persistent_servers", "app": "brinewatch-linux"},
       "command": {"type": "rcon", "port": "rcon", "password_env": "RCON_PASSWORD", "command": "Broadcast Double XP starts now"}}'
```

Or run a [game command](https://studio.survivalservers.com/docs/concepts/game-profiles/#game-commands) by its id. Each target's own version writes the command, and a target whose version has no such command is skipped with the reason `no_command`:

```bash
curl -X POST "https://studio.survivalservers.com/v0/bulk-actions/commands" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d '{"selector": {"target": "persistent_servers", "app": "brinewatch-linux"},
       "command_id": "announce", "args": {"text": "Double XP starts now"}}'
```

The answer is the bulk action (`202`). Each target keeps what the game answered: the RCON reply, or the HTTP status and body. For a console line it keeps the console written in the next `capture` seconds (2 by default, up to 30). Commands go 50 at a time and never stop on failures. For one persistent server, `POST /v0/persistent-servers/{id}/command` answers at once with the same result.

A console line reaches a Linux game under its init (the default) and a Windows game that reads whole lines from its console. A Windows game that reads single key presses does not see it: use RCON or HTTP.

## Shared settings

Shared settings hold environment variables, launch arguments, config files and setting `values` for one app. The API calls them a config set (`/v0/config-sets`). Versions never change: an edit is a new version. `values` set the settings in your [game profile](https://studio.survivalservers.com/docs/concepts/game-profiles/#settings) by ID, and change only those keys in the game's own files.

```bash
curl -X POST "https://studio.survivalservers.com/v0/config-sets" -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" -d '{
  "app": "brinewatch-linux", "name": "PvP rules",
  "env": {"XP_MULTIPLIER": "1.0", "MAX_PLAYERS": "60"},
  "args": ["-NoBattlEye"],
  "files": [{"path": "/data/saves/ServerSettings.ini",
             "content": "[ServerSettings]\nServerName={{server.name}}\nMode={{label.mode}}\n"}]}'
```

- The set's `env` replaces a persistent server's own value of the same name, so a change reaches every persistent server it is applied to. The dry run lists the names some persistent servers set themselves (`overridden`).
- The set's `args` go after the version's and the persistent server's own.
- Files are written into the persistent server's folder before every run, replacing what is there. Windows paths are relative to the build's top folder; Linux paths are absolute and must lie in one of its save paths. `{{server.name}}`, `{{server.id}}`, `{{region}}` and `{{label.<key>}}` are filled in for each persistent server.

Apply a version to many persistent servers with `POST /v0/config-sets/{id}/apply`. `apply` is `next_restart` (the default: nothing restarts) or `now` (rolling restarts). The dry run groups the persistent servers by the version they have now and shows the diff for each group. It shows values added, changed and removed (secrets as `***`), the arguments before and after, and a line diff of each file. `GET /v0/config-sets/{id}/diff?from=1&to=2` compares two versions.

`"remove": true` takes the set off the matching persistent servers. Its files stay in each persistent server's folder; new runs stop writing them. A set is deleted only when no persistent server uses it.

## Restart and roll out

`POST /v0/bulk-actions/restarts` restarts running persistent servers; `POST /v0/bulk-actions/rollouts` moves persistent servers (or pools) to a `version` or `branch`. Each restart saves (the version's save command), takes the backup at stop and starts on the same machine and address.

```json
{"selector": {"target": "persistent_servers", "app": "brinewatch-linux"}, "version": "1.8.2", "apply": "now",
 "rollout": {"batch_percent": 10, "per_machine": 3, "pause": "1m", "healthy_for": "30s", "max_failures": 1}}
```

| Field | Default |
|---|---|
| `batch_size` or `batch_percent` | 10% |
| `per_machine` | 3 |
| `pause` | 30s |
| `healthy_for` | 30s |
| `max_failures` | 0 |

- `batch_size` or `batch_percent`: how many targets go in each batch. With neither given, one target goes alone first and the rest follow 10% at a time, so a version or setting that fails stops after one server.
- `per_machine`: how many persistent servers restart at once on one machine.
- `pause`: the wait between batches.
- `healthy_for`: a game server counts as back once it is ready and has run this long.
- `max_failures`: the action stops once more targets than this have failed.

A target fails when its new run does not boot (`boot_failed`), crashes (`crashed`) or is not back in time (`not_back`). Past `max_failures` the action stops: its status is `stopped` and `stop_reason` says in a sentence which target failed and how many were left alone. `POST /v0/bulk-actions/{id}/resume` goes on (`"reset_failures": true` counts from zero again); `/retry` makes a new bulk action over the failed targets only.

Pools are judged by their starts, in batches like a restart. Each pool's waiting game servers of the old version stay until the new ones are ready, so a start never finds the pool empty. A pool succeeds once as many ready servers as it keeps (at least one) have passed their ready check on the new version and run `healthy_for`, or were taken by a start. Its first game server on the new version that ends on its own before a start takes it fails the pool, `boot_failed` (or `crashed` if it had passed its ready check): the pool goes back to its earlier version at once, its old ready servers kept, and `stop_reason` says what happened, such as "Europe West: a ready server on 1.0.2 exited with code 1 before its ready check, within a second of starting. The pool went back to 1.0.1, where its 4 ready servers stayed." While a pool is checked, its target's `phase` is `checking`.

In October 2026, restarts and rollouts ran in batches with pauses, and a rollout was undone, on Windows and Linux. In November 2026, a rollout to a pool on Windows was judged by its starts. It stopped at the first failed start, and the pool went back to its earlier version. Rollouts to persistent servers on Linux stopped at `max_failures`. Not run on real machines yet: resume, cancel, retry, and `per_machine` across several machines.

## Try it on a few first

Add `first` to `rollout`, and a change goes to those targets alone. Then it waits and pauses, with `stop_reason` saying so, and `POST /v0/bulk-actions/{id}/resume` takes it on to the rest:

```json
{"rollout": {"first": {"count": 2, "wait": "10m"}, "batch_percent": 10}}
```

If one of them fails, the action stops there as usual and the others are left alone.

## Undo

`POST /v0/bulk-actions/{id}/undo` on a rollout, a config change or a rollback makes a new bulk action that puts each target back where it was. That is its earlier version and branch, its earlier shared settings version (or none), or its saves from just before. It takes the same `apply` and `rollout` fields. A rollout that stopped on a persistent server whose new version crashed puts that one back too, and with `apply` `now` starts it again on its earlier version.

## Roll back saves

To put many persistent servers back to before a moment, see [roll back a bad update](https://studio.survivalservers.com/docs/guides/roll-back-after-a-bad-update/).

## Export backups

`POST /v0/bulk-actions/exports` with a `selector` builds one zip per persistent server, of its newest backup, or its newest from before `at`.

- Nothing about the persistent servers changes, and a failure does not stop the rest.
- Each target of the bulk action carries its `export`, with a `url` once its zip is ready. Zips are kept for 24 hours.
- It needs `backups:read`. A key without it sees the bulk action but no links.

## Schedule maintenance

At a time, warn players in game, then restart, roll out or apply shared settings:

```json
{"selector": {"target": "persistent_servers", "app": "brinewatch-linux", "continents": ["europe"]},
 "at": "2026-10-23T05:00:00Z", "ends_at": "2026-10-23T06:00:00Z",
 "warnings": [{"before": "15m", "message": "Server restarts in 15 minutes for an update"},
              {"before": "5m", "message": "Server restarts in 5 minutes"},
              {"before": "1m", "message": "Server restarts in 1 minute, log out now"}],
 "warn_command": {"type": "rcon", "port": "rcon", "password_env": "RCON_PASSWORD", "command": "Broadcast {{message}}"},
 "action": {"type": "rollout", "version": "1.8.2"}}
```

Send it to `POST /v0/bulk-actions/maintenance`. It covers the persistent servers the selector matches when its first warning goes out (with no warnings, at `at`), so one created after you send it is warned and changed too. Each warning is sent through `warn_command` with `{{message}}` replaced, at its time before `at`. At `at` the action runs on all of them at once unless `rollout` says otherwise. Persistent servers not started by `ends_at` are skipped (`window_closed`). The bulk action is the report: each persistent server's result, and `bulk_action.finished` when it ends.

## Save and schedule

A saved action keeps a bulk action's request under a name, to run again in one call or by itself on a schedule:

```bash
curl -X POST "https://studio.survivalservers.com/v0/saved-actions" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d '{"name": "Double XP weekend", "action": "config", "config_set": "cfg_...",
       "request": {"selector": {"target": "persistent_servers", "app": "brinewatch-linux"}, "apply": "now"},
       "schedule": {"cron": "0 18 * * 5", "time_zone": "America/New_York"}}'
```

`schedule.cron` has five fields: minute, hour, day of the month, month and day of the week. `4#1` in the last field is the first Thursday of the month, so `0 19 * * 4#1` runs at 19:00 on that day only. A plain `4` is every Thursday. On the days the clocks change, a time the clocks skip runs once when they jump (02:30 runs at 03:00), and a time that happens twice runs only the first time.

`action` is `command`, `restart`, `rollout` or `config`, and `request` is the body of that bulk action's own call. Each run works out its targets again, names what it skips and shows in the bulk actions list like any other. `POST /v0/saved-actions/{id}/run` runs it now; add `"dry_run": true` to see the plan first. `PATCH` with `"paused": true` holds its schedule.

A scheduled run that cannot start says why in `last_error`. One more than an hour late, after an outage on our side, does not run: the action says it missed its time. In the panel, **Save action** in a new bulk action keeps it, and the Bulk actions page lists it.

In November 2026, two saved actions ran on their schedules, each on three persistent servers: a warning, then a forced wipe one at a time.

## Watch a bulk action

- `GET /v0/bulk-actions/{id}`: status, counts, the running batch and when the next starts.
- `GET /v0/bulk-actions/{id}/targets`: each target's batch, result, answer or error.
- `GET /v0/bulk-actions/{id}/replies`: each different reply once, most common first, with how many targets gave it.
- The event stream carries `bulk_action.started`, `bulk_action.progress` (stream only), `bulk_action.paused`, `bulk_action.stopped` and `bulk_action.finished`. Webhooks get all but `bulk_action.progress`.
- `POST .../pause` lets the running batch finish and starts no new one; `POST .../cancel` cancels what has not started.

## Limits

- 2,000 targets per bulk action; 10 bulk actions scheduled or running per studio at once. Stopped and paused ones do not count, and resuming one needs room under the limit.
- The calls are in the `config_write` rate group. Targeting game servers also needs `game_servers:write`, and pools `apps:write`.
- A persistent server is changed by one restarting bulk action at a time; a second one skips it as `busy`.
