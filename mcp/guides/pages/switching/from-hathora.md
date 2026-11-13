# Switching from Hathora

> Point your Hathora SDK at our address: rooms, processes and connection info keep working, and your image runs as it is.

Hathora's hosting ended on May 5, 2026. If your backend called Hathora Cloud, it can keep making the same calls here: change the server address and the token. Your server image runs as it is and still finds its `HATHORA_*` variables.

In October 2026, Hathora's TypeScript SDK (2.25.6) started, read, listed and stopped game servers here, and read their logs.

## Change the address

Point Hathora's SDK, or your own HTTP calls, at `https://studio.survivalservers.com/compat/hathora`, and use one of our API keys as the developer token:

```ts
import { HathoraCloud } from '@hathora/cloud-sdk-typescript';

const hathora = new HathoraCloud({
	serverURL: 'https://studio.survivalservers.com/compat/hathora',
	hathoraDevToken: process.env.API_KEY, // our API key
	appId: 'app_...', // our app's ID
});

const room = await hathora.roomsV2.createRoom({ region: 'Frankfurt', roomConfig: '{"mode":"1v1"}' }, undefined, matchId);
const info = await hathora.roomsV2.getConnectionInfo(room.roomId); // starting, then active with the address
```

- `hathoraDevToken`: an API key with `game_servers:write` and `game_servers:read`, and `logs:read` if you read logs.
- `appId`: our app's ID (`app_...`) or its slug. Connection info needs the ID, because it is called without a key.
- Each call becomes a call on our own API with your key, so the key's scopes, app limits and rate limits apply.

## How the terms map

| Hathora | Survival Servers Studio |
|---|---|
| Application | App |
| Deployment | Version |
| Build tag | Version name |
| Room | Game server, started fresh |
| Room ID | `external_id` |
| Process | Game server |
| Region | Region |
| Developer token | API key |

## Your server image

1. Create a Linux app and a build from the image you ran on Hathora, from any registry:

   ```bash
   curl -X POST "https://studio.survivalservers.com/v0/apps" \
     -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
     -d '{"name": "Your Game", "slug": "your-game", "os": "linux"}'
   curl -X POST "https://studio.survivalservers.com/v0/apps/your-game/builds" \
     -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
     -d '{"os": "linux", "source": {"type": "image", "image": "registry.example.com/you/your-server:1.0.0"}}'
   ```

   The build is ready to use once `GET /v0/builds/{id}` shows `status` `ready`.
   With the command-line tool, which waits for the build:

   ```bash
   survivalservers apps create your-game --os linux
   survivalservers builds image your-game registry.example.com/you/your-server:1.0.0 --wait
   ```

2. Create a version with the vCPUs, memory and container port your deployment had, a port named `default`, and `env_aliases: ["hathora"]`. Send it to `POST /v0/apps/your-game/versions`:

   ```json
   {
     "name": "1.0.0",
     "build": "<build-id>",
     "size": { "cpu": 1, "memory_gb": 2 },
     "launch": {
       "ports": [{ "name": "default", "protocol": "udp", "container_port": 7777 }],
       "ready_check": { "type": "port", "port": "default" },
       "env_aliases": ["hathora"]
     }
   }
   ```

   The `port` check counts the game server ready once it listens on `default`. If your game listens before it has finished loading, use a `log` check with the `pattern` of its ready line.

3. Point the `live` branch at it:

   ```bash
   curl -X PUT "https://studio.survivalservers.com/v0/apps/your-game/branches/live" \
     -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" -d '{"version": "1.0.0"}'
   ```
   Or `survivalservers branches set your-game live 1.0.0`.

   A room starts from `live` unless the call names a `deploymentId`, which is read as a version name or ID.

With `env_aliases: ["hathora"]` each game server also gets Hathora's variables. Our own `GS_*` variables are there as well.

| Variable | Value |
|---|---|
| `HATHORA_APP_ID` | Our app's ID |
| `HATHORA_PROCESS_ID` | The game server's ID |
| `HATHORA_DEPLOYMENT_ID` | The version's name |
| `HATHORA_BUILD_TAG` | The version's name |
| `HATHORA_ALLOCATED_CPU` | vCPUs |
| `HATHORA_ALLOCATED_MEMORY_MB` | Memory in MiB |
| `HATHORA_REGION` | The nearest Hathora region |
| `HATHORA_ROOMS_PER_PROCESS` | Always `1` |
| `HATHORA_HOSTNAME` | The public IP address |
| `HATHORA_DEFAULT_PORT` | The public port of `default` |
| `HATHORA_<NAME>_PORT` | The public port of each other port |
| `HATHORA_INITIAL_ROOM_ID` | The room ID (`external_id`) |
| `HATHORA_INITIAL_ROOM_CONFIG` | The room's `roomConfig` |

