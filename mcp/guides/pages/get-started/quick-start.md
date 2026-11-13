# Quick start

> Start your first game server from the command line with plain HTTPS calls, on Windows or Linux, for any engine.

This guide takes your own server build to a running game server, then stops it, with `curl` and nothing else installed. It works the same for any engine.

This guide builds the plain sample, runs it as a game server, and then stops it. The sample is a small Go server that uses no SDK. Your own server, in any engine, does the same things in its own language.

## Prerequisites

- An account in the [panel](https://studio.survivalservers.com/app). If you have none, [sign up](https://studio.survivalservers.com/app/signup) with your email.
- `curl` and `uuidgen`.
- Your server build: a Windows zip, or a Linux image in a registry. It reads its port from `GS_PORT_GAME` (Windows) or listens on a fixed port inside the image (Linux).
- To build and run the plain sample: Go 1.22 or newer and `zip`; `bash`, `curl`, `jq` and `sha256sum` (or `shasum`) for the quick-start script; `nc` (netcat) to talk to the sample; and for a Linux game server, Docker.

## 1. Get an API key

In the panel, open **Developers**, then **API keys**, and choose **New key**. Pick **Custom** and tick these six:

| Tick | Permission |
|---|---|
| See apps and versions | `apps:read` |
| Change apps and versions | `apps:write` |
| Upload builds | `builds:write` |
| See game servers | `game_servers:read` |
| Start and stop game servers | `game_servers:write` |
| Read logs and files | `logs:read` |

The key is shown once and can't be seen again. Keep it in an environment variable, not in a file you commit:

```bash
export API_KEY=gsk_live_...
```

API keys are for your backend or build pipeline. Never put a key in your game build: players can extract it. Game servers need no key. [API keys and roles](https://studio.survivalservers.com/docs/account/api-keys-and-roles/) shows which parts of your game hold one.

## 2. Create the app

An app is one game on one system, `windows` or `linux`, and one tier:

```bash
curl -X POST "https://studio.survivalservers.com/v0/apps" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d '{"name": "my-game", "slug": "my-game", "os": "linux", "tier": "high-frequency", "default_placement": {"continents": ["europe"]}}'
```

- `tier`: pick one a region sells on your system. Today that is `high-frequency` on Linux and `standard` on Windows.
- With no `tier`, an app is `standard`. `GET /v0/regions` lists what each region sells in `offers`.
- `Idempotency-Key`: a new one for each write. A retry with the same key gets the first answer for 24 hours, so a corrected request needs a new key.

## 3. Upload the build

A Linux image is one call. Give its full name with a version tag:

```bash
curl -X POST "https://studio.survivalservers.com/v0/apps/my-game/builds" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d '{"os": "linux", "source": {"type": "image", "image": "registry.example.com/you/my-game:1.0.0"}}'
```

A Windows zip goes up in parts: [upload builds](https://studio.survivalservers.com/docs/guides/upload-builds/) has the three calls, and the login for a private registry. Either way, the reply has the build's `id`. Read it until `status` is `ready`:

```bash
curl "https://studio.survivalservers.com/v0/builds/bld_..." -H "Authorization: Bearer $API_KEY"
```

A build that ends `failed` says why in `failure.code` and `failure.message`.

## 4. Create the version

A version is the build plus how to run it. Save this as `version.json`, with your build's ID:

```json
{
  "name": "1.0.0",
  "build": "bld_...",
  "size": { "cpu": 1, "memory_gb": 1 },
  "launch": {
    "ports": [{ "name": "game", "protocol": "udp", "container_port": 7777 }],
    "ready_check": { "type": "port", "port": "game" },
    "stop": { "method": "sigterm", "shutdown_time": "10s" }
  },
  "restart": { "policy": "never" },
  "max_duration": "2h",
  "regions": ["*"]
}
```

- `ports`: one UDP port named `game`. Each start gets a fresh public port, mapped to `container_port` inside the image.
- `ready_check`: `port` counts the game server as joinable once it has bound its game port. A server that loads its world after it binds the port should use a `log` check on a line it prints when ready.
- `stop`: SIGTERM, then 10 seconds to exit before the process is ended.
- `restart` and `max_duration`: no relaunch after a crash, and a stop after 2 hours at most.
- `regions`: `["*"]` copies the version to every region that offers the app's tier and system.

For a Windows zip, add `"executable"` (the program inside the zip) and `"args"`, leave out `container_port`, and stop with `ctrl_break`. Your server then reads its port from `GS_PORT_GAME`. [Windows builds](https://studio.survivalservers.com/docs/game-server/windows-builds/) has the details.

```bash
curl -X POST "https://studio.survivalservers.com/v0/apps/my-game/versions" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d @version.json
```

If no region sells your app's tier on its system, the call answers `422` on `regions` and says which. The version is copied ahead to the regions. Read it until one entry in `regions` has `status` `ready`:

```bash
curl "https://studio.survivalservers.com/v0/apps/my-game/versions/1.0.0" -H "Authorization: Bearer $API_KEY"
```

## 5. Start a game server

```bash
START_KEY=$(uuidgen)
curl -X POST "https://studio.survivalservers.com/v0/game-servers" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $START_KEY" -H "Content-Type: application/json" \
  -d '{"app": "my-game", "version": "1.0.0", "placement": {"continents": ["europe"]}}'
```

The reply has the game server's `id` and the address players join: `address.ipv4` and `address.ports.game.port`. The address is final at once.

A region can read ready a moment before it takes starts. If the start answers `409` with `version_not_ready`, send the same `curl` again with the same `$START_KEY`, so it can never start two.

## Or use the samples' script

Clone the [samples](https://github.com/survivalservers/studio-samples) and build the plain server:

```bash
cd plain
./build.sh
```

`build.sh` makes two files:

- `dist/plain-server-windows.zip`: the Windows build. Its executable is `PlainServer.exe`.
- `dist/linux/plain-server`: the Linux program. With Docker installed, `build.sh` also builds the image `plain-server:dev`.

On a Linux computer, run it in one terminal and talk to it from another:

```bash
GS_PORT_GAME=7777 ./dist/linux/plain-server
echo ping | nc -u -w1 127.0.0.1 7777
```

```text
pong
```

For a Linux game server, push the image to your registry:

```bash
docker tag plain-server:dev registry.example.com/you/plain-server:1.0.0
docker push registry.example.com/you/plain-server:1.0.0
```

The script sends no registry login. For a private registry, or to push to ours, see [upload builds](https://studio.survivalservers.com/docs/guides/upload-builds/).

From the top of the samples, give `quickstart.sh` the system, the build and the version settings. It calls the API at `API`:

```bash
export API=https://studio.survivalservers.com

# Windows: the zip build.sh made
quickstart/quickstart.sh windows plain/dist/plain-server-windows.zip plain/version-windows.json

# Linux: the image you pushed
quickstart/quickstart.sh linux registry.example.com/you/plain-server:1.0.0 plain/version-linux.json
```

The script makes the app `plain-server`, uploads the build, creates the version and waits until a region has it. Then it starts a game server and prints the address:

```text
1. App plain-server (windows)
2. Build from plain/dist/plain-server-windows.zip
   part 1 uploaded
   bld_...
3. Version
   1.0.0
4. Game server
   gs_... in europe-west, starting

Players connect to 203.0.113.24:31042
```

If the start answers `version_not_ready`, the script prints `waiting for the image to reach the region` and tries the same start again, with the same `Idempotency-Key`, for up to a minute. Any other refusal stops the script with the API's code and reason.

Set `APP` to change the app's name, `CONTINENT` to start elsewhere and `TIER` to pick another tier. It starts in `europe`, where a region is open today, on the tier that region sells for the system.

The sample's Windows settings, `plain/version-windows.json`, have `"executable": "PlainServer.exe"`, a `local_api` ready check (the sample says it is ready with a call once it listens) and a Ctrl+Break stop. Send the sample `ping` and it answers `pong`.

## 6. Connect to it

Point your game client at the address. Read the game server's state and follow its console:

```bash
curl "https://studio.survivalservers.com/v0/game-servers/gs_..." -H "Authorization: Bearer $API_KEY"
curl "https://studio.survivalservers.com/v0/game-servers/gs_.../logs/stream?follow=true" -H "Authorization: Bearer $API_KEY"
```

`status` turns from `starting` to `running` once the ready check passes. The panel shows the same console, the players and the timeline.

## 7. Stop it

```bash
curl -X POST "https://studio.survivalservers.com/v0/game-servers/gs_.../stop" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Idempotency-Key: $(uuidgen)"
```

The reply is `202` with `"status": "stopping"`. Its console stays readable for 90 days.

The sample writes `match-result.json` to `GS_ARTIFACT_DIR` and exits with code 0. It also ends a match by itself: send `join sam`, then `leave sam`, and it stops once the last player has left.

Game servers are billed per second while they run, and the first 2,000 vCPU-hours each month are free.

## Next steps

- Your own engine: [Unreal](https://studio.survivalservers.com/docs/get-started/unreal/), [Unity](https://studio.survivalservers.com/docs/get-started/unity/), [Godot](https://studio.survivalservers.com/docs/get-started/godot/), or any other with [Environment variables and allocation](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/).
- Upload from your CI: [upload builds](https://studio.survivalservers.com/docs/guides/upload-builds/#from-your-ci).
- Let your AI coding tool run these steps: [Build with AI](https://studio.survivalservers.com/docs/guides/build-with-ai/).
- Start a game server for every match: [start game servers from your backend](https://studio.survivalservers.com/docs/guides/start-matches-from-your-backend/).
