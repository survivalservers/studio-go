# Linux images

> Run your existing container image as it is: how to bring it, which ports to name, how it runs and stops, and where its logs go.

A Linux game server is a container image, the same kind of image you run with Docker. Any image that runs your server headless works, including images you built for another provider.

## Bring the image

Push it to our registry, then create a build that names it:

```bash
login=$(curl -s -X POST https://studio.survivalservers.com/v0/registry/token -H "Authorization: Bearer $API_KEY")
repo=$(echo "$login" | jq -r .repository)
echo "$login" | jq -r .password | docker login "$(echo "$login" | jq -r .registry)" \
  -u "$(echo "$login" | jq -r .username)" --password-stdin
docker build --platform linux/amd64 -t "$repo/breakwire-server:2.3.0" .
docker push "$repo/breakwire-server:2.3.0"

curl -X POST https://studio.survivalservers.com/v0/apps/breakwire-linux/builds \
  -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" \
  -d "{\"os\": \"linux\", \"source\": {\"type\": \"image\", \"image\": \"$repo/breakwire-server:2.3.0\"}}"
```

- `POST /v0/registry/token` returns a login good for one hour, or up to 6 hours while a push is still uploading with it. `repository` is your studio's own space in our registry; push under it.
- The login can push only to your own repositories, and it never deletes anything.
- To copy an image from your own registry instead, name it in the build: `"image": "registry.kingsmoor.example/breakwire-server:2.3.0"`.
- For a private registry, save its login once with `POST /v0/registry-credentials` and pass its ID as `registry_credential` in the build's `source`.
- Either way the image is copied into our registry and pinned by its digest. An outage of your registry never stops a start.
- Use a version tag, never `latest`.
- Build for `linux/amd64`. An ARM image, such as one built on an Apple silicon Mac without `--platform`, does not start.
- Images can be up to your studio's limit, 40 GB by default. A larger one fails the build with `too_large`; the limit is raised [on request](https://studio.survivalservers.com/docs/help/get-help/).

## Ports

Give every port in the version the port your server listens on inside the image, `container_port`. A Linux version without it is refused. A fresh public port is mapped onto it at every start:

```json
"ports": [{ "name": "game", "protocol": "udp", "container_port": 7777 }]
```

Inside the container, `GS_PORT_GAME_INTERNAL` is that container port and `GS_PORT_GAME` the public one, for a server that tells players its address. Listen on `0.0.0.0`, not on `127.0.0.1`.

## How it runs

- **Ubuntu 24.04 LTS** machines, with each game server in its own container.
- **Caps.** vCPUs and memory are capped at the version's size (`GS_CPU`, `GS_MEMORY_MB`), with no swap beyond it.
- **Its own network**, with only its own ports. No privileged containers and no host networking.
- **Its own user IDs.** Root inside the container is not root on the machine.
- **Its own copy of the image's files** to write in, plus `/gs` for the allocation file, `GS_LOG_DIR` and `GS_ARTIFACT_DIR`.
- **Files lists.** The [Files](https://studio.survivalservers.com/docs/guides/files/) list uses the container's own paths and leaves out what the machine adds, such as `/dev`, `/proc`, `/sys`, `/run` and `/gs`.
- **Name resolution.** The machine agent gives each container a read-only `/etc/resolv.conf` and `/etc/hosts`.
- **Certificates.** The machine adds no CA certificates. A server that makes HTTPS calls needs them in its image, such as the `ca-certificates` package on Debian or Ubuntu.
- **Outbound limits.** A container cannot reach private networks, cloud metadata addresses or our own services, or send mail on port 25.
- `executable`, when set, replaces the image's entrypoint.

Unreal game servers refuse to run as root. Set a user in the image, such as `USER 1000`; the folders the game writes belong to the image's user.

## Ready check

```json
"ready_check": { "type": "log", "pattern": "Loading level: 100%" }
```

- `type` is `local_api` (a ready call on the local API), `log` (a line on the console), `port` (a bound port), `delay` (with `after`) or `none`.
- On Linux a `log` check reads the console, so have the server print the line there.
- A server whose port opens before its world loads needs a `log` check. A `port` check would call it ready too early, and a stop then saves nothing.
- In November 2026, Unturned answered on its game port while its level was still loading. Its ready check waits for `Loading level: 100%`.
- No readiness within `boot_timeout` (5 minutes by default) fails the start.

## The init process

Every game server starts under a small open-source init, which runs as the container's process 1. A server that runs as process 1 itself ignores any stop signal it has no handler for. In October 2026, Source and Terraria servers ignored stop signals that way.

- It forwards stop signals to the server's whole process group.
- When the server's own process ends while others of its group still run, it waits up to 30 seconds for them. A launch script that starts the real server, as Unreal's does, is one such case.
- It reaps orphaned processes and exits with the server's exit code (128 plus the signal's number when a signal ended it).
- It gives the server a terminal as its standard input, as in a shell. Console commands, such as a console stop or save command, are typed into it. Source servers on Linux read commands only from a terminal.
- Turn it off with `"init": false` only for an image that must be process 1 itself, such as s6-overlay. Console commands then cannot be sent.

## How it stops

When a game server is stopped, the version's `save` command runs first, when it has one. Then the shutdown notice goes out on the [local API](https://studio.survivalservers.com/docs/game-server/local-api/), and the version's `stop.method` is used:

| `stop.method` | What the server gets |
|---|---|
| `sigterm` (default) | `SIGTERM` |
| `sigint` | `SIGINT` |
| `command` | A console line, an RCON command or an HTTP call |
| `local_api` | The shutdown notice only |
| `kill` | Ended at once |

- A server that has asked for the shutdown notice (`GET /v0/self/shutdown`) gets the notice first. Its stop signal follows only if it still runs halfway through the shutdown time.
- A server that never asks for the notice gets the notice and the signal at once.
- In November 2026, an Unreal 5.8 server that asked for the notice stopped this way with no signal: exit code 0 after 3.8 seconds, its whole log kept.
- `local_api` suits a server that waits for the shutdown notice instead of a signal.
- A `command` that cannot be delivered falls back to `SIGTERM`, and the timeline says `stop_command_failed`.
- After `stop.shutdown_time` (30 seconds by default), the container is ended.
- Exiting on its own with code 0 is recorded as `self`; any other code is `crashed`. After a stop request, any exit keeps the stop's reason.

## Images from other providers

An image made for Gameye, Edgegap or Hathora that listens on a fixed port needs only that port as `container_port`. For a server that reads another provider's variable names, set the version's `env_aliases` to `["hathora"]`, `["gameye"]` or `["edgegap"]`. Those names are then set beside ours. The [switching guides](https://studio.survivalservers.com/docs/switching/from-hathora/) cover the rest.

## Logs and crash dumps

- The console (stdout and stderr) is the log, kept for 90 days.
- Files under `log_paths`, and anything written to `GS_LOG_DIR`, are kept with it.
- A crash leaves a core dump, kept with the game server, encrypted, for 72 hours unless the studio sets otherwise, and unless `crash_dump` is `off`. The crash's stack trace is kept with the logs.
- `GS_ARTIFACT_DIR` and `artifact_paths` are collected when the game server exits.
