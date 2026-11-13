# Bring your saves

> Upload a zip of saves as a backup of a persistent server, then restore it.

Upload a zip of saves and it becomes a backup of a persistent server. Use it to move a game from another host. The persistent server stays as it is until you restore that backup.

Run here with Windows and Linux persistent servers (November 2026).

## 1. Make the zip

Put each file in the zip at its path under one of the persistent server's `save_paths`. For `save_paths` of `BrinewatchServer/Saves`, a file in the zip is `BrinewatchServer/Saves/world.sav`.

- With a single save path, any other path in the zip is taken as inside it: `world.sav` becomes `BrinewatchServer/Saves/world.sav`.
- A Linux save path such as `/data/saves` goes in the zip without its leading `/`: `data/saves/world.sav`.
- With several save paths, a file outside all of them refuses the whole zip, with the reason. So does a link, an unsafe path or the same file twice.
- A zip can be up to 5 GiB to upload, and hold up to 32 GiB of files and 200,000 files.

## 2. Upload it

In the panel, use **Upload a save** on the persistent server's Backups card. Through the API:

1. `POST /v0/persistent-servers/{id}/backups/uploads` with the zip's size, `{"bytes": 81920000}`, and an optional `label`. The answer has an `id` and a `url` that works for 6 hours.
2. `PUT` the zip to that `url`.
3. `POST /v0/persistent-servers/{id}/backups/uploads/{upload_id}/complete`.
4. `GET /v0/persistent-servers/{id}/backups/uploads/{upload_id}` until `status` is `done`. Its `backup` is the new backup, with `kind` `imported`.

With curl, `UPLOAD_URL` and `UPLOAD_ID` are the first answer's `url` and `id`:

```bash
curl -X POST "https://studio.survivalservers.com/v0/persistent-servers/ps_.../backups/uploads" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d "{\"bytes\": $(wc -c < saves.zip | tr -d ' '), \"label\": \"From the old host\"}"
curl -T saves.zip "UPLOAD_URL"
curl -X POST "https://studio.survivalservers.com/v0/persistent-servers/ps_.../backups/uploads/UPLOAD_ID/complete" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)"
curl "https://studio.survivalservers.com/v0/persistent-servers/ps_.../backups/uploads/UPLOAD_ID" -H "Authorization: Bearer $API_KEY"
```

Or with the command-line tool, which waits until the save is stored and prints the backup's ID:

```bash
survivalservers backups upload ps_... saves.zip --label "From the old host"
```

Keys need `backups:write`.

## 3. Restore it

```bash
curl -X POST "https://studio.survivalservers.com/v0/persistent-servers/ps_.../restore" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d '{"backup": "bkp_..."}'
```

Or `survivalservers restore ps_... --backup bkp_...`.

This is the normal restore. How the persistent server is now is kept first, so you can undo. See [back up and restore a persistent server](https://studio.survivalservers.com/docs/guides/back-up-and-restore/).

An imported backup stays until you delete it.

## One file at a time

To replace a single file instead, see [read and replace files](https://studio.survivalservers.com/docs/guides/files/). The persistent server has to be stopped.
