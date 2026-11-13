# Switching from Gameye

> Point your backend's Gameye calls at our address: sessions, players, locations and logs keep working, and your image runs as it is.

Your backend can keep making its Gameye Session API calls here: change the base address and the token. Your image runs as it is and can still read its `GAMEYE_*` variables. As on Gameye, one call starts a session, and its reply holds the IP address and ports.

In October 2026, Gameye's documented requests started, read, listed and stopped game servers through this address. Player join and leave, the console log, scoped tokens and the error replies worked too.

## Change the address

Point your backend's Gameye base address at `https://studio.survivalservers.com/compat/gameye`, and send one of our API keys as the bearer token:

```bash
curl -X POST "https://studio.survivalservers.com/compat/gameye/session" \
  -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" \
  -d '{"id": "match-88231", "location": "europe", "image": "your-game", "version": "2.3.0",
       "env": {"MODE": "ranked"}, "args": ["-MaxPlayers=10"], "labels": {"queue": "ranked"}, "ttl": "2h"}'
```

The reply is Gameye's, with `201`:

```json
{ "id": "match-88231", "host": "203.0.113.24", "ports": [{ "type": "udp", "container": 7777, "host": 31042 }] }
```

Each call becomes a call on our own API with your key, so the key's scopes, app limits and rate limits apply.

## How the terms map

| Gameye | Survival Servers Studio |
|---|---|
| Image | App (its slug or ID) |
| Tag | Version name |
| Location | Region or continent |
| Session | Game server, started fresh |
| Session `id` | `external_id` and `Idempotency-Key` |
| Token | API key |

## Your image

1. Create a Linux app whose slug is your Gameye image name, and a build from the image you give Gameye, from any registry:

   ```bash
   curl -X POST "https://studio.survivalservers.com/v0/apps" \
     -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
     -d '{"name": "Your Game", "slug": "your-game", "os": "linux"}'
   curl -X POST "https://studio.survivalservers.com/v0/apps/your-game/builds" \
     -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
     -d '{"os": "linux", "source": {"type": "image", "image": "registry.example.com/you/your-server:2.3.0"}}'
   ```

   The build is ready to use once `GET /v0/builds/{id}` shows `status` `ready`.
   With the command-line tool, which waits for the build:

   ```bash
   survivalservers apps create your-game --os linux
   survivalservers builds image your-game registry.example.com/you/your-server:2.3.0 --wait
   ```

   A slug takes lowercase letters, digits and dashes. If your image name has other characters, send the app's ID as `image`.
2. Create a version named after the image tag, with each port your image listens on as `container_port`, and `env_aliases: ["gameye"]`. Send it to `POST /v0/apps/your-game/versions`:

   ```json
   {
     "name": "2.3.0",
     "build": "<build-id>",
     "size": { "cpu": 2, "memory_gb": 4 },
     "launch": {
       "ports": [{ "name": "game", "protocol": "udp", "container_port": 7777 }],
       "ready_check": { "type": "port", "port": "game" },
       "env_aliases": ["gameye"]
     }
   }
   ```

   The `port` check counts the game server ready once it listens on `game`. If your game server opens its port before its map has loaded, use a `log` ready check whose `pattern` matches its ready line.

3. Point the `live` branch at it:

   ```bash
   curl -X PUT "https://studio.survivalservers.com/v0/apps/your-game/branches/live" \
     -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" -d '{"version": "2.3.0"}'
   ```
   Or `survivalservers branches set your-game live 2.3.0`.

   A session that names no `version` starts from `live`.

With `env_aliases: ["gameye"]` each game server gets Gameye's variables. Our own `GS_*` variables are there as well.

| Variable | Value |
|---|---|
| `GAMEYE_IP`, `GAMEYE_HOST` | The public IP address |
| `GAMEYE_APPLICATION`, `GAMEYE_IMAGE` | The app's slug |
| `GAMEYE_IMAGE_VERSION` | The version's name |
| `GAMEYE_SESSION_ID`, `GAMEYE_CONTAINER` | The session's `id` |
| `GAMEYE_REGION` | The session's `location` |
| `GAMEYE_PORT_<PROTOCOL>_<PORT>` | The public port of a container port |

- `GAMEYE_PORT_UDP_7777=31042` is the public port mapped onto container port 7777.
- A game server your backend starts through our own API gets its own ID in `GAMEYE_SESSION_ID` and `GAMEYE_CONTAINER`, and its region in `GAMEYE_REGION`.

## Supported calls

