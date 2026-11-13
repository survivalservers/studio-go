# Environment variables and allocation

> The GS_* variables and the allocation file every game server gets: its ports, its address and its match's settings, with no SDK.

Every game server starts with the `GS_*` environment variables below (GS stands for game server). When a match is assigned to it, it also gets an allocation file with the match's settings. Your server reads both the way it reads any environment variable or file.

## The variables

| Variable | What it is |
|---|---|
| `GS_ID` | This game server's ID |
| `GS_KIND` | `match` or `persistent_server` |
| `GS_APP` | The app's slug |
| `GS_VERSION` | The version's name |
| `GS_REGION` | The region it runs in |
| `GS_CONTINENT` | That region's continent |
| `GS_PUBLIC_IP` | The address players use |
| `GS_PORT_<NAME>` | Public port of a named port |
| `GS_PORT_<NAME>_INTERNAL` | Linux: the port's `container_port` |
| `GS_CPU` | vCPUs enforced |
| `GS_MEMORY_MB` | Memory enforced, in MiB |
| `GS_LOCAL_API_URL` | The local API |
| `GS_TOKEN` | The local API's token |
| `GS_ALLOCATION_FILE` | The match's settings as a file |
| `GS_LOG_DIR` | Files here are kept as logs |
| `GS_ARTIFACT_DIR` | Files here are kept after exit |
| `TEMP`, `TMP` | Windows: its own temp folder |

A Linux match game server with one port named `game` sees values like these:

```bash
GS_ID=gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2
GS_KIND=match
GS_APP=breakwire-linux
GS_VERSION=2.3.0
GS_REGION=europe-west
GS_CONTINENT=europe
GS_PUBLIC_IP=203.0.113.24
GS_PORT_GAME=31042
GS_PORT_GAME_INTERNAL=7777
GS_CPU=2
GS_MEMORY_MB=4096
GS_LOCAL_API_URL=http://192.0.2.1:7600
GS_TOKEN=...
GS_ALLOCATION_FILE=/gs/allocation.json
GS_LOG_DIR=/gs/logs
GS_ARTIFACT_DIR=/gs/artifacts
```