- `HATHORA_DEFAULT_PORT`: with no port named `default`, the first port by name.
- `HATHORA_INITIAL_ROOM_CONFIG` is set only when `createRoom` sends a `roomConfig`.

## Supported calls

| Hathora call | Here |
|---|---|
| `roomsV2.createRoom` | A fresh start in the matching region |
| `roomsV2.getConnectionInfo` | The address once active; no key |
| `roomsV2.getRoomInfo` | The room and its allocation |
| `roomsV2.destroyRoom` | Stops the room's game server |
| `processesV3.createProcess` | A fresh start with no room |
| `processesV3.getProcess` | A game server as a process |
| `processesV3.stopProcess` | Stops the game server |
| `processesV3.getLatestProcesses` | The 100 newest, filtered |
| `discoveryV2.getPingServiceEndpoints` | Ping addresses |
| `logsV1.getLogsForProcess` | The console, `follow`, `tailLines` |

- `createRoom` takes the region, `roomConfig`, `clientIPs` and your own room ID.
- `getConnectionInfo` answers `starting` while the game server starts, then `active` with the host and ports. Once the room stops, it answers `404`.
- `getPingServiceEndpoints` gives each Hathora region the ping beacon of the region it maps to. Today every Hathora region maps to `europe-west`, so each gets its beacon.
- `getLatestProcesses` reads the app's 100 newest game servers, then keeps those that match `status` and `region`.
- `stopProcess` and `destroyRoom` on a game server that has already stopped succeed (`204`).

## Differences

- **Regions map to ours.** A Hathora region goes to our nearest region. The regions are `Seattle`, `Los_Angeles`, `Washington_DC`, `Chicago`, `Dallas`, `London`, `Frankfurt`, `Mumbai`, `Singapore`, `Tokyo`, `Sydney`, `Sao_Paulo`, `Johannesburg` and `Dubai`. An unknown region answers `422`.
- **`clientIPs` reach placement** as `placement.player_ips`.
- **One room per process.** A room is its own game server, started fresh. `roomsPerProcess` is always 1, and a room's process lives as long as the room.
- **Room IDs.** A room ID you give is kept, and can be used once in your studio: a second `createRoom` with it answers `422`. Room IDs are your studio's own, so another studio's never clash with yours. Rooms we name get a random 32-character ID.
- **Status words.** A room is `scheduling` while its game server starts, `active` once it is ready, and `destroyed` once it is stopping or has ended. A process is `starting`, `running`, `stopping`, `stopped` or `failed`.
- **No capacity.** When no machine can take a room or process, or its region cannot be reached, `createRoom` and `createProcess` answer `503`. Try again shortly, or in another region. A version not yet ready there answers `422`.
- **No app secret.** `HATHORA_HOSTNAME` holds an IP address, not a host name, and there is no `HATHORA_APP_SECRET`: a game server never needs a key here.

## What does not carry over

- **Lobbies, player logins, builds, deployments, fleets and billing.** These calls answer `404`. Make builds and versions with our API or the panel. A backend that needs lobbies keeps its own.
  The command-line tool `survivalservers` makes builds and versions too.
- **Many rooms in one process.** `getActiveRoomsForProcess`, `getInactiveRoomsForProcess`, `resumeRoom`, `updateRoomConfig` and the suspend call are not translated.
- **Older call versions.** Only `roomsV2`, `processesV3`, `logsV1` and `discoveryV2` are translated.
- **Process metrics and log downloads.** Read the game server's metrics and logs on our API.

## Migration steps

1. Push your image, create the app, build and version above, and point `live` at the version.
2. Make an API key for your backend with `game_servers:write` and `game_servers:read`, limited to the app.
3. Change the SDK's server address and token, and set `appId` to our app's ID.
4. Play a match on a test room, then move your players.

You can move to our own API call by call. A room is a game server: `POST /v0/game-servers` with your room ID as `external_id` and `Idempotency-Key`, and the address is in the start reply. Every call is in the [API reference](https://studio.survivalservers.com/docs/reference/game-servers-and-regions/).

If you move from another provider, your first month is free up to $1,000 of use, and we move the game for you.

## Sources

- Hathora's end date, May 5, 2026: [Hathora blog](https://web.archive.org/web/20260308180848/https://hathora.dev/blog), read October 8, 2026 (archived copy of March 8, 2026).
- Hathora's calls, regions and status words: [@hathora/cloud-sdk-typescript 2.25.6](https://www.npmjs.com/package/@hathora/cloud-sdk-typescript), read October 8, 2026.
- Hathora's `HATHORA_*` variables: [Environment Variables](https://web.archive.org/web/20260121012737/https://hathora.dev/docs/guides/access-env-variables), read October 8, 2026 (archived copy of January 21, 2026).
