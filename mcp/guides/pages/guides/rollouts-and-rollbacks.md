# Roll out and roll back

> Ship a new version through a public test branch to live, and roll back with one call that puts the old version back.

Your backend starts game servers from a branch, such as `live`. A release puts a new version on a branch; a rollback puts the old version back. Game servers already running keep the version they started with, so no match is cut short.

## 1. Make the version

`version.json` holds the version's settings, with `"name": "2.3.1"` and `"build": "bld_a01JA80M1N3B5V7C9X1Z3L5K7J"`:

```bash
curl -X POST "https://studio.survivalservers.com/v0/apps/breakwire-windows/versions" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d @version.json
curl "https://studio.survivalservers.com/v0/apps/breakwire-windows/versions/2.3.1" -H "Authorization: Bearer $API_KEY"
```

```json
"regions": [
  { "region": "europe-west", "status": "ready", "ready_at": "2026-10-09T17:31:12.004Z" },
  { "region": "europe-central", "status": "copying" }
]
```

- The version is copied ahead of time to each of its regions. Each region reports `pending`, `copying`, `ready` or `failed`, or `removed` once the machines there no longer keep it.
- `version.region_ready` fires once for each region, the first time that region is ready.
- Read the version again to see every region before you update `live`.
- With the command-line tool, `survivalservers versions create breakwire-windows version.json --build bld_a01JA80M1N3B5V7C9X1Z3L5K7J --name 2.3.1 --wait` makes it and returns once one region has it ready, or once it failed in every region. `survivalservers versions get breakwire-windows 2.3.1` shows every region.
- A start pinned to 2.3.1 that finds no ready region it may use answers `409 version_not_ready`.

## 2. Put it on public-test

```bash
curl -X PUT "https://studio.survivalservers.com/v0/apps/breakwire-windows/branches/public-test" \
  -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" -d '{"version": "2.3.1"}'
```

Or `survivalservers branches set breakwire-windows public-test 2.3.1`.

Your backend picks the branch per match: send `"branch": "public-test"` on, say, one start in twenty. Then watch how those game servers end:

```bash
curl "https://studio.survivalservers.com/v0/game-servers?app=breakwire-windows&version=2.3.1&status=stopped,failed" -H "Authorization: Bearer $API_KEY"
```

Or `survivalservers game-servers list --app breakwire-windows --version 2.3.1 --status stopped,failed`.

Failed ones keep their logs and crash dumps. See [logs, crash dumps and metrics](https://studio.survivalservers.com/docs/concepts/logs-and-crash-dumps/).

## 3. Put it on live

```bash
curl -X PUT "https://studio.survivalservers.com/v0/apps/breakwire-windows/branches/live" \
  -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" -d '{"version": "2.3.1"}'
```

New starts on `live` run 2.3.1 from that moment. In CI, the same calls follow the upload: see [from your CI](https://studio.survivalservers.com/docs/guides/upload-builds/#from-your-ci).

With the command-line tool, one CI step uploads the build, makes the version, waits for each and puts the version on the branch once a region has it:

```bash
survivalservers deploy breakwire-windows --zip build/BreakwireServer-Win64.zip --settings version.json --name 2.3.1 --branch live
```

## Pools follow the branch

- A pool set on a branch switches when the branch gets a new version: new game servers start once the version is copied to its region.
- Its ready game servers of the old version stay until as many new ones are ready, so starts never find the pool empty.
- A pool set on a fixed `version` stays on it until you change the pool.
- If the new version's game servers keep failing to start, the pool slows down and then stops trying that version, keeping the old ready ones: see [when new ready servers fail](https://studio.survivalservers.com/docs/concepts/ready-servers/#when-new-ready-servers-fail).
- To try a new version on pools with a check, roll it out to them as a bulk action instead: it stops at the first failed start and puts that pool back.

## Persistent servers move too

A persistent server that follows a branch takes the branch's new version without a call of its own:

- A running server takes it at its next restart. Nobody is thrown out.
- A stopped server takes it at once, so its next start runs it.
- The old version's last save is backed up before the new version starts, as a `before_update` backup kept for 30 days. See [the backup before an update](https://studio.survivalservers.com/docs/guides/back-up-and-restore/#the-backup-before-an-update).
- To restart them now, a few at a time, stopping on failures, use a [rollout](https://studio.survivalservers.com/docs/guides/bulk-actions/).
- A persistent server made with a fixed `version` stays on it until you change the persistent server.

## Roll back

```bash
curl -X PUT "https://studio.survivalservers.com/v0/apps/breakwire-windows/branches/live" \
  -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" -d '{"version": "2.3.0"}'
```

Or `survivalservers branches set breakwire-windows live 2.3.0`.

- New starts run 2.3.0 again at once while machines still have a copy.
- Machines keep each app's three newest versions that are not deprecated. They also keep any version a branch, a pinned pool, a game server or a persistent server uses.
- An older version has been removed from the machines. Pointing a branch at it copies it again, so move `public-test` to it first.
- Then read `GET /v0/apps/breakwire-windows/versions/2.2.0` until a region is `ready`, and update `live`. See [what stays on the machines](https://studio.survivalservers.com/docs/concepts/apps-builds-versions/#what-stays-on-the-machines).
- `survivalservers versions get breakwire-windows 2.2.0 --wait` does that wait for you.
- A branch cannot point at a deprecated version (`409 invalid_state`). Send `{"deprecated": false}` first.

If the update damaged saves, go back on those too: [roll back a bad update](https://studio.survivalservers.com/docs/guides/roll-back-after-a-bad-update/).

## Clean up

- `PATCH /v0/apps/{app}/versions/{version}` deprecates a version.
- `DELETE /v0/apps/{app}/versions/{version}` deletes a version.
- `DELETE /v0/apps/{app}/branches/{branch}` deletes a branch.

What to know:

- Deprecate with `{"deprecated": true}`: new starts answer `409 invalid_state`, and running game servers keep it. `{"deprecated": false}` undoes it.
- Deleting a version answers `409 in_use` while a game server runs it, a branch points at it, or a persistent server uses it.
- Deleting a branch keeps its version. It answers `409 in_use`, naming them, while a pool or a persistent server follows the branch.
- An app keeps up to 200 versions. Past that, a new one answers `403 limit_reached`: delete old ones to make room.
