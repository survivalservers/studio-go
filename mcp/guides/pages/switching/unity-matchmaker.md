# Unity Matchmaker

> Start a game server for every Unity Matchmaker match through a Cloud Code module, and move a queue off Multiplay hosting.

Unity Matchmaker hands each match it makes to a hosting provider through a Cloud Code module in Unity's format. A module can start a game server on Survival Servers Studio for each match with two API calls.

Not run on real machines yet: a Cloud Code module in a live Unity Matchmaker queue.

| Matchmaker calls | The module calls |
|---|---|
| `Matchmaker_AllocateServer` | `POST /v0/game-servers` |
| `Matchmaker_PollAllocation` | `GET /v0/game-servers/{id}` |

- **One game server per match.** Send the match ID as the start's `Idempotency-Key` and as `external_id`. When Unity calls again for the same match, the API returns the game server it already started.
- **The address.** Players join at `address.ipv4` and the game port, once the game server's `status` is `running`.
- **The key.** Keep the API key in a Unity secret, never in the module's code or in your game. It needs the scopes `game_servers:write` and `game_servers:read`, limited to this app.

## Our allocator module

Our allocator module lets Unity Matchmaker start a game server on Survival Servers Studio for every match it makes, with no code of your own. You set your app in one file, deploy it, and pick it in the queue's hosting settings.

Not proven: the module is tested only in .NET, against stand-ins for our API. It has never been deployed with `ugs` to a Unity project or run through a real Matchmaker queue.

- **Allocate** answers `Created` with the game server's ID in the allocation data, or `Error` with the API's reason. When a ready server from a pool took the match, its address is there too, so the first poll answers without a call.
- **Poll** answers `Allocated` with the IPv4 address and the game port once the game server is `running`. It answers `Pending` while the game server is `starting` or the API is briefly out of reach, and `Error` once it is stopping, stopped or failed.
- **One game server per match.** The module sends the match ID as the `Idempotency-Key` and as `external_id`. When Unity calls again for the same match, the API returns the game server it already started.
- **No room fails at once.** `503 capacity_unavailable` fails the ticket with the reason, so Unity can match the players again.
- **Short retries.** Rate limits, network failures, other `5xx` errors and a start still running under the same key (`idempotency_key_in_use`) are tried again with the same key. A call gets at most three attempts, with up to 2 seconds between them.

## The match in your server

Your game server reads the match from two settings:

- `MATCH_ID`: the match ID, always sent.
- `MATCH_PROPERTIES`: the match's teams and players as JSON. It is left out when it is over 32,768 characters or when `SendMatchProperties` is `false`.

Both arrive in the environment on a fresh start and in the allocation data on a ready server from a pool ([Environment variables and allocation](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/)). The game server also carries the labels `matchmaker: unity`, `match_id`, `pool` and `queue`, so you can find it in the panel and the API.

## Set it up

1. **Make an API key** in the panel with the scopes `game_servers:write` and `game_servers:read`, limited to this app. In the Unity Dashboard, open **Administration**, **Secrets**, and add it as `GAME_SERVER_API_KEY`. The key never goes in the module's code or in your game.
2. **Get the module** (`integrations/unity-matchmaker` in [our open-source code](https://github.com/survivalservers/studio-samples)) and open `Project/ModuleConfig.cs`. Your app's slug and the API address are the values to set:

   ```csharp
   config.Dependencies.AddSingleton(new AllocatorConfig
   {
       App = "breakwire-windows",
       ApiBaseUrl = "https://studio.survivalservers.com",
       PlacementByRegion = new Dictionary<string, PlacementChoice>
       {
           ["us-east"] = new PlacementChoice { Continents = { "north-america" } },
           ["eu-west"] = new PlacementChoice { Continents = { "europe" } },
       },
   });
   ```

3. **Deploy it** with the Unity Gaming Services command line, from the module's folder. It needs the .NET 8 SDK:

   ```bash
   ugs deploy GameServerAllocator.sln
   ```

4. **Set the queue's hosting** to Cloud Code, with the module `GameServerAllocator`, the allocate function `Matchmaker_AllocateServer` and the poll function `Matchmaker_PollAllocation`.

## Where each match runs

The module picks the placement in this order:

1. **The match's `Region`**, which Unity sets from your players' region choice. `PlacementByRegion` maps it to our regions or continents. A `Region` missing from the map falls through to the next step.
2. **The pool name**, for queues with one pool per region: `PlacementByPool`.
3. **`DefaultPlacement`**, and when that is empty, your app's own default placement.

A `PlacementChoice` lists `Regions` in order of preference, then `Continents` to fall back on. Region and continent IDs are on the [regions](https://studio.survivalservers.com/docs/concepts/regions-and-placement/) page.

## Moving from Multiplay

Unity's Multiplay hosting ended for Unity customers on April 1, 2026, and is now Multiplay by Rocket Science. Unity Matchmaker still works, so a queue that used Multiplay can keep its matchmaking and move only the hosting:

1. Upload the same Unity dedicated server build: the Windows folder as a zip, or the Linux build in an image. See [upload builds](https://studio.survivalservers.com/docs/guides/upload-builds/).
2. Create a version with its executable, its ports and a ready check. See the [Unity quick start](https://studio.survivalservers.com/docs/get-started/unity/).
3. Deploy a Cloud Code module that makes the two calls above, with your API key stored as a Unity secret.
4. In the queue's hosting settings, change Multiplay to Cloud Code with the module's two functions, then play a test match.

Calls your build made through Unity's server SDK for Multiplay get no answer here: drop them. The version's ready check and stop signal do their work, as the [Unity quick start](https://studio.survivalservers.com/docs/get-started/unity/) shows.

## Other settings

| Setting | Default |
|---|---|
| `Version` | Empty |
| `Branch` | Empty: the app's `live` branch |
| `StartMode` | `auto` |
| `PortName` | `game` |
| `TimeLimit` | `null`: no limit |
| `SendMatchProperties` | `true` |
| `ApiKeySecretName` | `GAME_SERVER_API_KEY` |
| `ApiBaseUrl` | None: set `https://studio.survivalservers.com` |

- `Version` starts that version. When it is empty, `Branch` starts the version the branch points at.
- `StartMode` `auto` takes a ready server from a pool when one is waiting and starts a new one otherwise. `warm` and `cold` allow only that kind.
- `PortName` is the port in the version's `launch.ports` that players connect to.
- `TimeLimit` is sent as the start's `ttl`, to stop a match that never ends by itself.
- `ApiKeySecretName` is the Unity secret that holds the API key.

## Test it

The module's own tests run against a fake of the API, with no Unity project:

```bash
dotnet test Tests
```

To try it end to end, deploy the module to a test environment in Unity, point it at a test app, and run a match. The game server shows in the panel with the match's labels.

## Sources

- Hosting through a Cloud Code module, named with its allocate and poll functions: [Unity Matchmaker hosting providers](https://docs.unity.com/en-us/matchmaker/hosting-providers), read October 8, 2026.
- The end of Multiplay hosting by Unity, April 1, 2026: [Unity status notice](https://status.unity.com/info_notices/362941), read October 20, 2026.
- Multiplay's relaunch under Rocket Science: [Multiplay news](https://www.multiplay.com/multiplay-news/multiplay-relaunches-under-rocket-science-and-opens-its-doors-to-new-studios), read October 20, 2026.
