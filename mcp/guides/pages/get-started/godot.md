# Godot

> Run a Godot headless server as a Linux image or a Windows zip, with optional calls to the local API.

A Godot dedicated server needs nothing extra to run here. Godot's `--headless` mode runs your project as a server, and the server reads its port from an environment variable. For the optional calls (the match, players, shutdown notices), make plain HTTP calls with `HTTPRequest`.

Or add the Survival Servers Studio add-on. The [samples](https://github.com/survivalservers/studio-samples) include both: the add-on with an example project in `godot-addon/`, and a single-file GDScript server with no add-on in `godot/`.

In October 2026, a GDScript server built this way ran here on Linux with Godot 4.7.2 and 4.2.2, and on Windows with 4.7.2. Players joined it over the internet. A C# build (Godot 4.7.2 .NET) ran on both systems.

## Build the server

**Linux image.** This `Dockerfile` runs Godot's own headless program on the project, on a slim Debian base:

```dockerfile
FROM debian:bookworm-slim
ARG GODOT_VERSION=4.7.2
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl unzip \
 && curl -fsSL -o /tmp/godot.zip "https://github.com/godotengine/godot/releases/download/${GODOT_VERSION}-stable/Godot_v${GODOT_VERSION}-stable_linux.x86_64.zip" \
 && unzip -q /tmp/godot.zip -d /tmp/godot \
 && mv /tmp/godot/Godot_v${GODOT_VERSION}-stable_linux.x86_64 /usr/local/bin/godot \
 && rm -rf /tmp/godot /tmp/godot.zip \
 && apt-get purge -y curl unzip && apt-get autoremove -y && rm -rf /var/lib/apt/lists/*
COPY project.godot server.tscn server.gd /game/
RUN godot --headless --path /game --import
ENV HOME=/tmp
USER nobody
EXPOSE 7777/udp
ENTRYPOINT ["godot", "--headless", "--path", "/game"]
```

The `--import` line builds Godot's cache while the image is built, so a start does not spend time on it. You can also export with a **Dedicated Server** export preset and copy the exported files in instead.

**Windows zip.** Zip Godot's Windows console program with your project files beside it, and start it with `--headless --path .`; this is what ran here. Or export with a Windows dedicated server preset, zip the export folder, and name the exported program as the version's `executable`.

Give a Windows server a frame cap: `--max-fps 30` in the arguments, or `Engine.max_fps`. In October 2026, an uncapped Godot 4.7.2 on Windows ran at about 145 frames a second with nobody playing. It used 10 to 15% of a core. Capped at 30 it used about 1%, at 60 about 4%. On Linux, uncapped, the same build stayed near zero.

## Listen on the right port

Read the port from the environment, the container port first for Linux images, and bind to every address:

```gdscript
func _listen_port() -> int:
	for name in ["GS_PORT_GAME_INTERNAL", "GS_PORT_GAME"]:
		var value := OS.get_environment(name)
		if value.is_valid_int() and int(value) > 0:
			return int(value)
	return 7777  # your own computer

func _ready() -> void:
	var peer := ENetMultiplayerPeer.new()
	peer.create_server(_listen_port())
	multiplayer.multiplayer_peer = peer
	print("Server ready for players")
```

## Create a version

For the Linux image, give the port the image listens on, and wait for the line the server prints. A `version-linux.json` for it:

```json
{
  "name": "1.0.0",
  "build": "BUILD_ID",
  "size": { "cpu": 1, "memory_gb": 1 },
  "launch": {
    "ports": [{ "name": "game", "protocol": "udp", "container_port": 7777 }],
    "ready_check": { "type": "log", "pattern": "Server ready for players" }
  },
  "restart": { "policy": "never" },
  "max_duration": "2h",
  "regions": ["*"]
}
```

For the Windows zip, add the program and its arguments:

```json
"executable": "Godot_v4.7.2-stable_win64_console.exe",
"args": ["--headless", "--max-fps", "30", "--path", "."],
"ports": [{ "name": "game", "protocol": "udp" }]
```

- **Ready check.** `log` waits for the line your server prints once it can take players. A `port` check fits only a server that binds its port at that moment. If yours binds first and loads its data after, keep the log check.
- **Stop.** With no `stop`, Linux sends SIGTERM and Windows Ctrl+Break, and the process is ended after 30 seconds. Godot does not catch SIGTERM, so on Linux it ends at once and saves nothing.

Once your server makes the [optional calls](#optional-httprequest-calls), it says ready itself, and on a stop it waits for the shutdown notice, saves and quits. Then use these in `launch`:

```json
"ready_check": { "type": "local_api" },
"stop": { "method": "local_api", "shutdown_time": "10s" }
```

## Optional: use the add-on

The add-on has been tested with Godot 4.7.2 headless, but not yet on our machines. It is not on the Asset Library yet. Copy `addons/game_server` from `godot-addon/` in the samples into your project, then turn **Survival Servers Studio** on under **Project > Project Settings > Plugins**. That adds the `GameServer` autoload:

```gdscript
func _ready() -> void:
	var peer := ENetMultiplayerPeer.new()
	peer.create_server(GameServer.port())
	multiplayer.multiplayer_peer = peer
	multiplayer.peer_connected.connect(func(id: int) -> void: GameServer.player_connect(str(id)))
	multiplayer.peer_disconnected.connect(func(id: int) -> void: GameServer.player_disconnect(str(id)))
	GameServer.allocated.connect(_on_match)
	GameServer.shutdown_requested.connect(_on_stop_notice)
	await GameServer.ready_for_players()
```

- **The match.** `allocated` hands you its `external_id`, `labels`, `env` and `args`. `env` includes secret values, so never print it. On a fresh start it fires before the server says ready; a game server from a pool gets it when a start takes it.
- **Shutdown notice.** On `shutdown_requested`, save and call `get_tree().quit(0)` before `notice["deadline"]`.
- **End the match.** Call `await GameServer.stop("match_over")`, then quit the game.
- **Also.** `set_label()`, `remove_label()`, `tick()` for update times, `health()`, `public_address()`, and `set_player_count()` for games without player IDs.

Run anywhere else, every call does nothing, so the same export runs as a plain local server. `ready_for_players()` also prints `Server ready for players`, so the log ready check works with the add-on too.

## Optional: HTTPRequest calls

Every call sends `Authorization: Bearer $GS_TOKEN` to `$GS_LOCAL_API_URL`, with one `HTTPRequest` node per call:

```gdscript
var agent_url := OS.get_environment("GS_LOCAL_API_URL")
var token := OS.get_environment("GS_TOKEN")

func _call(method: int, path: String, body = null) -> Array:
	var request := HTTPRequest.new()
	request.timeout = 40.0  # longer than a long-poll's wait
	add_child(request)
	var headers := PackedStringArray(["Authorization: Bearer " + token])
	var data := ""
	if body != null:
		headers.append("Content-Type: application/json")
		data = JSON.stringify(body)
	if request.request(agent_url + path, headers, method, data) != OK:
		request.queue_free()
		return [0, ""]
	var done: Array = await request.request_completed
	request.queue_free()
	if done[0] != HTTPRequest.RESULT_SUCCESS:
		return [0, ""]
	return [done[1], (done[3] as PackedByteArray).get_string_from_utf8()]
```

- **Match settings.** Once the port is bound, call `GET /v0/self/allocation` once. A fresh start answers `200` with its match: read the JSON's `labels`, `env` and `args`. A game server from a pool gets `204`; after saying ready it calls `GET /v0/self/allocation?wait=30s` until it answers `200`.
- **Ready.** `await _call(HTTPClient.METHOD_POST, "/v0/self/ready")` after that first allocation call.
- **Players.** `PUT /v0/self/players` with `{"ids": [...]}` when someone joins or leaves.
- **Shutdown notice.** Keep a `GET /v0/self/shutdown?wait=30s` open. When it answers `200`, save and call `get_tree().quit(0)`.
- **End the match.** `POST /v0/self/stop` with `{"reason": "match_over"}`, then quit.

When `GS_LOCAL_API_URL` is empty, skip the calls: the server then runs as a plain local server on your own computer. The full list is on [the local API](https://studio.survivalservers.com/docs/game-server/local-api/) page.

## Run it

1. Try it on your computer: `godot --headless --path .` serves on port 7777.
1. Run it under the [local test agent](https://studio.survivalservers.com/docs/game-server/test-locally/) to see every call.
1. Build the image, push it, and start a game server as in the [quick start](https://studio.survivalservers.com/docs/get-started/quick-start/).
