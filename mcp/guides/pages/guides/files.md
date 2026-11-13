# Read and replace files

> List and download the files of a running game server or persistent server, and put a file into a stopped one, with no remote desktop or SSH.

You can read a running game server's folder or a persistent server's folder through the API, and write files into a stopped persistent server. Use this to fetch a log your game writes, pull a save for a bug report, or put back a config file or a save. You don't need remote desktop, SSH or anything installed.

## List a folder

```bash
curl "https://studio.survivalservers.com/v0/game-servers/gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2/files?path=Saved/Logs" -H "Authorization: Bearer $API_KEY"
```

```json
{
  "path": "Saved/Logs",
  "live": true,
  "data": [
    { "name": "BreakwireServer.log", "path": "Saved/Logs/BreakwireServer.log", "type": "file", "bytes": 48213, "modified_at": "2026-10-20T09:14:02Z" },
    { "name": "Crashes", "path": "Saved/Logs/Crashes", "type": "folder" }
  ],
  "next_cursor": null
}
```

- Leave `path` out for the top of the folder.
- Entries come in name order, files and folders only: links and anything else are left out.
- A page holds 50 entries by default; `limit` takes up to 500. Pass `next_cursor` back as `cursor` for the next page.
- For a persistent server, call `GET /v0/persistent-servers/{id}/files`.

## Which folder is read

- **A game server's folder** is the one it runs in: its own folder on a Windows machine, or its container's files from `/` on Linux. It can be read while the game server runs.
- After a game server ends, the same calls read the files its version kept (see [logs, crash dumps and metrics](https://studio.survivalservers.com/docs/concepts/logs-and-crash-dumps/)), and `live` is `false`.
- **A persistent server's folder** holds its saves and can be read whether the persistent server runs or not. On Windows it is the persistent server's folder on the machine.
- **Paths are as the game sees them**, in one form for listing, downloading and uploading. On Windows they start at the persistent server's own folder (`Saved/Logs`). In a Linux image they start at the container's `/`, with or without the leading `/` (`opt/game/Saved/Logs`).
- In a Linux image, a persistent server's folder holds its save paths. The top, and each folder above a save path, list the folders on the way down to it. A path outside every save path is not found.
- Lists leave out the machine agent's own folders, which cannot be read: `dev`, `proc`, `sys`, `run`, `gs` and `.gs-init` on Linux, and `.gs` on Windows.
- On Windows, a build's files keep their dates from your zip, so `modified_at` shows the zip's date until the game changes the file.
- Every path stays inside the folder. A path that leads out of it (`..`) is refused, and a link the game left there is never followed out.

## Download a file

```bash
curl -L -o BreakwireServer.log \
  "https://studio.survivalservers.com/v0/game-servers/gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2/files/download?path=Saved/Logs/BreakwireServer.log" \
  -H "Authorization: Bearer $API_KEY"
```

The machine copies the file to storage, and the call answers once the copy is there. It redirects to a download link that works for 15 minutes without your key. A request that accepts JSON, as generated clients do, gets `{"url": ..., "expires_at": ...}` instead. For a persistent server, call `GET /v0/persistent-servers/{id}/files/download`.

- **The file as it is now.** Every download makes a fresh copy, because a running game keeps changing its files.
- **One copy shared.** Two downloads of the same file at the same time share one copy.
- **A big file** can take longer than the call waits, which is 100 seconds. It then answers `503 unavailable` with `Retry-After: 10`. Ask again, and the call goes on waiting for the same copy.
- **Over 2 GiB** answers `413 too_large`.

## Write while it is stopped

The machine puts the file in place only once all of it has arrived and its SHA-256 matches. The game never sees half a file. A game server's folder is read-only: change what it starts with in a new version.

You can also do this before a new persistent server's first start, to give it its settings.

1. **Stop the persistent server**: `POST /v0/persistent-servers/{id}/stop`. Its backup at stop keeps what you are about to replace.

2. **Start the upload** with the file's path in the folder, its size and its SHA-256:

   ```bash
   curl -X POST "https://studio.survivalservers.com/v0/persistent-servers/ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P/files/uploads" \
     -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
     -d '{"path": "/data/saves/world.sav", "bytes": 81920000, "sha256": "<sha256>"}'
   ```

   The reply has the upload's `id` and a `url` that works for an hour. A persistent server that is not `stopped` or `failed` answers `409 invalid_state`.

3. **Send the file** with `PUT` to that `url`, with its exact length.

4. **Finish** with `POST /v0/persistent-servers/{id}/files/uploads/{upload_id}/complete`. It answers `202` with `"status": "processing"` while the machine fetches and checks the file, then puts it in place.
   - Call it again to see `done`, or `failed` if the file did not match.
   - A file that has not all arrived answers `422`. An expired link answers `409 invalid_state`: start a new upload.
   - The file replaces any file at that path, and missing folders are made.
   - On Linux, give the path as the game sees it, inside one of the save paths: `/srv/game/server.properties` for the save path `/srv/game`. A path outside every save path answers `422`.

5. **Start the persistent server**: `POST /v0/persistent-servers/{id}/start`.

For backups and restores of the whole save, see [back up and restore a persistent server](https://studio.survivalservers.com/docs/guides/back-up-and-restore/).

## Limits

- Downloads up to 2 GiB per file; uploads up to 5 GiB.
- One copy at a time in each folder, and 4 at a time on a machine. Another answers `429 rate_limited` with `Retry-After: 5`.
- The machine needs free disk for twice the file plus 20 GiB. Otherwise finishing the upload answers `413 too_large`.
- Paths up to 1,024 characters.
- A persistent server that has no machine yet answers `409 invalid_state`.
- A game server's files need the scope `logs:read`. A persistent server's need `persistent_servers:read`, and an upload `persistent_servers:write`.

## In the panel

The **Files** card on a game server's or persistent server's page lists the folder and downloads a file. On a stopped persistent server it has **Upload a file**.

## Command line and AI tools

- `survivalservers files ID [PATH]` lists a game server's folder.
- `survivalservers files get ID PATH -o FILE` downloads one of its files.
- In an AI coding tool, the MCP server's `list_game_server_files` lists a folder and `read_game_server_file` reads a text file from it.
