# Switching from GameLift

> Run a Linux server build that uses GameLift's server SDK with no changes to your code, and replace fleets and queues with one start call.

A Linux server build made with Amazon GameLift's server SDK 5.x runs here with no changes to your code. Set one launch option on the version, and the machine answers as the GameLift end your SDK talks to. Your game keeps calling `InitSDK()`, `ProcessReady()` and `ProcessEnding()`, and keeps its callbacks. In November 2026, test game servers on GameLift's own C# 5.5.2, Go 5.6.0 and C++ 5.6.1 and 5.6.0 server SDKs, with no changes to the SDK code, each ran whole game sessions this way in our tests. Unreal's GameLift plugin builds this same C++ SDK; no Unreal build has run here this way yet. Windows builds call the [local API](https://studio.survivalservers.com/docs/game-server/local-api/) instead, as shown under "Without the SDK".

## How the terms map

| GameLift | Survival Servers Studio |
|---|---|
| Build | Build: a Windows zip or an image |
| Fleet and its instances | None to size |
| Fleet capacity | [Ready servers](https://studio.survivalservers.com/docs/concepts/ready-servers/) per region, optional |
| Runtime configuration | Version `launch` |
| Processes per instance | One game server each |
| Game session | Game server |
| Game properties, game session data | `env` and `labels` |
| Queue destinations | `placement.regions`, `continents` |
| Player latencies | `placement.player_pings` |
| Player sessions | The players your server reports |
| Alias | Branch |

## Your build

**Linux:** a Linux server runs here as a container image. If GameLift ran your Linux build from a folder, copy that folder into an image:

```dockerfile
FROM ubuntu:24.04
RUN useradd --create-home --uid 10001 server
COPY --chown=server server/ /server/
WORKDIR /server
USER server
ENTRYPOINT ["/server/BreakwireServer.sh"]
```

**Windows:** zip the same folder you upload to GameLift. The launch path becomes the version's `executable`, and the launch parameters its `args`.

## Turn it on

Add `"compat": ["gamelift"]` to the version's `launch` (`POST /v0/apps/{app}/versions`):

```json
{
  "name": "2.3.0",
  "build": "<build-id>",
  "size": { "cpu": 2, "memory_gb": 4 },
  "launch": {
    "compat": ["gamelift"],
    "ports": [{ "name": "game", "protocol": "udp", "container_port": 7777 }],
    "ready_check": { "type": "local_api" },
    "stop": { "method": "sigterm", "shutdown_time": "60s" },
    "log_paths": ["/server/BreakwireServer/Saved/Logs/*.log"]
  }
}
```

- `compat`: the machine sets the variables `InitSDK()` reads (`GAMELIFT_SDK_WEBSOCKET_URL`, `GAMELIFT_SDK_AUTH_TOKEN`, `GAMELIFT_SDK_PROCESS_ID`, `GAMELIFT_SDK_HOST_ID` and `GAMELIFT_SDK_FLEET_ID`) and answers your SDK on `127.0.0.1:9360` inside the container.
- These variables win over any `ServerParameters` your code passes, in every GameLift SDK. A build written for Anywhere fleets runs as it is.
- The auth token belongs to this game server alone. It is not your API key or `GS_TOKEN`, and the local API refuses it.
- No AWS account, key or address is involved. Nothing is sent to AWS.
- `ready_check` `local_api`: `ProcessReady()` marks the game server ready.
- `stop`: a stop reaches your game as `OnProcessTerminate()`. The `sigterm` follows only if it still runs halfway through `shutdown_time`; with `local_api` no signal is sent. After `shutdown_time`, the whole process tree is killed.
- `log_paths` takes the place of the log paths you pass to `ProcessReady()`, which are not collected.
- `health_timeout`: GameLift's SDK reports health every 60 seconds, give or take 10. Leave it out, or set it to `90s` or more.
- Pick `container_port` values other than 9360, or the machine moves its own port to the next free one.

The C++ SDK, and so Unreal's plugin, always connects with TLS. Each game server gets a certificate of its own, valid only for `127.0.0.1`. Only that game server trusts it: its folder comes first in the game's own `SSL_CERT_DIR`, and your image's files are not changed. If your image sets `SSL_CERT_DIR` itself, set it in the version's `env` instead, and the machine adds its folder in front. C++ SDK 5.6.1 (Unreal's plugin 3.4.1) checks the certificate; earlier ones accept any.

## What the SDK sees

| GameLift server SDK | Here |
|---|---|
| `InitSDK()` | Connects to the machine |
| `ProcessReady()` | Ready |
| `OnHealthCheck()` | `true` counts as a health call |
| `OnStartGameSession()` | Called once the game server runs |
| `ActivateGameSession()` | Accepted |
| `AcceptPlayerSession()` | Adds a player |
| `RemovePlayerSession()` | Removes a player |
| `DescribePlayerSessions()` | The ones accepted here |
| `OnProcessTerminate()` | Called when a stop is asked for |
| `GetTerminationTime()` | The stop's deadline |
| `ProcessEnding()` | The game server stops itself |
| `OnUpdateGameSession()` | Never called |

- `OnStartGameSession()`: on a fresh start, right after `ProcessReady()`. On a ready server from a pool, when a match takes it. A persistent server gets one session of its own.
- `AcceptPlayerSession()` and `RemovePlayerSession()`: the player session IDs become the game server's players. None are issued or checked here, so your game checks who joins.
- `DescribePlayerSessions()`: the sessions this game server accepted, `ACTIVE` or `COMPLETED`. A player's own ID is never known here.
- `UpdatePlayerSessionCreationPolicy()` is accepted and changes nothing.
- `StartMatchBackfill()`, `StopMatchBackfill()` and `GetComputeCertificate()` return an error: not available here.
- `GetFleetRoleCredentials()` returns the error GameLift gives an Anywhere fleet: fleet roles are AWS's.

The game session your game gets:

| Game session field | From |
|---|---|
| `GameSessionId` | Game server ID, `-` for `_` |
| `Name` | `GAME_SESSION_NAME` or `external_id` |
| `GameProperties` | The start's `env` and `labels` |
| `GameSessionData` | `GAME_SESSION_DATA` |
| `MatchmakerData` | `MATCHMAKER_DATA` |
| `MaximumPlayerSessionCount` | `MAX_PLAYERS`, else 0 |
| `IpAddress`, `Port` | The public address and port |
| `FleetId` | The app's slug |

- `GameSessionId`: `gs_7hK2...` becomes `gs-7hK2...`, because the SDKs accept no `_` in it.
- `Name`: the game server's ID when the start sets neither.
- `Port`: the public port for the port you pass to `ProcessReady()`, else the version's first port by name.

## Without the SDK

Windows builds, and any build that drops the SDK, make the same steps as HTTP calls to the local API:

| GameLift server SDK | Local API |
|---|---|
| `InitSDK()` | Nothing to call |
| `ProcessReady()` | `POST /v0/self/ready` |
| `onStartGameSession()` | `GET /v0/self/allocation?wait=30s` |
| `ActivateGameSession()` | Nothing to call |
| `AcceptPlayerSession()` | `PUT /v0/self/players` |
| `RemovePlayerSession()` | `PUT /v0/self/players` |
| `onHealthCheck()` | `POST /v0/self/health` |
| `onProcessTerminate()` | `GET /v0/self/shutdown?wait=30s` |
| `GetTerminationTime()` | `deadline` in the shutdown reply |
| `ProcessEnding()` | `POST /v0/self/stop`, or exit 0 |

The game server calls the local API at `GS_LOCAL_API_URL` with `GS_TOKEN`:

```bash
curl -X POST "$GS_LOCAL_API_URL/v0/self/ready" -H "Authorization: Bearer $GS_TOKEN"
curl "$GS_LOCAL_API_URL/v0/self/allocation?wait=30s" -H "Authorization: Bearer $GS_TOKEN"
curl -X PUT "$GS_LOCAL_API_URL/v0/self/players" -H "Authorization: Bearer $GS_TOKEN" \
  -H "Content-Type: application/json" -d '{"ids": ["p_1", "p_2"]}'
```

- `onStartGameSession()`: only a ready server from a pool waits for its match. On a fresh start the match's `env` is already in the environment and the allocation file.
- `AcceptPlayerSession()` and `RemovePlayerSession()`: send the whole list of connected players each time. This is optional.
- `onProcessTerminate()`: a game server that has asked for the shutdown notice gets the notice first. The version's stop signal follows only if it still runs halfway through `shutdown_time`.
- Skip `InitSDK()` behind the switch: without `compat`, it retries for up to about 156 seconds before it fails.

A Windows version:

```json
{
  "name": "2.3.0",
  "build": "<build-id>",
  "size": { "cpu": 2, "memory_gb": 4 },
  "launch": {
    "executable": "WindowsServer/BreakwireServer.exe",
    "args": ["-port={{port.game}}", "-log"],
    "ports": [{ "name": "game", "protocol": "udp" }],
    "ready_check": { "type": "local_api" },
    "log_paths": ["WindowsServer/BreakwireServer/Saved/Logs/*.log"]
  }
}
```

- `{{port.game}}` is filled with the game server's public port at launch; it is also in `GS_PORT_GAME`.
- A game server whose port opens before its map has loaded needs a `log` check on the line it prints when ready. A `port` check would call it ready too early.

## Your backend

| GameLift call | Survival Servers Studio call |
|---|---|
| `StartGameSessionPlacement` | `POST /v0/game-servers` |
| `CreateGameSession` | `POST /v0/game-servers` |
| `DescribeGameSessionPlacement` | Not needed |
| `DescribeGameSessions` | `GET /v0/game-servers/{id}` |
| `SearchGameSessions` | `GET /v0/game-servers?label=...` |
| `CreatePlayerSession` | Not needed |
| `UpdateFleetCapacity` | `PUT /v0/apps/{app}/pools/{region}` |

```http
POST /v0/game-servers
Authorization: Bearer $API_KEY
Content-Type: application/json
Idempotency-Key: match-88231

{
  "app": "breakwire",
  "branch": "live",
  "placement": { "player_pings": { "p_1": { "europe-west": 31, "europe-central": 44 } }, "max_ping_ms": 80 },
  "external_id": "88231",
  "env": { "MAP": "desert", "MAX_PLAYERS": "16" },
  "labels": { "queue": "ranked" }
}
```

- The reply has the address at once, so there is no placement to poll.
- `PlayerLatencies` become `placement.player_pings`, and the queue's destinations `placement.regions` or `placement.continents`.
- `GameProperties` become `env` and `labels`; the game session's other fields come from the `env` names in the table above.
- `SearchGameSessions`: filter the list with `label`, `region` and `players_below`.
- `CreatePlayerSession`: your backend hands players the address and its own join token, and your game checks the token.

## What does not carry over

- **The SDK on Windows builds.** Not available yet: Windows builds use the local API calls above.
- **Several processes per instance.** Each game server is one process with its own size.
- **Player session IDs.** None are issued or validated; your game checks who joins.
- **FlexMatch.** A FlexMatch adapter is not available yet, and neither is match backfill (`StartMatchBackfill()`, `onUpdateGameSession()`). Run your matchmaker on its own and call our start when it makes a match.
- **AWS's own parts.** Fleet roles, compute certificates and AWS credentials are not available.
- **Scaling policies.** A pool is a fixed size or sized `auto` between `min` and `max`.
- **Anywhere fleets.** [Your own machines](https://studio.survivalservers.com/docs/guides/own-machines/) take their place. Not run on real machines yet.

## Migration steps

1. Upload your build as a Linux image and create a version with `"compat": ["gamelift"]`.
2. Replace game session placement in your backend with `POST /v0/game-servers`.
3. Play a match on a test game server, then move your players region by region.

Every call is in the [API reference](https://studio.survivalservers.com/docs/reference/game-servers-and-regions/).

If you move from another provider, your first month is free up to $1,000 of use, and we move the game for you.

## Sources

- Fleets, game sessions and Anywhere fleets: [How Amazon GameLift Servers works](https://docs.aws.amazon.com/gameliftservers/latest/developerguide/gamelift-howitworks.html), read October 30, 2026.
- Queues, placement and `PlayerLatencies`: [Game session queues](https://docs.aws.amazon.com/gameliftservers/latest/developerguide/queues-intro.html), read October 30, 2026.
- Server SDK calls: [Server SDK reference](https://docs.aws.amazon.com/gameliftservers/latest/developerguide/gamelift-sdk-server-api.html), read October 30, 2026.
- What the SDK sends and expects: the server SDKs' own sources, [C# 5.5.2](https://github.com/amazon-gamelift/amazon-gamelift-servers-csharp-server-sdk/releases/tag/v5.5.2), [C++ 5.6.1](https://github.com/amazon-gamelift/amazon-gamelift-servers-cpp-server-sdk/releases/tag/v5.6.1) and [Go 5.6.0](https://github.com/amazon-gamelift/amazon-gamelift-servers-go-server-sdk/releases/tag/v5.6.0), read November 9, 2026.
- Unreal's plugin and its C++ SDK: [Amazon GameLift Plugin for Unreal 3.4.1](https://github.com/amazon-gamelift/amazon-gamelift-plugin-unreal/releases/tag/v3.4.1), read November 9, 2026.
- Player sessions: [Player sessions](https://docs.aws.amazon.com/gameliftservers/latest/developerguide/player-sessions.html), read October 22, 2026.
- Service calls: [Amazon GameLift Servers API reference](https://docs.aws.amazon.com/gameliftservers/latest/apireference/), read October 30, 2026.
