# Roll back a bad update

> Put many persistent servers back to before a moment: preview, try one first, then undo if you need to.

A rollback puts many persistent servers back to before a moment. Use it when an update or a game bug damaged saves. To go back on one persistent server, see [back up and restore a persistent server](https://studio.survivalservers.com/docs/guides/back-up-and-restore/).

## 1. Preview it

Send `POST /v0/bulk-actions/rollbacks` with a `selector` and `at`:

```bash
curl -X POST "https://studio.survivalservers.com/v0/bulk-actions/rollbacks" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d '{"selector": {"target": "persistent_servers", "app": "brinewatch-linux", "continents": ["europe"], "labels": {"mode": "pvp"}},
       "at": "2026-10-30T13:00:00Z"}'
```

From the command line, this prints the preview and asks before anything changes:

```bash
survivalservers rollback --app brinewatch-linux --continent europe --label mode=pvp --before "2026-10-30 14:00 Europe/Berlin"
```

- The answer lists each persistent server, the backup it would go back to and the play it would lose.
- Persistent servers it would leave alone are listed with the reason.
- Nothing changes yet.

In the panel, open **Game servers**, then **Bulk actions**, choose **New action**, then **Roll back**. From a version's page, **Roll back persistent servers on this version** fills the form in for you.

## 2. Choose the version

Each persistent server keeps the version it runs now. To also put back the version it ran then, send `"version": "then"`.
On the command line, use `--version then`.

A save from a newer version than the persistent server runs is skipped with `save_newer` unless you do. The preview lists that persistent server with the reason.

## 3. (Optional) Try one first

Send `"rollout": {"first": {"count": 1}}`. The rollback does one persistent server and pauses, so you can join it and look. Resume it to do the rest, in the panel or with the API:

```bash
curl -X POST "https://studio.survivalservers.com/v0/bulk-actions/bulk_.../resume" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)"
```

On the command line, `--try-one-first` does the same, and `survivalservers bulk-actions resume bulk_...` resumes it.

## 4. (Optional) Warn players

Add `warnings` and `warn_command`, as for [maintenance](https://studio.survivalservers.com/docs/guides/bulk-actions/).
On the command line, use `--warn 15m,5m,1m` with `--warn-command`.

## 5. Run it

Send the preview's `id` as `plan`, within 15 minutes. The rollback does exactly what the preview showed. If a persistent server or a backup changed since, the answer is `409 plan_changed`: preview again.

Each running persistent server is saved and stopped, and how it is now is kept as a `before_restore` backup. Then it is restored and started on the same address.

A persistent server that cannot be backed up first is not rolled back. It goes back to running on the saves it has, and the action says why.

## 6. Check the results

```bash
curl "https://studio.survivalservers.com/v0/bulk-actions/bulk_..." -H "Authorization: Bearer $API_KEY"
curl "https://studio.survivalservers.com/v0/bulk-actions/bulk_.../targets" -H "Authorization: Bearer $API_KEY"
```

`survivalservers bulk-actions watch bulk_...` follows it until it ends.

- Failures do not stop a rollback: a half-finished one is worse than a reported failure.
- Failed persistent servers are listed with the reason.
- `POST /v0/bulk-actions/{id}/retry` runs them again.

## Undo it

`POST /v0/bulk-actions/{id}/undo`, or **Undo rollback** on the action's page. Each persistent server goes back to how it was just before. Undo is a bulk action too, so you can undo it. A `before_restore` backup is kept for 30 days.

## After a rollout

**Roll back** on a finished rollout puts the old version back and keeps the saves. See [rollouts and rollbacks](https://studio.survivalservers.com/docs/guides/rollouts-and-rollbacks/).

**Roll back saves too** opens a rollback of the same persistent servers, each back to its own last save before that rollout reached it, on the version it ran. With the API, send the rollout's ID as `before_update` in place of `at`:

```json
{ "selector": { "target": "persistent_servers", "app": "brinewatch-linux" }, "before_update": "bulk_...", "version": "then" }
```

On the command line, use `--before-update bulk_...` in place of `--before`.

- Each persistent server goes back to its `before_update` backup, the one taken as it stopped for that update. The play from when the rollout began up to that persistent server's restart is kept.
- A persistent server the rollout's version never reached is left alone with `not_updated`. Its update is waiting for its next restart, or the rollout never got to it. Its saves are still the old version's.
- A persistent server whose `before_update` backup is gone goes back to its newest backup from before its update, and the preview says so.

See [the backup before an update](https://studio.survivalservers.com/docs/guides/back-up-and-restore/#the-backup-before-an-update).
