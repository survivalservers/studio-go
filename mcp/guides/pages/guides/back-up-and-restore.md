# Back up and restore

> Save folders, how long backups are kept, backups by hand, restores, undo and downloads.

A [persistent server](https://studio.survivalservers.com/docs/concepts/persistent-servers/) keeps its game's saves on its machine between runs. Backups keep copies off the machine, so a bad update or a griefed map costs you at most the time since the last backup. A failed machine costs less: the saves leave the machine a few seconds after your game writes them.

## Set the save folders

```json
"save_paths": ["BrinewatchServer/Saves"]
```

- Set `save_paths` on the persistent server to the folders that hold its saves. Backups copy exactly those folders.
- A persistent server needs at least one. Only these folders are kept between runs: with none, a Linux game would start empty at every restart, and a Windows one at its first update or move.
- To change them, stop the server and send `save_paths` with `PATCH /v0/persistent-servers/{id}`. Its newest backup is locked first and labelled "Before its save folders changed", so you can restore it.
- For a Windows build they are relative to the build's top folder. For a Linux image they are absolute paths in the container, such as `/data/saves`.
- No path may contain `..`.

## Make the game save first

```json
"save": { "command": { "type": "console", "line": "save-all" }, "wait": "30s", "done_log": "Saved the game" }
```

For a persistent server that does not save on its own, set `launch.save` on the version.

- The command is sent before every scheduled, by-hand and stop backup, and before the stop. It is a console line, an RCON command or an HTTP call.
- A Windows game that reads single key presses, not whole lines, does not see a console line. Use RCON or HTTP for it.
- `wait` is how long the save may take, from 1 second to 5 minutes (default 10 seconds).
- `done_log` is a console line that ends the wait early.
- A console save with no `done_log` waits all of `wait`. An RCON or HTTP save with no `done_log` ends when the game answers. If your game answers before its save is written, set `done_log`.
- A save that fails or runs out of time does not hold up the backup. The timeline shows `save_failed`.

## Set the schedule

```json
"backup": { "every": "10m", "keep_for": "30d" }
```

| Field | Default | Range |
|---|---|---|
| `every` | 10 minutes | 1 minute to 24 hours |
| `keep_for` | `30d` | `7d`, `30d` or `1y` |

- `every`: how often a backup is taken while the persistent server runs.
- `keep_for`: how long backups are kept. `7d` keeps any 10 minutes in the last 6 hours, any hour in the last 2 days and any day in the last week. `30d` keeps any 10 minutes in the last 6 hours, any hour in the last 2 days and any day in the last 30 days. `1y` keeps the same, then any week in the last 3 months and any month in the last year.
- The latest backup, one a restore or rollback is using, and a `before_restore` or `before_update` backup for 30 days, are always kept.
- Backups by hand and imported ones stay until you delete them with `DELETE /v0/backups/{id}`.
- To keep any other backup, lock it: `PATCH /v0/backups/{id}` with `{"locked": true}`. A locked backup must be unlocked before it can be deleted.
- A backup is also taken at every stop, with `kind` `stop`. The one before a new version starts is kept as `before_update`: see [the backup before an update](#the-backup-before-an-update).
- While the persistent server runs, its saves are also copied within seconds of each game save, with `kind` `continuous`. Only the changed parts are sent. The newest 3 are kept, apart from your other backups, and they fire no webhook. A persistent server whose machine fails starts again elsewhere from the newest backup of any kind, so save often in your game to lose less.
- Have your game write each save to a new file and rename it over the old one. A copy taken in the middle of a save then still holds the last finished one.
- Change the schedule with `PATCH /v0/persistent-servers/{id}` and a new `backup`.
- An app's `default_backups` (`PATCH /v0/apps/{app}`, the same `every` and `keep_for`) is what its new persistent servers take when their create sends no `backup`; each field a create sends still wins. `null` goes back to ours. Persistent servers made before keep their own.
- In the panel, a persistent server's **Backups** card has **Back up now** and **Upload a save**. Each backup has Restore, Download, Lock and Delete, and **Keep for** is in the persistent server's settings.

## Say when the game saved

```bash
curl -X POST "$GS_LOCAL_API_URL/v0/self/saved" -H "Authorization: Bearer $GS_TOKEN"
```

Saves are backed up without this call. It only means no wait for the save folder to go quiet.

- Your game makes this call with its own HTTP client, as it makes the other [local API](https://studio.survivalservers.com/docs/game-server/local-api/) calls.
- With our SDK, call `GS.Saved()`: `Saved` in the C++, C#, Go, Unity and Unreal SDKs, `saved` in Rust and Godot, `gs_saved` in C.
- Keep a save under a name with `{"keep": "Season 3 end"}`. It stays, locked, until you delete it.
- In the SDKs, that is `GS.Saved("Season 3 end")`.
- Call it at most once every 10 seconds, and with `keep` once a minute. Sooner answers `429` with the reason.
- A game can keep 20 backups of a persistent server. Past that, a save is an ordinary backup, and the panel says so.
- `persistent_server.keep_limit_reached` fires at most once a day. Unlock or delete a kept backup to free a place.
- A machine agent older than this call answers `404`. Treat that as a call that did nothing.
- The SDKs do that for you.

## Back up now

```bash
curl -X POST "https://studio.survivalservers.com/v0/persistent-servers/ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P/backups" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)"
```

- Take one before anything risky, such as a new version or a mod change.
- Send `{"label": "Before the patch", "locked": true}` to name it and keep it.
- It works while the persistent server runs. A stopped persistent server answers `409 invalid_state`: its backup at stop already holds its saves.
- The call answers `202` with the backup `in_progress`. `persistent_server.backup_created` fires when it is done.
- `GET /v0/persistent-servers/{id}/backups` lists them newest first, with `kind` (`scheduled`, `stop`, `manual`, `continuous`, `before_restore`, `before_update`, `imported` or `recovered`), `status`, `bytes`, `added_bytes`, `saved_at`, `version` and `expires_at`. A failed one has `failure`, and one that left files out has `left_out` (see [when a backup fails](#when-a-backup-fails)). One copied while the game may still have been writing a save has `mid_save: true`, and the panel shows it as **Taken mid-save**.

## When a backup fails

A backup that cannot be made is never skipped quietly. It is listed as `failed`, with the reason.

- `failure.reason` is `too_big`, `locked_file`, `read_error`, `disk_full` or `machine_error`, and `failure.detail` says it in plain words. A run of the same failure is one failed backup: `failure.times` counts the tries.
- While backups keep failing, the persistent server has `backups_failing`, with the reason and since when. The panel shows **Backups failing** beside its state. It clears once a backup is stored.
- `persistent_server.backup_failed` fires when backups start failing, then at most once a day while they still fail.
- The run's timeline shows `backup_failed` with `data.reason` and `data.detail`. A scheduled or continuous backup that fails again the same way adds a step at most every 10 minutes.
- If the backup at a stop fails, nothing waits for it. A move starts from the newest backup, and an update waits for the next restart (`update_held`).
- `too_big`: a backup holds at most 32 GiB and 200,000 files. Keep logs and caches out of the save folders.
- `disk_full` is ours to fix. Our team is alerted when a server's backups fail 3 times in a row, whatever the reason.

## Files the game holds locked

On Windows, a running game can hold a save file locked so nothing else can read it. Minecraft does this with `world/session.lock`.

- The file is tried again for about a second. If it is still locked, it is left out and the backup goes on.
- `left_out` names the files left out, and `left_out_count` counts them.
- A file named `*.lock` under 1 KB that the game holds is its own in-use marker, with no save data in it. The game makes it again when it starts, so leaving it out keeps the backup whole.
- Any other file left out makes the backup `partial: true`. The panel shows it as **Partial**, names what is missing, and says so again before you restore it. A restore's preview (`"dry_run": true`) says so in `caution`. Restoring it leaves those files out of the server.
- A backup at a stop is always whole: the game has let go of its files.
- If the game holds every save file locked, the backup fails with `locked_file`.

## Restore a backup

`POST /v0/persistent-servers/{id}/restore` with `{"backup": "bkp_..."}`, or `{"at": "2026-10-30T14:00:00Z"}` for its newest backup from before that moment. The answer is a bulk action you can watch.

In the panel, **Go back to a time** on the **Backups** card does the same by time: pick the moment, and it shows the backup that moment lands on before anything changes.

- **A running persistent server** is saved and stopped, and how it is now is kept as a `before_restore` backup. Then it is restored and started on the same address.
- **A stopped persistent server** stays stopped, and the backup replaces its saves at its next start. Send `"start": true` to start it after the restore.
- **A persistent server that failed** is restored and started.

If its current state cannot be backed up, it is not restored. It goes back to running on the saves it has, and the bulk action says why. `persistent_server.restored` fires when it is back, with `backup` and `saved_at`, so your own backend can go back to the same moment.

Undo with `POST /v0/bulk-actions/{id}/undo`. It puts the persistent server back as it was just before, and is itself a bulk action you can undo. A `before_restore` backup is kept for 30 days.

To also put back the version the persistent server ran then, send `"version": "then"`. A save from a newer version than the persistent server runs is skipped with `save_newer` unless you do. If the version it ran then has been deleted, `"then"` cannot put it back: the persistent server is skipped with `version_gone`, and a restore of that one persistent server answers 409 with the version's name. Restore it with `"version": "keep"`, or pick another backup.

## The backup before an update

When a persistent server restarts onto a new version, the old version's last save is kept. This holds for a rollout, a `version` or `branch` change applied now or at the next restart, and a scheduled restart that applies one.

- The game saves and stops as at any stop. The new version starts only once the backup taken at that stop is stored, which adds the time to store what the game changed in its last save.
- That backup is listed with `kind` `before_update` and the old `version`, and is kept for 30 days.
- To go back to it, restore it with `"version": "then"`. The old version comes back on the saves it left.
- To take every persistent server of a rollout back to its own, send the rollout's ID as `before_update` to a [rollback](https://studio.survivalservers.com/docs/guides/roll-back-after-a-bad-update/#after-a-rollout).
- It may not be stored: it failed, or it was not stored within 5 minutes. Then the persistent server starts again on its old version, and the new one waits for its next restart. The run's timeline shows `update_held` with `data.reason`, and a rollout counts that persistent server as failed with `not_saved`.

## Download a backup

```bash
curl -X POST "https://studio.survivalservers.com/v0/backups/bkp_.../export" -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)"
curl "https://studio.survivalservers.com/v0/backups/bkp_.../export" -H "Authorization: Bearer $API_KEY"
```

Or with the command-line tool: `survivalservers backups download bkp_... -o saves.zip`.

- `POST /v0/backups/{id}/export` builds a zip of the backup, with no change to the persistent server. Ask `GET` on the same path until `status` is `ready`.
- The answer has a `url` good for 15 minutes. Ask again for a fresh one. The zip is kept for 24 hours.
- `backup.export_ready` fires when it is built.
- The zip holds a `SHA256SUMS` file naming every file, so you can check what arrived (`sha256sum -c SHA256SUMS` in the unzipped folder).
- `survivalservers backups download` checks it for you.
- Keys need `backups:read`. Backups hold player data, so a key that reads persistent servers does not get them by default.

## Create from a backup

Send `from_backup` when you create a persistent server, to debug on a copy of a live persistent server or to move a save.

```json
{ "app": "brinewatch-linux", "name": "Copy for debugging", "region": "europe-west", "from_backup": "bkp_..." }
```

- The backup must be of the same app, and stored near the new persistent server's region.
- `save_paths` are the source persistent server's. Send them only if they are the same.
- The new persistent server stays stopped until its saves are in. Its first backup shows as `imported`, `in_progress`, then `ready`.
- If `start` is true or left out, it then starts.
- The two persistent servers share nothing afterwards. Deleting the source does not touch the copy.

## Price

Backup storage is $0.05 per GB a month, after the first 100 GB each month. Restores, rollbacks and downloads are free. Like your other [prices](https://studio.survivalservers.com/docs/account/pricing/), it is locked for 2 years.

- `GET /v0/prices` shows the rate as `backup_gb_month` and the free amount as `free_backup_gb_months`.
- A deleted persistent server's last backup is kept for 90 days and counts while it is. `GET /v0/backups/deleted` lists them, and deleting one stops its charge.

## Other guides

- To go back on many persistent servers at once, see [roll back a bad update](https://studio.survivalservers.com/docs/guides/roll-back-after-a-bad-update/).
- To use saves you have elsewhere, see [bring your saves](https://studio.survivalservers.com/docs/guides/bring-your-saves/).

## After a machine failure

A persistent server whose machine fails is restored from its latest backup on another machine of its system in its region. If that backup was copied while the game was still writing a save and the game does not get ready on it, the persistent server starts from the newest backup before it that holds a finished save, and `persistent_server.restored` fires with that backup and `saved_at`. If the old machine comes back with newer saves, they appear as a `recovered` backup, kept 30 days: restore it to get them. In a region with only one machine of its system, there is nowhere to move it: it waits for its machine to come back and starts again there, from the saves in its folder, with no restore. The move is described on [persistent servers](https://studio.survivalservers.com/docs/concepts/persistent-servers/#when-a-machine-fails).

## Keep your own records too

Write anything your players would never forgive losing, such as accounts, purchases and progression, to your own database as it happens. Treat a persistent server's saves as the world, not as your only record of your players.