- `GS_ID` is set when the game server starts, ready servers in a pool included. It does not change when a match takes it.
- `GS_PORT_<NAME>` uses the port's name from the version, upper-cased: the port `game` gives `GS_PORT_GAME`.
- `GS_PORT_<NAME>_INTERNAL` is set only in Linux images, for a port with a `container_port`. It is the number the public port is mapped onto. A [passthrough port](#passthrough-ports) has none.
- For an [admin port](#admin-ports), `GS_PORT_<NAME>` is the port on the machine that is never opened, not a public one.
- `GS_CPU` can be a fraction, such as `0.5`. On Windows, `GS_MEMORY_MB` is committed memory.
- `GS_LOCAL_API_URL` is the [local API](https://studio.survivalservers.com/docs/game-server/local-api/): `http://127.0.0.1:7600` on Windows, and the container network's gateway on port 7600 on Linux.
- `GS_TOKEN` works only for this run of this game server: a restart gives it a new one. Never log it.
- The examples are a Linux image's paths. On Windows the three paths are in a `.gs` folder inside the game server's own folder.
- On a persistent server with pause when empty on, `GS_PORT_<NAME>` and `{{port.<name>}}` give a private port to listen on. Players still connect to the server's public port. A Linux [passthrough port](#passthrough-ports) is the exception: it is always the public port.

## Your own variables

- The version's `env` is added to these variables, secret values included.
- On a fresh start, the start request's `env` is added too. A game server from a pool gets it only in the allocation data.
- Names are letters, digits and `_`, and do not start with a digit.
- Names that start with `GS_`, in any case, are ours. A version or start request that sets one is refused with `422 validation_failed`.
- Set the version's `env_aliases` to also get another provider's names: `hathora` (`HATHORA_*`), `gameye` (`GAMEYE_*`) or `edgegap` (`ARBITRIUM_*`).

## Which port to listen on

```csharp
var port = Environment.GetEnvironmentVariable("GS_PORT_GAME_INTERNAL")
    ?? Environment.GetEnvironmentVariable("GS_PORT_GAME")
    ?? "7777"; // your own computer
```

A Linux image listens on `GS_PORT_GAME_INTERNAL`, the `container_port` you gave the port. A Windows build listens on `GS_PORT_GAME`, and so does a Linux [passthrough port](#passthrough-ports), which has no internal variable. Reading the internal variable first works in every case.

Bind to all addresses (`0.0.0.0`), not to `127.0.0.1`, or players cannot reach the server.

## What reaches your game server

Your game's own ports take traffic from anywhere, UDP or TCP as each port declares, QUIC included. Keep each UDP packet under 1,200 bytes: a bigger one travels in pieces, and some networks on the way drop pieces.

## Admin ports

RCON and a web admin page should never be open to the internet. Make such a port an admin port with `"port_policy": "none"`:

```json
"ports": [
  {"name": "game", "protocol": "udp"},
  {"name": "rcon", "protocol": "tcp", "port_policy": "none"}
]
```

- An admin port gets no public port. It is never in the game server's address, and the machine never opens it to the internet.
- Only the game server's own machine reaches it. The panel's console and players card, and RCON or HTTP stop and save commands, still work, because they run on the machine.
- Other studios' game servers on the same machine are kept off it too.
- Windows: the game listens on `GS_PORT_RCON`, or `{{port.rcon}}` in its arguments. It is a number from the machine's game port range that is never opened.
- Linux: the game listens on the port's `container_port` inside its own container, and nothing on the machine forwards to it.
- The default, `"port_policy": "dynamic"`, is a public port. Agones uses the same names: `portPolicy: None` is a port with no public port.
- Give an admin port a password too, kept as a secret value in `env`.

## Passthrough ports

On Linux, a public port usually has a `container_port`. The image listens on that fixed number, and the machine maps each game server's public port onto it. The game never needs to know its public port.

Some games tell a server list which ports they listen on. Steam's server browser is one: a game that uses Steam's game server API tells Steam its own game and query ports. With a fixed `container_port`, it tells Steam the number inside its container, and players' server browsers list a port nobody can reach.

For such a game, make the port a passthrough port. Inside the container, the game then listens on the public port's own number:

```json
"args": ["+server.port", "{{port.game}}", "+server.queryport", "{{port.query}}"],
"ports": [
  {"name": "game", "protocol": "udp", "port_policy": "passthrough"},
  {"name": "query", "protocol": "udp", "port_policy": "passthrough"},
  {"name": "rcon", "protocol": "tcp", "container_port": 28016, "port_policy": "none"}
]
```

- A passthrough port has no `container_port`. A version that gives it one is refused with `422 validation_failed`.
- The number is different for each game server, as on Windows. Pass it with `{{port.<name>}}`, or read `GS_PORT_<NAME>`.
- `GS_PORT_<NAME>_INTERNAL` is not set for it. `{{port.<name>.internal}}` gives the public number.
- On a persistent server with pause when empty on, it is the public number too.
- It is only for Linux images. A Windows build already listens on its public port.
- Admin ports keep their `container_port`, and the other ports of the version can keep theirs.
- Agones uses the same name: `portPolicy: Passthrough`.

| Your game | Give the port |
|---|---|
| Listens on a fixed port, never reports it | A `container_port` |
| Tells Steam's server list its own port | `"port_policy": "passthrough"` |
| RCON or a web admin page | `"port_policy": "none"` |

## Launch argument templates

Put templates in the version's `args` and the API fills them in at every start:

```json
"args": ["-log", "-Port={{port.game}}", "-QueryPort={{port.query}}", "-ServerId={{id}}"]
```

| Template | Becomes |
|---|---|
| `{{port.<name>}}` | The public port of that named port |
| `{{port.<name>.internal}}` | Linux: its `container_port` |
| `{{public_ip}}` | The address players use |
| `{{id}}` | The game server's ID |
| `{{region}}` | The region's ID |
| `{{cpu}}` | vCPUs enforced |
| `{{memory_mb}}` | Memory enforced, in MiB |
| `{{env.<NAME>}}` | One of the version's `env` values |

- `{{env.<NAME>}}` takes a secret value too. The machine fills it in as the game starts.
- Templates are filled in `args` and `files`, not in `env`.
- A template that is not in this table, or names a port or `env` value the version does not declare, is refused with `422 validation_failed`.
- `{{port.<name>.internal}}` in a Windows version is refused the same way. For a [passthrough port](#passthrough-ports) it is the public port.
- On a fresh start, the start request's `args` come after the version's and take the same templates. There `{{env.<NAME>}}` may name only the start's own `env` values.

## Admin passwords and files

Many dedicated servers read their admin or RCON password from their command line or from a config file of their own. Let us make a new password for each game server, and put it where the game reads it with `{{env.<NAME>}}`:

```json
"launch": {
  "executable": "WindowsServer/BreakwireServer.exe",
  "args": ["-log", "-Port={{port.game}}"],
  "env": { "RCON_PASSWORD": { "secret": true, "generate_value": true } },
  "files": [
    {
      "path": "WindowsServer/BreakwireServer/Saved/Config/Rcon.ini",
      "content": "[Rcon]\nPort={{port.rcon}}\nPassword={{env.RCON_PASSWORD}}\n"
    }
  ],
  "ports": [{ "name": "game", "protocol": "udp" }, { "name": "rcon", "protocol": "tcp", "port_policy": "none" }]
}
```

- `generate_value` makes a new password for each game server as it starts: 32 letters and digits, given to that game server alone. A persistent server gets a new one at every run.
- The password is never stored and never shown: it reads back as `***`. Our agent keeps it in memory on the game server's machine, for the panel's console and players card and for RCON stop and save commands.
- If our agent restarts while the game runs, it no longer holds the password. Until the next run those RCON commands cannot sign in, and a stop falls back to the stop signal. A `secret` value you set yourself behaves the same way.
- A fixed password works too: `{ "value": "...", "secret": true }`. Every game server of the version then shares it.
- The `rcon` port is an admin port (`port_policy` `none`): only the game server's own machine reaches it.
- The machine fills in `{{env.<NAME>}}` as the game starts. The version, the API's answers, the panel and events only ever show the template.
- Put a secret in a launch file (`files`), not in the arguments. Many games print their own command line in their log, Unreal servers included, and that log keeps whatever is on it, password too.
- `files` are written before every start and replace what the build has at that path. Each `content` takes the same templates as `args`.
- Windows: `path` is relative to the build's top folder. The file is in the game server's own folder, which only your studio's account can read. It is removed when the game server's run ends.
- Linux: `path` is an absolute path inside the container. The file is kept in memory and mounted there, so it never touches the machine's disk. Your game can read and rewrite it.
- Up to 16 files, and 512 KiB in all. A file inside a persistent server's save paths is refused: its snapshots would keep it.
- The panel's file browser does not show these files, and they are never kept as logs or artifacts, whatever `log_paths` and `artifact_paths` say.
- Linux images need `init` on for a secret value in an argument. The init puts it into the command line as the game starts, so it never sits in the container's settings. A plain value works with `init` off too.
- A start request's `env` can replace a value the version's templates use. In its own `args`, `{{env.<NAME>}}` names only its own `env` values.

## The allocation data

```json
{
  "game_server_id": "gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2",
  "allocated_at": "2026-10-09T18:22:04.560Z",
  "external_id": "88231",
  "labels": { "match_id": "88231", "queue": "ranked" },
  "env": { "MATCH_MODE": "ranked" },
  "args": ["-MaxPlayers=10"],
  "expires_at": "2026-10-09T20:22:04.560Z"
}
```

- `external_id`: your own ID from the start request. It is left out when you set none.
- `labels`, `env`, `args`: from the start request.
- `expires_at`: when the time limit stops the game server, or `null` without one.

The same JSON comes two ways:

- **The file** at `GS_ALLOCATION_FILE`. It is written whole at once, so a reader never sees half of it. On a fresh start it is there before your program starts; on a game server from a pool it appears at allocation. It never holds secret values.
- **The long-poll** `GET /v0/self/allocation?wait=30s` on the local API. It answers once the game server is allocated, at once on a fresh start. Its `env` includes secret values.

A game server from a pool waits for the file or the long-poll before it sets up the match: see [ready servers](https://studio.survivalservers.com/docs/concepts/ready-servers/). A game server that needs no match settings can ignore both.

## Tested engines

Nothing on this page depends on the engine. These engines have run here as hosted game servers (October and November 2026), on Linux and Windows unless noted:

- Node.js 26 with Colyseus 0.18
- Defold 1.14, headless
- Flax 1.13
- O3DE 26.05
- Godot 4.7.2, and 4.2.2 on Linux
- Bevy 0.20
- Unreal 5.8
- .NET 8, in a Godot .NET game
- Java 25, with Minecraft

Unity is proven only with shipped Unity servers: Valheim and Unturned on Linux and Windows, and V Rising and Rust on Windows.

Our [Unity package](https://studio.survivalservers.com/docs/get-started/unity/) has never been built in the Unity editor.
