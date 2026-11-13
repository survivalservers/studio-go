# Switching from PlayFab

> Run a server build that uses PlayFab's GSDK: the machine answers as the PlayFab agent the GSDK talks to.

Set one launch option on the version, and the machine answers as the PlayFab agent that PlayFab's game server SDK (GSDK) talks to. Your game keeps calling `ReadyForPlayers()`, keeps its health and player callbacks, and gets its session's settings where it always did. In October 2026, PlayFab's own C++ runner game, built from the GSDK's source, ran here as it is in a Linux image. In November 2026, PlayFab's C++ GSDK and its C# GSDK (NuGet 0.11.210519) made every call here: ready, health, players, the session's settings and the shutdown callback. No PlayFab build has run here on Windows yet.

Which GSDKs have run here:

| GSDK | Here |
|---|---|
| C++ | Ran on Linux, PlayFab's own sample apps included |
| C# | Ran on Linux, PlayFab's own sample app included |
| Java | Ran on Linux, PlayFab's own runner game included |
| Unity | Matched to its source and tested with its calls; not run yet |
| Unreal | Matched to its source; not run yet |

On Java 17 or later the Java GSDK needs `--add-opens java.base/java.time=ALL-UNNAMED` in its start command.

## How the terms map

| PlayFab | Survival Servers Studio |
|---|---|
| Build | App, build and version |
| Start command, ports | Version `launch` |
| VM size, servers per machine | Version `size`, per game server |
| Standing-by servers | [Pool of ready servers](https://studio.survivalservers.com/docs/concepts/ready-servers/) per region |
| `RequestMultiplayerServer` | `POST /v0/game-servers` |
| Session ID | Game server ID |
| Region | Region or continent |
| Process mode | Windows build as a zip |
| Container mode | Linux image |

## Turn it on

Add `"compat": ["playfab"]` to the version's `launch` (`POST /v0/apps/{app}/versions`):

```json
{
  "name": "2.3.0",
  "build": "<build-id>",
  "size": { "cpu": 2, "memory_gb": 4 },
  "launch": {
    "compat": ["playfab"],
    "ports": [{ "name": "game", "protocol": "udp", "container_port": 7777 }],
    "ready_check": { "type": "local_api" },
    "stop": { "method": "local_api", "shutdown_time": "30s" }
  }
}
```

- `compat`: each game server gets the GSDK's configuration file, named in `GSDK_CONFIG_FILE`, and the machine answers its heartbeats.
- `PF_TITLE_ID`, `PF_BUILD_ID` and `PF_REGION` hold the app's slug, the version's name and the region.
- `ready_check` `local_api`: `ReadyForPlayers()` marks the game server ready.
- `stop` `local_api`: a stop reaches the game only as the GSDK's shutdown callback, with no signal. After `shutdown_time`, the whole process tree is killed. With any other stop method, its signal arrives with the callback.
- `ports`: the GSDK looks ports up by name, so keep your PlayFab build's port names. Our names take lowercase letters, digits and `_`. `container_port` is the port the game listens on in its image.

## What the GSDK sees

| GSDK | Here |
|---|---|
| `ReadyForPlayers()` | Ready; returns once a match has it |
| Health callback | Healthy counts as a health call |
| `UpdateConnectedPlayers()` | The game server's players |
| Shutdown callback | Called when a stop is asked for |
| Maintenance callback | Never called |
| `sessionId` | A GUID from the game server's ID |
| `serverId` (C#), `instanceId` (C++) | Made for the game server, never a key |
| `sessionCookie` | `SESSION_COOKIE`, else `external_id` |
| `initialPlayers` | `INITIAL_PLAYERS`, comma-separated |
| Session metadata | The start's `env` and `labels` |
| `gamePorts` | The listening port per port name |
| Connection info | The public address and ports |

- `ReadyForPlayers()`: on a ready server from a pool the call waits until a match takes it. On a fresh start it returns within one GSDK heartbeat.
- Health callback: checked only when the version sets `health_timeout`. An unhealthy answer sends no health call.
- `sessionId` stays the same for the game server's whole life.
- `gamePorts`: the container port in a Linux image, the public port on Windows. An [admin port](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/#admin-ports) is left out of it and of the connection information; read its port from `GS_PORT_<NAME>`.
- A persistent server has no match, so it gets only its `sessionId`.

## Your backend

Where you called `RequestMultiplayerServer` with a session ID, a cookie, initial players and preferred regions, start a game server:

```http
POST /v0/game-servers
Authorization: Bearer $API_KEY
Content-Type: application/json

{
  "app": "breakwire-linux",
  "branch": "live",
  "placement": { "continents": ["europe", "north-america"] },
  "external_id": "match-88231",
  "env": { "SESSION_COOKIE": "ranked:88231", "INITIAL_PLAYERS": "p_1,p_2" },
  "labels": { "queue": "ranked" }
}
```

- The reply has the address at once: `address.ipv4` and `address.ports`, where PlayFab's had `IPV4Address` and `Ports`.
- `PreferredRegions` become `placement.continents`, or `placement.regions` for exact regions.
- You no longer choose the session ID. Keep your own ID in `external_id`; it is also the session cookie when you send no `SESSION_COOKIE`.

Your PlayFab calls and ours:

- `RequestMultiplayerServer`: `POST /v0/game-servers`.
- `GetMultiplayerServerDetails`: `GET /v0/game-servers/{id}`.
- `ShutdownMultiplayerServer`: `POST /v0/game-servers/{id}/stop`.
- `ListMultiplayerServers`: `GET /v0/game-servers`.
- Standing-by servers: `PUT /v0/apps/{app}/pools/{region}`.

## What does not carry over

- **Maintenance notices.** Never sent.
- **Game certificates.** `gameCertificates` and the certificate folder are empty.
- **Shared content folder.** Empty.
- **`vmId` and `fullyQualifiedDomainName`.** Empty. There is no virtual machine of your own: each game server has its own vCPUs and memory.
- **Several game servers per machine.** Each game server is sized on its own with the version's `size`.

## Migration steps

1. Create the app and a build from the image your PlayFab container build runs, then the version and the `live` branch (see [a Linux image](https://studio.survivalservers.com/docs/guides/upload-builds/#a-linux-image)). `version.json` holds the version above, with `"name": "2.3.0"` and the build's ID as `build`:

   ```bash
   curl -X POST "https://studio.survivalservers.com/v0/apps" \
     -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
     -d '{"name": "Breakwire Server (Linux)", "slug": "breakwire-linux", "os": "linux"}'
   curl -X POST "https://studio.survivalservers.com/v0/apps/breakwire-linux/builds" \
     -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
     -d '{"os": "linux", "source": {"type": "image", "image": "registry.example.com/you/breakwire-server:2.3.0"}}'
   # Once GET /v0/builds/{id} shows "status": "ready"
   curl -X POST "https://studio.survivalservers.com/v0/apps/breakwire-linux/versions" \
     -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
     -d @version.json
   curl -X PUT "https://studio.survivalservers.com/v0/apps/breakwire-linux/branches/live" \
     -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" -d '{"version": "2.3.0"}'
   ```
   With the command-line tool, one step does the build, version and branch, and `version.json` leaves out `name` and `build`:

   ```bash
   survivalservers apps create breakwire-linux --os linux
   survivalservers deploy breakwire-linux --image registry.example.com/you/breakwire-server:2.3.0 \
     --settings version.json --name 2.3.0 --branch live
   ```

   On Windows, process mode takes the same zip your PlayFab build uses. Create the app with `"os": "windows"` and upload the zip as in [upload builds](https://studio.survivalservers.com/docs/guides/upload-builds/). Give the version its `executable`, such as `WindowsServer/BreakwireServer.exe`, and leave out `container_port`.
2. Set a pool in each region where you kept standing-by servers:

   ```bash
   curl -X PUT "https://studio.survivalservers.com/v0/apps/breakwire-linux/pools/europe-west" \
     -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" \
     -d '{"branch": "live", "size": {"cpu": 2, "memory_gb": 4}, "mode": "auto", "min": 2, "max": 40}'
   ```
   Or `survivalservers pools set breakwire-linux europe-west --branch live --cpu 2 --memory-gb 4 --mode auto --min 2 --max 40`.

3. Replace `RequestMultiplayerServer` in your backend with `POST /v0/game-servers`.
4. Play a match on a test game server, then move your players.

If you move from another provider, your first month is free up to $1,000 of use, and we move the game for you.

## Sources

- The GSDK's calls, callbacks and configuration file: [PlayFab GSDK source](https://github.com/PlayFab/gsdk), read October 19, 2026.
- Builds, process and container servers, and servers per machine: [Server terms](https://learn.microsoft.com/en-us/gaming/playfab/multiplayer/servers/server-terms) and [Build definition](https://learn.microsoft.com/en-us/gaming/playfab/multiplayer/servers/build-definition), read October 30, 2026.
- `RequestMultiplayerServer`: [Request Multiplayer Server](https://learn.microsoft.com/en-us/rest/api/playfab/multiplayer/multiplayer-server/request-multiplayer-server), read October 30, 2026.
- Standing-by servers: [Scaling standby servers](https://learn.microsoft.com/en-us/gaming/playfab/multiplayer/servers/scaling-standby), read October 30, 2026.
