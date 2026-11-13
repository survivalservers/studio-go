# Switching from Agones

> Run a server build that uses the Agones SDK with no changes to your code: the machine answers as Agones' SDK server.

A server build that calls the Agones SDK runs here with no changes to your code. It can come from your own Kubernetes cluster or from a provider built on Agones, such as GameFabric. Set one launch option on the version, and each game server finds an Agones SDK server where its SDK looks for one. There is no Kubernetes and no sidecar to run. In October 2026, Agones' simple-game-server ran here as it is: its published image on Linux, and a source build on Windows. In November 2026, Agones' own Go and C# SDKs (v1.62.0) made every call here as written: ready, health, the watch through allocation and shutdown, labels, annotations, player tracking and their own stop. Reserve and counters were refused, as below.

## How the terms map

| Agones | Survival Servers Studio |
|---|---|
| `GameServer` | Game server |
| Fleet of Ready game servers | [Pool of ready servers](https://studio.survivalservers.com/docs/concepts/ready-servers/) per region |
| `FleetAutoscaler` | Pool `mode` `auto`, `min` to `max` |
| `GameServerAllocation` | `POST /v0/game-servers` |
| Allocation selectors | `app`, `version` and `placement` |
| Fleet `containerPort` | Version `container_port` |
| `portPolicy: Dynamic` | `port_policy` `dynamic`, the default |
| `portPolicy: None` | `port_policy` `none`: an [admin port](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/#admin-ports) |
| `portPolicy: Passthrough` | [`port_policy` `passthrough`](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/#passthrough-ports), Linux images |
| Cluster | Region |

## Turn it on

Add `"compat": ["agones"]` to the version's `launch` (`POST /v0/apps/{app}/versions`):

```json
{
  "name": "2.3.0",
  "build": "<build-id>",
  "size": { "cpu": 2, "memory_gb": 4 },
  "launch": {
    "compat": ["agones"],
    "ports": [{ "name": "game", "protocol": "udp", "container_port": 7654 }],
    "ready_check": { "type": "local_api" },
    "stop": { "method": "sigterm", "shutdown_time": "30s" }
  }
}
```

- `compat`: the machine answers Agones' gRPC calls on `127.0.0.1:9357` and its HTTP calls on `127.0.0.1:9358`, the default ports of the Agones SDKs. It also sets `AGONES_SDK_GRPC_HOST`, `AGONES_SDK_GRPC_PORT` and `AGONES_SDK_HTTP_PORT`.
- `ready_check` `local_api`: your SDK's `Ready()` marks the game server ready.
- `container_port`: your fleet's `containerPort`. The game server listens where it always did.
- Only the game server itself can reach its SDK server.

## What your SDK calls do

| SDK call | Here |
|---|---|
| `Ready()` | Marks the game server ready |
| `Health()` | A health call, for `health_timeout` |
| `Shutdown()` | The game server stops itself |
| `GetGameServer()` | State, address, ports, labels |
| `WatchGameServer()` | The same, on every change |
| `SetLabel()` | Our label `sdk-<key>` |
| `SetAnnotation()` | Kept for the game only |
| `Allocate()` | Succeeds once allocated |
| `Reserve()` | Refused |
| Player tracking (alpha) | The game server's players |

- `Ready()`: a game server in a pool waits for a match; any other becomes `running`.
- `Health()`: checked only when the version sets `health_timeout`, and passed on at most once a second.
- `Shutdown()`: the same as exiting with code 0 (exit reason `self`). The version's stop method follows.
- `WatchGameServer()`: an allocation and a stop request arrive at once; other changes arrive within a second.
- `SetLabel(key, value)`: the key is 1 to 59 characters of `a-z 0-9 _ . -`. The API filters on it as `label=sdk-<key>:<value>`, and the game sees it as `agones.dev/sdk-<key>`, as on Agones.
- `SetAnnotation()`: shown back to the game as `agones.dev/sdk-<key>`, at most 64 per game server. The API never sees it.
- `Allocate()`: refused until the game server is allocated, so a ready server in a pool cannot take itself. A fresh start counts as allocated once it is ready.

The game server's `status.state` moves as on Agones:

| State | When |
|---|---|
| `Scheduled` | Starting |
| `RequestReady` | After `Ready()`, before it counts |
| `Ready` | Waiting in a pool, or a persistent server |
| `Allocated` | A match has it; a fresh start once ready |
| `Shutdown` | A stop was asked for |

## Match settings

What you put in the start call reaches the game the way an Agones allocation delivers it:

```http
POST /v0/game-servers
Authorization: Bearer $API_KEY
Content-Type: application/json

{
  "app": "breakwire-linux",
  "branch": "live",
  "placement": { "continents": ["europe"] },
  "external_id": "88231",
  "env": { "MAP": "desert", "MODE": "ranked" },
  "labels": { "queue": "ranked" }
}
```

- `env` arrives as annotations (`MAP`, `MODE`), secret values included. Only this game server can read them.
- `external_id` arrives as the annotation `external_id`.
- `labels` arrive as labels.

A server that reads its match from the annotations and labels in `WatchGameServer` keeps doing that.

## Your backend

| On Agones | Here |
|---|---|
| Create a `GameServerAllocation` | `POST /v0/game-servers` |
| Read the allocation's address | `address` in the same reply |
| Delete the `GameServer` | `POST /v0/game-servers/{id}/stop` |
| List `GameServer` objects | `GET /v0/game-servers` |
| Fleet size, autoscaler | `PUT /v0/apps/{app}/pools/{region}` |

The start reply holds `address.ipv4` and `address.ports`, final at once. A pool of ready servers takes the place of a fleet's Ready replicas:

```http
PUT /v0/apps/breakwire-linux/pools/europe-west
Authorization: Bearer $API_KEY
Content-Type: application/json

{ "branch": "live", "size": { "cpu": 2, "memory_gb": 4 }, "mode": "auto", "min": 2, "max": 40 }
```

## What does not carry over

- **`Reserve()`.** There are no reservations: a ready server in a pool is taken only by a start.
- **`Allocate()` from inside the game server.** The API allocates game servers.
- **`SetAnnotation()` for your backend.** The API does not see annotations. Use `SetLabel()` for anything your backend filters on.
- **Counters and lists (beta).** Not available yet. The calls are refused. Agones' simple-game-server (image 0.44) counts players this way; report players with player tracking (alpha) or `PUT /v0/self/players` on the [local API](https://studio.survivalservers.com/docs/game-server/local-api/). Agones' Unreal plugin and its Node.js, Rust and C++ SDKs (1.62.0) have no player tracking calls, so a build on one of them uses `PUT /v0/self/players`.
- **`Ready()` again after a match.** On Agones a game server can go back to `Ready` and take another match. Here a game server takes one match: a second `Ready()` answers but changes nothing, and the state stays `Allocated`. Exit or call `Shutdown()` when the match ends, or set `ttl` in the start call.
- **Agones' Go SDK through an update of our machine agent.** The agent restarts for an update while your game server keeps running. The Go SDK opens its health and watch streams only once, so after the restart its `Health()` calls fail (a version with `health_timeout` is then stopped) and `WatchGameServer()` hears nothing more, a match included. Agones' C# SDK opens both again by itself.
- **Kubernetes fields.** `namespace` is always `default`. `generation` and `resource_version` are filled in only so readers do not fail. Health settings read as zeros: ours come from the version.
- **SDK calls without limits.** Each game server's SDK server takes 100 calls a second, with bursts of 200, and 8 open watches. A call's body is at most 64 KiB. Player tracking holds at most 1,000 players. An annotation value is at most 4 KiB, and a game server's annotations 64 KiB together. A call over a limit is refused.

## Migration steps

1. Create the app and a build from the image your fleet runs, then the version and the `live` branch. `version.json` holds the version above, with `"name": "2.3.0"` and the build's ID as `build`:

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

   For a Windows build there, use `--os windows` and pass `--zip` with the game server folder's zip.

   For a Windows build, create the app with `"os": "windows"` and upload the game server folder's zip as in [upload builds](https://studio.survivalservers.com/docs/guides/upload-builds/). Its `version.json` differs in three places:

   - `executable` is the server program's path in the zip. A Windows game server listens on the port it is given, so pass it in `args`, for example `["-port", "{{port.game}}"]`.
   - Ports have no `container_port`.
   - `stop` uses `ctrl_break`, the Windows default, or is left out. `sigterm` is for Linux only.

2. Set a pool in each region where you kept a fleet:

   ```bash
   curl -X PUT "https://studio.survivalservers.com/v0/apps/breakwire-linux/pools/europe-west" \
     -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" \
     -d '{"branch": "live", "size": {"cpu": 2, "memory_gb": 4}, "mode": "auto", "min": 2, "max": 40}'
   ```
   Or `survivalservers pools set breakwire-linux europe-west --branch live --cpu 2 --memory-gb 4 --mode auto --min 2 --max 40`.

3. Replace the allocation in your backend with `POST /v0/game-servers`.
4. Play a match on a test game server, then move your players.

If you move from another provider, your first month is free up to $1,000 of use, and we move the game for you.

## Sources

- Agones' SDK calls and fields: [Agones v1.61.0 source](https://github.com/agones-dev/agones) (`proto/sdk`), read October 19, 2026, and the same in v1.62.0, with its Go, C# and Unreal SDKs (`sdks/`), read November 9, 2026.
- Agones' game server states: [GameServer reference](https://agones.dev/site/docs/reference/gameserver/), read October 30, 2026.
- GameFabric runs on Agones: [GameFabric home page](https://gamefabric.com/), read October 8, 2026.
