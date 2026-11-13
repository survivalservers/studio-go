# Unity

> Build a Unity dedicated server for Windows or Linux, run it with no plugin, and start a game server with one call.

A Unity dedicated server runs on Survival Servers Studio as you build it today. Upload the Windows zip or the Linux image, create a version, and start a game server with one call. Nothing needs to be added to your game. The optional calls (ready, the match, players, stop) are plain HTTP, which `UnityWebRequest` makes.

The sample package in `unity/` in the [samples](https://github.com/survivalservers/studio-samples) adds a build script and those calls.

Unity hosting is proven with shipped Unity dedicated servers. In October and November 2026, Valheim and Unturned ran here on Windows and Linux, and V Rising and Rust on Windows. They cover Unity 2022.3 and Unity 6, with Mono and IL2CPP. Each answered queries from the internet, saved its world when stopped and loaded it on the next run.

## Build the server

In Unity Hub, add **Dedicated Server** build support for Windows, Linux or both. Then build the server as Unity's [Dedicated Server guide](https://docs.unity3d.com/6000.0/Documentation/Manual/dedicated-server-build.html) describes. In Unity 6, pick **Windows Server** or **Linux Server** in Build Profiles. Before Unity 6, pick **Dedicated Server** in Build Settings. A build script sets the same thing in its `BuildPlayerOptions`:

```csharp
options.subtarget = (int)StandaloneBuildSubtarget.Server;
```

- **Windows.** Zip the build folder with the folder kept, and upload the zip. Its executable is the `.exe` Unity wrote, such as `WindowsServer/GameServer.exe`.
- **Linux.** Put the build folder in an image that runs as a user of its own and listens on a fixed port, such as UDP 7777:

```dockerfile
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY LinuxServer/ /game/
RUN chmod +x /game/GameServer.x86_64
ENV HOME=/tmp
USER nobody
EXPOSE 7777/udp
ENTRYPOINT ["/game/GameServer.x86_64", "-batchmode", "-nographics", "-logFile", "-"]
```

- Start the server with `-batchmode -nographics -logFile -`. The last two send Unity's log to the console, which is kept for 90 days.
- Ubuntu's base image has no certificate authorities, so every HTTPS call from Unity fails without `ca-certificates`. Unturned's Linux server could not finish starting without it.
- `chmod +x` gives the program the execute bit that a build copied from Windows can lack. `HOME=/tmp` gives `nobody` a home folder it can write to.

## Listen on the right port

Each game server gets its port in an environment variable: `GS_PORT_GAME` on Windows, and `GS_PORT_GAME_INTERNAL` inside a Linux image (the container port the public port is mapped to). Read it before you start your network manager:

```csharp
static ushort GamePort()
{
    var port = Environment.GetEnvironmentVariable("GS_PORT_GAME_INTERNAL")
        ?? Environment.GetEnvironmentVariable("GS_PORT_GAME");
    return port != null ? ushort.Parse(port) : (ushort)7777; // 7777 on your own PC
}

// Netcode for GameObjects with Unity Transport
var transport = NetworkManager.Singleton.GetComponent<UnityTransport>();
transport.SetConnectionData("0.0.0.0", GamePort(), "0.0.0.0");
NetworkManager.Singleton.StartServer();
```

With Mirror or FishNet, set the same number on your transport's port before the server starts.

With the package, `GsConfig.FromEnvironment().ListenPort` gives the same number.

## Create a version

A version is your build plus how to run it. For the Windows zip, with no code added to your game:

```json
{
  "name": "1.0.0",
  "build": "bld_...",
  "size": { "cpu": 1, "memory_gb": 2 },
  "launch": {
    "executable": "WindowsServer/GameServer.exe",
    "args": ["-batchmode", "-nographics", "-logFile", "-"],
    "ports": [{ "name": "game", "protocol": "udp" }],
    "ready_check": { "type": "port", "port": "game" }
  },
  "regions": ["*"]
}
```

- **Linux image.** Leave out `executable` and `args` (the image's entry point has them), and give the port `"container_port": 7777`.
- **Ready check.** `port` counts the server as joinable once it has bound its game port. Some Unity servers open a port before their world has loaded, so a `port` check calls them ready too early. Give those a `log` check on the line they print once they are ready. For example, V Rising waits for `Startup Completed` and Unturned on Linux for `Loading level: 100%`. Stopped before its line, V Rising saved nothing.
- **Stop.** With no `stop`, Windows sends Ctrl+Break and Linux SIGTERM, then ends the process after 30 seconds. Your own server saves only if your code does it when Unity quits.
- **Shipped servers.** Hosted here, Valheim and Unturned saved and exited on Ctrl+Break on Windows and `sigint` on Linux, and V Rising on Ctrl+Break. Rust saved and exited on its RCON commands `server.save` and `quit`.
- **Console commands.** On Windows, a Unity server that reads single key presses from its console never sees typed commands; Unturned took none. Stop such a server with its signal or RCON.

Every field is in the [API reference](https://studio.survivalservers.com/docs/reference/apps-builds-and-versions/).

## Start a game server

```bash
curl -X POST "https://studio.survivalservers.com/v0/game-servers" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d '{"app": "your-game", "version": "1.0.0", "placement": {"continents": ["europe"]}}'
```

The reply carries `address.ipv4` and `address.ports.game.port`: send your players there. The [quick start](https://studio.survivalservers.com/docs/get-started/quick-start/) has every call, from the upload to the stop.

## Optional: local API calls

Each optional call is plain HTTP to the [local API](https://studio.survivalservers.com/docs/game-server/local-api/) on the machine, at `GS_LOCAL_API_URL` with the token in `GS_TOKEN`. When `GS_LOCAL_API_URL` is empty (a run in the editor or on your own PC), skip the calls. This coroutine says ready; start it with `StartCoroutine(SayReady())` once the server can take players:

```csharp
using System;
using System.Collections;
using UnityEngine.Networking;

IEnumerator SayReady()
{
    var url = Environment.GetEnvironmentVariable("GS_LOCAL_API_URL");
    if (string.IsNullOrEmpty(url)) yield break;  // the editor or your own PC
    using (var req = new UnityWebRequest(url + "/v0/self/ready", "POST"))
    {
        req.SetRequestHeader("Authorization", "Bearer " + Environment.GetEnvironmentVariable("GS_TOKEN"));
        yield return req.SendWebRequest();  // 204: recorded
    }
}
```

This coroutine has never run in the Unity editor. The same call, made from plain .NET, passes our tests.

The local API is plain HTTP. Unity 6 refuses plain HTTP to any address but `localhost` and `127.0.0.1` unless **Allow downloads over HTTP** is set to **Always allowed** (Player Settings, Other Settings). On Windows the local API is at `127.0.0.1`, but on Linux it is at another address, so set it for a Linux build.

For the `local_api` ready check, put `"ready_check": { "type": "local_api" }` in the version. The other calls (the match, players, the shutdown notice) are on the [local API](https://studio.survivalservers.com/docs/game-server/local-api/) page.

## Optional: use the package

Add `unity/` from the [samples](https://github.com/survivalservers/studio-samples) to your project by path or git URL; it declares Unity 2021.3 or newer. `LocalApi` in the package makes every optional call to the [local API](https://studio.survivalservers.com/docs/game-server/local-api/), under the same names as our game server SDK. It uses Unity's non-blocking `UnityWebRequest`, so nothing native is added to your build. Without `GS_LOCAL_API_URL` (a run in the editor or on your own PC) every call does nothing.

The package has never been built in the Unity editor, and no Unity server has run with it. Its plain C# part is tested in .NET, and its Unity code is compiled against stand-ins for Unity's types. Test it in your own project before you rely on it.

| Call | What it does |
|---|---|
| `Ready()` | Ready for players (`local_api` check) |
| `WaitAllocation(wait)` | Waits up to 60 s for the match |
| `WaitShutdown(wait)` | Waits up to 60 s for a shutdown notice |
| `PlayerConnect(id)` | A player joined |
| `PlayerDisconnect(id)` | A player left |
| `SetPlayerCount(n)` | Player count, for games without IDs |
| `SetLabel(name, value)` | Sets a label your backend filters on |
| `RemoveLabel(name)` | Removes a label |
| `Tick(p50Ms, p99Ms, hz)` | Last second's frame times |
| `Health()` | Says it is still healthy, for `health_timeout` |
| `Stop(reason)` | Ends the match |

- `WaitAllocation` returns the match's ID, settings, labels and arguments, or null when there is none yet: call it again. A ready server waiting in a pool waits here for its match.
- `WaitShutdown` returns the shutdown notice's deadline and reason, so the server can save and leave in time.
- The package keeps the player list and sends it whole after each change.
- `Tick` once a second feeds the game server's tick graph.
- `Stop` counts the same as exiting with code 0.

```csharp
using SurvivalServers.Unity;

var cfg = GsConfig.FromEnvironment();
var api = new LocalApi(cfg.AgentUrl.Length > 0 ? new UnityWebRequestTransport(cfg.AgentUrl, cfg.Token) : null);
await api.Ready();
var match = await api.WaitAllocation(TimeSpan.FromSeconds(30));   // null: not yet, ask again
await api.PlayerConnect(playerId);
```

The package comes with a sample game: in the Package Manager, open the package's Samples and import it. Its `GameServerBehaviour` runs a small UDP game with every call. It says ready, waits for its match, reports players and stops when the match ends. Copy the calls you need from it.

## Next steps

- Run every call on your own PC first: [test on your own PC](https://studio.survivalservers.com/docs/game-server/test-locally/).
- Start a game server for each Unity Matchmaker match: [Unity Matchmaker](https://studio.survivalservers.com/docs/switching/unity-matchmaker/). That module has not run through a real queue yet.
- Every call on the machine: [the local API](https://studio.survivalservers.com/docs/game-server/local-api/).
