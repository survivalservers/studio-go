# Unreal Engine

> Package an Unreal Engine dedicated server for Windows or Linux, run it with no plugin, and make the optional calls with Unreal's HTTP module.

An Unreal dedicated server runs on Survival Servers Studio as you package it today. Upload the Windows zip or the Linux image, pass the port on the command line, and start a game server with one call. You don't need a plugin or any new code. The optional calls are plain HTTP, which Unreal's own HTTP module makes.

Our plugin, in `unreal-plugin/` in the [samples](https://github.com/survivalservers/studio-samples), makes those calls for you, and the template in `unreal/` uses it. The plugin is built and tested with Unreal 5.8 only.

In November 2026, a project built with Unreal 5.8.4 from source ran here on Windows and Linux. Two packaged clients joined it over the internet.

## Package the server

Dedicated server targets need Unreal Engine built from source. Add a Server target next to your game target:

```csharp
using UnrealBuildTool;

public class YourGameServerTarget : TargetRules
{
    public YourGameServerTarget(TargetInfo Target) : base(Target)
    {
        Type = TargetType.Server;
        DefaultBuildSettings = BuildSettingsVersion.Latest;
        IncludeOrderVersion = EngineIncludeOrderVersion.Latest;
        ExtraModuleNames.Add("YourGame");
        bUseLoggingInShipping = true;
        BuildEnvironment = TargetBuildEnvironment.Unique;
    }
}
```

A Shipping server writes no log at all unless its target sets `bUseLoggingInShipping`, which needs `TargetBuildEnvironment.Unique`. Without it the console and `Saved/Logs` stay empty.

Package it with RunUAT. This line packages it for Windows; run it again with `-serverplatform=Linux` for Linux:

```bat
RunUAT.bat BuildCookRun -project=YourGame.uproject -noP4 -server -serverplatform=Win64 -noclient ^
  -build -cook -stage -pak -archive -archivedirectory=Builds -serverconfig=Shipping -nodebuginfo ^
  -utf8output -AdditionalCookerOptions=-notraceserver
```

`-nodebuginfo` leaves Unreal's debug files out of the package. They stay in your project's `Binaries` folder.

- **Windows.** Zip the `Builds/WindowsServer` folder with the folder kept, and upload it as it is. Its executable is `WindowsServer/YourGameServer.exe`. Unreal 5.8 needs the Visual C++ runtime 14.50 or newer; our Windows machines have 14.51.
- **Linux.** The Linux package needs Unreal's cross-compile toolchain on the Windows PC. Put `Builds/LinuxServer` in an image. Unreal servers will not run as root, so give the image a user of its own. A server packaged on Windows also has no execute bits, and without them the start fails with exit code 126. This `Dockerfile` handles both:

```dockerfile
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* \
 && useradd --create-home --uid 10001 server
COPY --chown=server Builds/LinuxServer/ /game/
RUN chmod +x /game/YourGameServer.sh /game/YourGame/Binaries/Linux/YourGameServer-Linux-Shipping
USER server
EXPOSE 7777/udp
ENTRYPOINT ["/game/YourGameServer.sh", "-log", "-Port=7777"]
```

## Ports and arguments

Unreal takes its game port from `-Port=`. In the version's launch arguments, `{{port.game}}` becomes the public port of each game server:

```json
"args": ["-log", "-Port={{port.game}}"],
"ports": [{ "name": "game", "protocol": "udp" }]
```

A Linux image listens on a fixed port inside the container, and each public port is mapped onto it. Put `-Port=7777` in the image's entry point and `"container_port": 7777` on the port. If you answer server browser queries, add a second named port (`query`) and pass it the same way. A server that lists itself in Steam's server browser tells Steam its own ports: give those ports `"port_policy": "passthrough"` instead, and pass `-Port={{port.game}}` (see [passthrough ports](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/#passthrough-ports)).

The same values are in environment variables (`GS_PORT_GAME`, `GS_PUBLIC_IP`, `GS_ID` and others): `FPlatformMisc::GetEnvironmentVariable(TEXT("GS_ID"))` in C++.

**Name every port the server opens.** A [Windows game server](https://studio.survivalservers.com/docs/game-server/windows-builds/) cannot bind ports it was not given. Your own extra ones (an admin REST or RCON port) get their own entries and arguments. Package the server as Shipping: our project's Shipping server opened only its game port.

## Create a version

```json
{
  "name": "1.0.0",
  "build": "bld_...",
  "size": { "cpu": 2, "memory_gb": 4 },
  "launch": {
    "executable": "WindowsServer/YourGameServer.exe",
    "args": ["-log", "-Port={{port.game}}"],
    "ports": [{ "name": "game", "protocol": "udp" }],
    "ready_check": { "type": "port", "port": "game" },
    "stop": { "method": "ctrl_break", "shutdown_time": "15s" },
    "log_paths": ["WindowsServer/YourGame/Saved/Logs/*.log"],
    "artifact_paths": ["WindowsServer/YourGame/Saved/Crashes/**"]
  },
  "regions": ["*"]
}
```

- **Ready check.** `port` counts the server as joinable once it has bound its game port. Unreal binds it while it loads the map, before play begins. If your server loads more data or levels after that, use a `log` check on a line it prints once it is ready. If it says ready with a call to the local API, use `local_api` instead.
- **Stop.** Ctrl+Break on Windows and SIGTERM on Linux ask Unreal to exit. `shutdown_time` is how long it has before the process is ended.
- **Stop with the shutdown notice.** A server that keeps `GET /v0/self/shutdown?wait=30s` open on the local API gets the notice first, so it can save and exit. The signal follows only if it still runs halfway through `shutdown_time`.
- **Stop with the plugin.** The plugin hears the shutdown notice, ends the match and exits. The signal follows only if the server still runs halfway through `shutdown_time`. In November 2026, the sample's API stop on Linux exited with code 0 in 3.8 s, and no signal was sent. The console and the log file kept every line, Unreal's last one (`LogExit: Exiting.`) included. On Windows, on the same agent, it exited with code 0 in 0.4 s.
- **Stop without the notice.** The signal is sent straight away. On Linux, Unreal's launch script ends at once on SIGTERM. Our init then waits for the server itself to exit: up to 30 seconds, and never past `shutdown_time`.
- **Logs and files.** The console and the `log_paths` files are kept for 90 days; `artifact_paths` files are collected at exit. Crash dumps are caught without being listed in `artifact_paths`.
- **Linux image.** Leave out `executable` and `args`, give the port `"container_port": 7777`, and stop with `sigterm`. Paths start at the image's `/`, such as `/game/YourGame/Saved/Logs/*.log`. Our project's log file on Linux could be followed live while it ran.

The sample's own settings are `version-windows.json` and `version-linux.json`.

Every field is in the [API reference](https://studio.survivalservers.com/docs/reference/apps-builds-and-versions/).

## Start a game server

```bash
curl -X POST "https://studio.survivalservers.com/v0/game-servers" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d '{"app": "your-game", "version": "1.0.0", "placement": {"continents": ["europe"]}}'
```

Players connect to `address.ipv4` and `address.ports.game.port` from the reply: `open 203.0.113.24:31042` in the client's console.

## Optional: local API calls

Each optional call is plain HTTP to the [local API](https://studio.survivalservers.com/docs/game-server/local-api/) on the machine, at `GS_LOCAL_API_URL` with the token in `GS_TOKEN`. Unreal's own HTTP module makes them: add `"HTTP"` to your module's dependencies in its `.Build.cs`. When `GS_LOCAL_API_URL` is empty (a run on your own PC), skip the calls. Run this once the map has loaded, to say ready:

```cpp
#include "HttpModule.h"
#include "Interfaces/IHttpRequest.h"

const FString AgentUrl = FPlatformMisc::GetEnvironmentVariable(TEXT("GS_LOCAL_API_URL"));
if (!AgentUrl.IsEmpty())  // empty on your own PC
{
    TSharedRef<IHttpRequest, ESPMode::ThreadSafe> Request = FHttpModule::Get().CreateRequest();
    Request->SetURL(AgentUrl + TEXT("/v0/self/ready"));
    Request->SetVerb(TEXT("POST"));
    Request->SetHeader(TEXT("Authorization"), TEXT("Bearer ") + FPlatformMisc::GetEnvironmentVariable(TEXT("GS_TOKEN")));
    Request->ProcessRequest();  // 204: recorded
}
```

With this call, use the `local_api` ready check. The other calls (the match, players, the shutdown notice) are on the [local API](https://studio.survivalservers.com/docs/game-server/local-api/) page.

## Optional: use the plugin

The Survival Servers Studio plugin makes every optional call to the [local API](https://studio.survivalservers.com/docs/game-server/local-api/) through Unreal's non-blocking HTTP module. The calls have the same names as in our game server SDK. It works only in dedicated servers: clients and the editor never create it. Without `GS_LOCAL_API_URL` (a run on your own PC) every call does nothing.

1. Copy the `SurvivalServersStudio` folder into your project's `Plugins` folder.
2. Enable it in the project file, or in Edit > Plugins:

   ```json
   "Plugins": [{ "Name": "SurvivalServersStudio", "Enabled": true }]
   ```

3. To call it from C++, add `"SurvivalServersStudio"` to your module's dependencies in its `.Build.cs`.
4. In the version, use the `local_api` ready check: `"ready_check": { "type": "local_api" }`.

With no code of yours, it:

- says ready once the map has loaded and the server listens;
- reads the match (its ID, settings, labels and arguments) before the first player arrives;
- reports players from the game mode's logins and logouts, sending the whole list after each change;
- sends the server's frame times once a second, for the tick graph;
- hears the shutdown notice, tells your game, then ends the match and exits;
- writes `match-result.json` for the platform to collect.

### Settings

Project Settings > Plugins > Survival Servers Studio, saved in `Config/DefaultGame.ini`:

| Setting | Default |
|---|---|
| Ready When Map Loaded | On |
| Report Players From Logins | On |
| Stop When Last Player Leaves | Off |
| Exit On Stop Request | On |
| Report Frame Times | On |

- **Ready When Map Loaded.** Turn it off to call `Ready` yourself, after loading your own data.
- **Report Players From Logins.** Turn it off to report players with `PlayerConnect` and `PlayerDisconnect`.
- **Stop When Last Player Leaves.** Ends a match once everyone who joined has left. A persistent server never ends this way.
- **Exit On Stop Request.** Turn it off to save and exit in your own time. If the server still runs halfway through `shutdown_time`, the stop signal comes then.

### Calls

`USurvivalServersSubsystem` is a game-instance subsystem. In Blueprints every call is under Survival Servers.

| Call | What it does |
|---|---|
| `Ready` | Ready for players (`local_api` check) |
| `PlayerConnect(Id)` | A player joined |
| `PlayerDisconnect(Id)` | A player left |
| `SetPlayerCount(Count)` | Player count, for games without IDs |
| `SetLabel(Name, Value)` | Sets a label your backend filters on |
| `RemoveLabel(Name)` | Removes a label |
| `Tick(P50Ms, P99Ms, Hz)` | Last second's frame times |
| `Health()` | Says it is still healthy |
| `Saved(Keep)` | A persistent server finished a save |
| `Stop(Reason)` | Ends the match |
| `OnMatchAllocated` | Event: the match arrived |
| `OnStopRequested` | Event: save now |

- `GetMatchId`, `GetSetting(Name, Value)`, `GetPublicIp` and `GetPublicPort` read the match and the address.
- `IsHosted` and `IsPersistentServer` say whether it runs on the platform, and how.
- In C++, `WaitAllocation(WaitSeconds, Done)` and `WaitShutdown(WaitSeconds, Done)` wait up to 60 s each.
- `Stop` sends the last player report, writes `match-result.json`, stops the game server and exits.

```cpp
#include "SurvivalServersSubsystem.h"

USurvivalServersSubsystem* Server = GetGameInstance()->GetSubsystem<USurvivalServersSubsystem>();
if (Server)  // null on clients and in the editor
{
    Server->SetLabel(TEXT("map"), TEXT("desert"));
    Server->OnStopRequested.AddDynamic(this, &AMyGameMode::SaveBeforeStop);
}
```

## Next steps

- Run every call on your own PC first: [test on your own PC](https://studio.survivalservers.com/docs/game-server/test-locally/).
- Every call on the machine: [the local API](https://studio.survivalservers.com/docs/game-server/local-api/).
- Start a game server for every match: [start game servers from your backend](https://studio.survivalservers.com/docs/guides/start-matches-from-your-backend/).