| Gameye call | Here |
|---|---|
| `POST /session` | A fresh start near `location` |
| `GET /session` | Your sessions, with Gameye's filters |
| `GET /session/{id}` | One session |
| `DELETE /session/{id}` | A stop; `409` once it has ended |
| `PUT /session/player/join` | Adds players to a session |
| `DELETE /session/player/leave` | Removes players |
| `GET /region` | Our continents |
| `GET /available-location/{image}` | Regions ready for `live` |
| `GET /logs` | The console, `follow=true` |
| `GET /artifacts` | Collected files as `.tar.gz` |
| `POST /token` | A scoped API key |

- `POST /session` takes `id`, `location`, `image`, `version`, `env`, `args`, `labels` and `ttl`. An `external_id` you send becomes our `external_id` in place of `id`.
- `GET /session` lists running sessions; `all=true` adds ended ones. It filters with `filter[key]=value` and `filterNot[key]=value` on `location`, `image`, `tag`, `status`, `host` and labels.
- `GET /session` also takes `location`, `image`, `tag` and `host` as plain parameters, and `playerCount[lt]` and `playerCount[gt]`.
- A session's `status` is `created`, `running`, `shuttingdown`, `exited` or `dead`.
- `GET /available-location/{image}` lists each region where the app's `live` version is ready, with its ping beacon's address in `ips` when it has one. An app with no `live` branch answers `404`.
- `GET /logs?id=<session>` streams the console.
- `GET /artifacts?session=<session>&path=<folder or file>` packs the files collected at that path once the session has ended: the files the version names in `artifact_paths`, and what the game server left in `GS_ARTIFACT_DIR`. Crash dumps are never in it. The key needs `logs:read`, as our own artifacts call does.
- Downloads are proven here: a session's collected file came back in the archive, and a key without `logs:read` was refused.
- `POST /token` takes `name`, `scopes` and `expires_after` (`30m`, `2h`, `2h30m`), and maps Gameye's scopes to ours. The calling key needs `keys:write`.

Errors come back in Gameye's shape (`statusCode`, `code`, `message`, `identifier`). No capacity, an unreachable location, a tag not yet copied there and rate limits answer `420` with `CAPACITY_UNAVAILABLE`. Gameye's docs give `420` for no capacity, maintenance and too many starts. An image or tag you do not have answers `404` with `RESOURCE_NOT_FOUND`, as Gameye does. Your retry logic keeps working.

## Differences

- **Locations.** `location` is one of our regions or continents, or a Gameye or cloud region name such as `eu-west-1` or `us-east-1`. A Gameye or cloud region name goes to up to four of our regions within about 2,200 km of it. An unknown location answers `404`.
- **Session IDs.** An `id` already used answers `409` and starts nothing, until we remove that session's record 90 days after it ends. Without an `id`, a UUID is made.
- **Player calls need `session`.** A join or leave without it answers `422`.
- **Lists show sessions started through this address.** Game servers your backend starts through our own API are not listed here.
- **Restarts.** `restart` in the session call is not used; the version's `restart` policy applies.

## What does not carry over

- **Applications and tags.** These calls answer `404`. Make apps, builds and versions with our API or the panel.
  The command-line tool `survivalservers` makes them too.
- **`GAMEYE_ORGANIZATION` and `GAMEYE_INGRESS_PORT_*`.** Not set. `GAMEYE_PORT_*` already holds the port players connect to.

## Migration steps

1. Create the app, a build from your image and a version named after its tag, and point `live` at it.
2. Make an API key for your backend with `game_servers:write`, `game_servers:read`, `logs:read` and `apps:read`, limited to the app. `apps:read` lets replies show container ports.
3. Change the backend's Gameye base address and token.
4. Play a match on a test session, then move your players region by region.

You can move to our own API call by call. A session is a game server: `POST /v0/game-servers` with your session ID as `external_id` and `Idempotency-Key`, and the same address is in the reply. Every call is in the [API reference](https://studio.survivalservers.com/docs/reference/game-servers-and-regions/).

If you move from another provider, your first month is free up to $1,000 of use, and we move the game for you.

## Sources

- Session calls, replies, statuses and filters: [Session Management](https://gameye.com/docs/api-v2/session-management/) and [GET /session](https://gameye.com/docs/api-v2/reference/get-sessions/), read October 8, 2026.
- Error codes and `420`: [Error Codes](https://gameye.com/docs/troubleshooting/error-codes/), read October 8, 2026.
- The `GAMEYE_*` variables: [Container environment variables](https://gameye.com/docs/guides/using-gameye/using-variables/), read October 8, 2026.
- Tokens and their scopes: [POST /token](https://gameye.com/docs/api-v2/reference/create-token/), read October 8, 2026.
