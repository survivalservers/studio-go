# Apps, builds, versions

> How a server program becomes something you can start: an app holds builds, a version says how to run one, and a branch points at a version.

An app holds builds, a version says how to run one build, and a branch points at a version. Your backend starts game servers from a branch, so you can ship a new build without changing how matches start.

```json
GET /v0/apps/breakwire-windows
{
  "id": "app_a01JA7ZQ4W8E2R6T0Y4U8I2O6P",
  "name": "Breakwire Server (Windows)",
  "slug": "breakwire-windows",
  "os": "windows",
  "tier": "standard",
  "default_placement": { "continents": ["north-america", "europe"] },
  "branches": { "live": "2.3.0", "public-test": "2.4.0-beta.2" }
}
```

## App

An app is one game server program on one system: `windows` (zip builds) or `linux` (image builds). The system never changes, so a game with a Windows and a Linux game server has two apps.

- `slug`: the short name every call takes in place of the ID.
- `tier`: `standard` (the default) or `high-frequency`. A version can set its own. See [tiers and sizes](https://studio.survivalservers.com/docs/concepts/tiers-and-sizes/).
- `default_placement`: the regions and continents used by starts that send no `placement`.
- `branches`: each branch and the version it points at.

## Build

A build is your game server's files: a zip of its folder for a Windows app, or a container image for a Linux app. See [upload builds](https://studio.survivalservers.com/docs/guides/upload-builds/).

- A zip is checked after upload: its SHA-256, and every path in it (safe, used once, a plain file or folder). The programs it holds are listed in `executables`.
- A zip's files are cut into chunks, stored once for your studio. A new build shares the chunks that did not change with your earlier builds, and machines fetch only the new ones.
- Chunks are never shared between studios. Another studio's build with the same files gets its own copy, in our store and on every machine, so nothing about your builds shows in theirs.
- Each file keeps the date it has in the zip.
- An image is copied into our registry and pinned by digest, so your own registry being down never blocks a start. Your registry must be on the public internet: one on a loopback, private or link-local address fails the build with `registry_denied`.
- A zip's programs, libraries and scripts are scanned for malware before it is `ready`. `scan.status` is then `clean` or `flagged`, or `pending` when nothing could be scanned (below).
- A flagged program fails the build with `scan_flagged`, naming the file, and our staff review it.
- Linux images are not scanned, so their `scan.status` stays `pending`. So does a zip whose program is over 1 GiB.
- A build that fails carries `failure.code`, such as `checksum_mismatch`, `bad_archive` or `image_not_found`.
- By default a zip may be up to 30 GB and an image up to 40 GB. `GET /v0/studio` shows your studio's limits, raised [on request](https://studio.survivalservers.com/docs/help/get-help/).

## Version

```json
POST /v0/apps/breakwire-windows/versions
{
  "name": "2.3.0",
  "build": "bld_a01JA80M1N3B5V7C9X1Z3L5K7J",
  "size": { "cpu": 2, "memory_gb": 4 },
  "launch": {
    "executable": "WindowsServer/BreakwireServer.exe",
    "args": ["-log", "-Port={{port.game}}", "-ServerId={{id}}"],
    "ports": [{ "name": "game", "protocol": "udp" }],
    "ready_check": { "type": "log", "pattern": "Server ready for players" },
    "stop": { "method": "ctrl_break", "shutdown_time": "30s" }
  },
  "max_duration": "6h",
  "regions": ["*"]
}
```

A version is a build plus how to run it. After it is made, only `deprecated`, `regions` and its [game profile](https://studio.survivalservers.com/docs/concepts/game-profiles/) can change, so a name such as `2.3.0` always means the same thing.

- `launch`: the program, its arguments and environment, named ports, ready check, stop and save methods, and the logs to keep.
- A port with `"port_policy": "none"` is an admin port, for RCON or a web admin page: never open to the internet, reached only from its machine. See [admin ports](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/#admin-ports).
- On Linux, a port with `"port_policy": "passthrough"` has no `container_port`: the game listens on its public port's own number, for a game that tells Steam's server list its port. See [passthrough ports](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/#passthrough-ports).
- `launch.ready_check` says when it is joinable: `local_api`, `log`, `port`, `delay` or `none`.
- A game whose port opens before its world loads needs a `log` check, such as V Rising's `Startup Completed` or Unturned's `Loading level: 100%`. A `port` check calls it ready while it still loads, and a stop then saves nothing.
- `launch.stop.method` is `ctrl_break` by default on Windows and `sigterm` on Linux. Its `shutdown_time` is 30 seconds by default.
- Arguments take templates filled in at every start, such as `{{port.game}}` and `{{public_ip}}`. The full list is on [Environment variables and allocation](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/).
- `size`: vCPUs and memory. A start can ask for another size.
- `max_duration`: an optional hard limit on every match of this version. There is none by default.
- `restart`: what happens when it crashes. See [game servers](https://studio.survivalservers.com/docs/concepts/game-servers/).
- `regions`: where to copy it ahead of time. `["*"]`, the default, is every region that offers the app's tier and system.
- An `env` value sent as `{"value": "...", "secret": true}` is stored encrypted, kept out of our own logs, and read back as `***`. A game that prints its own command line logs what is on it, so give secrets to the game in a [launch file](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/#admin-passwords-and-files), not in its arguments.
- `{"secret": true, "generate_value": true}` makes a new random value for each game server, such as its own RCON password. It is never stored.
- `{{env.<NAME>}}` puts an `env` value, a secret one too, into an argument or a `launch.files` file. The machine fills it in as the game starts. See [admin passwords and files](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/#admin-passwords-and-files).
- A password written out in an argument, such as `-password hunter2`, `+rcon.password hunter2` or `?ServerPassword=hunter2`, is refused with `422`, naming the argument: put the password in a secret `env` value and write `{{env.<NAME>}}` in its place.
- `env` names starting with `GS_` are refused with `422`: the machine agent sets those.
- An app keeps up to 200 versions by default, deleted ones not counted. One more answers `403 limit_reached`.

## Branch

```bash
curl -X PUT "https://studio.survivalservers.com/v0/apps/breakwire-windows/branches/live" \
  -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" \
  -d '{"version": "2.3.0"}'
```

```json
{ "live": "2.3.0", "public-test": "2.4.0-beta.2" }
```

A branch is a name, such as `live` or `public-test`, that points at one version. A start that names no version or branch uses `live`.

- To update, put the new version on the branch. To roll back, put the old one back. See [roll out and roll back](https://studio.survivalservers.com/docs/guides/rollouts-and-rollbacks/).
- New starts on the branch take the new version. Game servers already running keep the version they started on.
- A pool on the branch keeps its ready servers of the old version until as many of the new one are ready.
- A persistent server that follows the branch takes the new version at its next restart, or at once when stopped.
- A branch cannot point at a deprecated version: the call answers `409 invalid_state`.

## Status in each region

```json
"regions": [
  { "region": "europe-west", "status": "ready", "ready_at": "2026-10-09T17:31:12.004Z" },
  { "region": "europe-central", "status": "copying" }
]
```

A new version is copied ahead of time to each of its regions. Each region reports `pending`, `copying`, `ready` or `failed`, or `removed` once the machines there no longer keep it (below).

- A region is `ready` once every machine there that can run the version has it, or at least two do.
- `version.region_ready` fires once per version and region, the first time that region is ready.
- Starts go only to machines that already have the version. A region still `copying` takes starts on the machines that have it.
- When no region a start may use has it yet, the start answers `409 version_not_ready`. Wait, or allow more regions.

## What stays on the machines

Machines keep copies of these versions of each app:

- the three newest versions that are not deprecated;
- any version a branch points at or a pool is pinned to;
- any version a running game server uses, or a persistent server uses or will take next.

Every other version is removed from the machines, and its regions read `removed`. A start that names it answers `409 version_not_ready`, with `version_removed` in `regions_tried`. Put it on a branch and it is copied again, as a new version is.

`version.region_ready` does not fire a second time for it. Read its `regions` with `GET /v0/apps/{app}/versions/{version}` before you send matches to it.
`survivalservers versions get APP VERSION --wait` returns once one region is ready.
