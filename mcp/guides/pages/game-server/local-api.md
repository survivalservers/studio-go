# The local API

> The HTTP calls a game server can make on its own machine: ready, its match, players, labels, frame times, stop and the shutdown notice.

Every game server can call a small HTTP API on its own machine at `$GS_LOCAL_API_URL`. It uses HTTP/1.1 and JSON, so your engine's own HTTP client works: the HTTP module in Unreal, `HTTPRequest` in Godot, `fetch` in Node.js, or libcurl.

```bash
curl -X POST "$GS_LOCAL_API_URL/v0/self/ready" -H "Authorization: Bearer $GS_TOKEN"
```

Each engine's guide shows the calls in that engine: [Unreal](https://studio.survivalservers.com/docs/get-started/unreal/), [Unity](https://studio.survivalservers.com/docs/get-started/unity/) and [Godot](https://studio.survivalservers.com/docs/get-started/godot/).

Every call is optional. A game server that makes none still works: exiting is a stop and the console is the log. The version's ready check can watch a log line, a bound port or a delay instead. Use a log line for a server whose port opens before its world loads, or it counts as ready too early.

## The calls

| Call | Does | Answers |
|---|---|---|
| `POST /v0/self/ready` | Ready for players | `204` |
| `GET /v0/self/allocation` | Waits for the match | `200`, `204` |
| `GET /v0/self/shutdown` | Waits for a stop request | `200`, `204` |
| `PUT /v0/self/players` | Reports who is playing | `200` |
| `PATCH /v0/self/labels` | Sets or removes labels | `200` |
| `POST /v0/self/stop` | Ends the game server | `202` |
| `POST /v0/self/health` | Says it is still healthy | `204` |
| `POST /v0/self/tick` | Frame times of the last second | `204` |
| `POST /v0/self/saved` | The game just finished a save | `202` |
| `GET /v0/self` | This game server's own record | `200` |

## Rules for every call

- Send `Authorization: Bearer $GS_TOKEN`. The token works only for this game server.
- A missing or wrong token, or a game server that has ended, gets `401 unauthenticated`.
- On Linux, `GS_LOCAL_API_URL` is the container network's gateway. Calls are accepted only from the game server's own container.
- Each game server may make 50 calls a second, in bursts of up to 100. Over that: `429 rate_limited` with `Retry-After` in seconds.
- Request bodies are at most 64 KiB. Larger ones get `413 too_large`.
- Unknown fields in a request body are ignored.
- Errors are problem details (`application/problem+json`) with a stable `code`: `bad_request`, `unauthenticated`, `not_found`, `invalid_state`, `validation_failed`, `too_large`, `rate_limited` or `internal`. A `validation_failed` answer lists the fields in `errors`.

## Long-polls

`GET /v0/self/allocation` and `GET /v0/self/shutdown` take `wait`, a number with a unit: `500ms`, `30s`, `1m`. A bare number is seconds.

- `200` as soon as there is something to return.
- `204` when the wait runs out first. Call again.
- Without `wait`, the call answers at once.
- `wait` is capped at `60s`. A `wait` that is not a duration gets `422 validation_failed`.
- Each game server may hold 4 open long-polls of each kind. A fifth gets `429 rate_limited` with `Retry-After: 1`.

## Call details

### `POST /v0/self/ready`

```bash
curl -X POST "$GS_LOCAL_API_URL/v0/self/ready" -H "Authorization: Bearer $GS_TOKEN"
```

- `204`: recorded. A game server waiting in a pool becomes `warm`, available to matches; any other becomes `running`.
- It decides readiness only when the version's ready check is `local_api`. With another check it is recorded and does nothing.
- Calling it again is fine. After a stop request it gets `409 invalid_state`.

### `GET /v0/self/allocation`

```bash
curl "$GS_LOCAL_API_URL/v0/self/allocation?wait=30s" -H "Authorization: Bearer $GS_TOKEN"
```

```json
{"game_server_id": "gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2", "allocated_at": "2026-10-09T18:22:04.560Z",
 "external_id": "88231", "labels": {"queue": "ranked"}, "env": {"MATCH_MODE": "ranked"},
 "args": ["-MaxPlayers=10"], "expires_at": null}
```

- `200` with the [allocation data](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/) once the game server is allocated. A fresh start is allocated before its process starts, so it answers at once.
- `env` includes the start request's secret values. The allocation file leaves them out.
- `relay=true` leaves the secret values out. It is for compatibility layers that pass the match on as another platform's settings, such as GameLift's game properties. Leave it out in your own game server.
- `204` when the wait runs out, or early when a stop is requested meanwhile. Read the stop with `GET /v0/self/shutdown`.

### `GET /v0/self/shutdown`

```bash
curl "$GS_LOCAL_API_URL/v0/self/shutdown?wait=30s" -H "Authorization: Bearer $GS_TOKEN"
```

```json
{"requested_at": "2026-10-09T19:10:00.000Z", "deadline": "2026-10-09T19:10:30.000Z", "reason": "api"}
```

- `200` once a stop is requested: by the API, a time limit, or the game server's own `POST /v0/self/stop`. Save and exit before `deadline`.
- After `deadline` the whole process tree is ended.
- When the version has a `save` command, the notice comes once the save is done.
- Once your server has made this call, a stop sends it the notice first. The version's stop signal follows only if it still runs halfway through the shutdown time.
- A server that never makes this call gets the notice and the stop signal at once.
- `relay=true` is for compatibility layers that pass the notice on in another platform's terms, such as an Agones SDK server. That call does not count as your server waiting for the notice, so it gets the stop signal at once, as on that platform. Leave it out in your own game server.
- `reason` says why it stops. Treat a value you do not know like `api`.

| `reason` | Why it stops |
|---|---|
| `api`, `bulk` | A stop call or a bulk stop |
| `self` | Its own `POST /v0/self/stop` |
| `ttl`, `max_duration` | A time limit |
| `boot_timeout` | Not ready within the version's `boot_timeout` |
| `health_timeout` | No health call in time |
| `restart` | A persistent server's restart |
| `paused` | Pause when empty stops it |
| `out_of_disk` | Its files went over its disk size |
| `lost_contact` | Its machine lost contact with us |
| `machine_failure` | It was started on another machine |

### `PUT /v0/self/players`

```bash
curl -X PUT "$GS_LOCAL_API_URL/v0/self/players" -H "Authorization: Bearer $GS_TOKEN" \
  -H "Content-Type: application/json" -d '{"ids": ["p_1", "p_2"]}'
```

```json
{"count": 2, "ids": ["p_1", "p_2"]}
```

- Send exactly one of `players`, `ids` (your own player IDs) or `count`. The call replaces what was there.
- `ids`: up to 1,000, no repeats, each 1 to 128 characters. `count`: 0 to 100,000.
- An ID with a control character, often in a name a player chose, gets `422 validation_failed` naming it (`ids[2]`). Drop that player and send the rest.
- Your backend can then filter game servers with `players_below` and `players_above` on `GET /v0/game-servers`.
- To show who is on the server and where, send `players` instead of `ids`:

  ```bash
  curl -X PUT "$GS_LOCAL_API_URL/v0/self/players" -H "Authorization: Bearer $GS_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"players": [{"id": "p_1", "name": "Vex_77", "x": 120.5, "y": -40, "team": "red", "ping_ms": 34}]}'
  ```

  Only `id` is needed. The panel shows the list, and a map if your [game profile](https://studio.survivalservers.com/docs/concepts/game-profiles/#maps) has one. Only the latest list is kept, in the machine's memory, so positions are never recorded. Send it as often as once a second.
- On a persistent server with pause when empty on, a count of zero for its idle time pauses it. Never report zero while someone is playing.

### `PATCH /v0/self/labels`

```bash
curl -X PATCH "$GS_LOCAL_API_URL/v0/self/labels" -H "Authorization: Bearer $GS_TOKEN" \
  -H "Content-Type: application/merge-patch+json" -d '{"map": "desert", "queue": null}'
```

```json
{"labels": {"map": "desert", "match_id": "88231"}}
```

- A key with a string sets that label. A key with `null` removes it.
- Keys are 1 to 63 characters of `a-z 0-9 _ . -`. Values are at most 256 characters.
- At most 32 labels after the change.
- At allocation, the start request's labels win; labels set while waiting in a pool stay while there is room.
- Your backend can filter game servers with `label=map:desert` on `GET /v0/game-servers`.

### `POST /v0/self/stop`

```bash
curl -X POST "$GS_LOCAL_API_URL/v0/self/stop" -H "Authorization: Bearer $GS_TOKEN" \
  -H "Content-Type: application/json" -d '{"reason": "match_over"}'
```

```json
{"status": "stopping"}
```

- The same as exiting with code 0, recorded with the exit reason `self`. The body is optional; `reason` is at most 128 characters and shows in the game server's events.
- The game server then stops as for any stop: the notice, with `reason` `self`, and the version's stop method and shutdown time.
- Calling it again is fine.

### `POST /v0/self/health`

```bash
curl -X POST "$GS_LOCAL_API_URL/v0/self/health" -H "Authorization: Bearer $GS_TOKEN"
```

- `204`: recorded.
- If the version sets `health_timeout`, a game server that goes that long without this call is marked failed.

### `POST /v0/self/tick`

```bash
curl -X POST "$GS_LOCAL_API_URL/v0/self/tick" -H "Authorization: Bearer $GS_TOKEN" \
  -H "Content-Type: application/json" -d '{"p50_ms": 2.1, "p99_ms": 3.9, "hz": 128}'
```

- `204`: recorded. Send it once a second.
- `p99_ms` is required; `p50_ms` and `hz` (the target tick rate) are optional.
- Times are 0 to 100,000 ms; `hz` is 1 to 1,000.
- The values appear as `tick_p99_ms` in the game server's metrics.
- `hz` sets the frame budget, `tick_budget_ms` (1000 divided by `hz`), that the panel judges frame times against. With no `hz` there is no budget, and the panel shows frame times with no color.

### `POST /v0/self/saved`

```bash
curl -X POST "$GS_LOCAL_API_URL/v0/self/saved" -H "Authorization: Bearer $GS_TOKEN" \
  -H "Content-Type: application/json" -d '{"keep": "Season 3 end"}'
```

- `202`: being backed up. Optional, for persistent servers: saves are backed up without it.
- `keep` is optional, 1 to 100 characters. That backup stays, locked, under that name.
- Call it at most once every 10 seconds, and with `keep` once a minute. Sooner answers `429` with the reason.
- A game can keep 20 backups of a server. Past that, a save is an ordinary backup and the studio is told.
- `409`: a match server has no backups.
- `404` from an agent older than this call. It is not an error: treat it as a call that did nothing.

### `GET /v0/self`

```bash
curl "$GS_LOCAL_API_URL/v0/self" -H "Authorization: Bearer $GS_TOKEN"
```

```json
{"id": "gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2", "kind": "match", "app": "breakwire-windows", "version": "2.3.0",
 "region": "europe-west", "continent": "europe", "public_ip": "203.0.113.24",
 "ports": {"game": {"port": 31042, "protocol": "udp"}}, "labels": {"queue": "ranked"},
 "status": "running", "players": {"count": 2, "ids": ["p_1", "p_2"]},
 "allocated_at": "2026-10-09T18:22:04.560Z", "shutdown_requested_at": null}
```

- `status` is `starting` until ready, then `running`. A game server in a pool is `warm` while it waits for a match.
- `status` becomes `stopping` once a stop is requested.
- A port has `internal`, its container port, on Linux only. A [passthrough port](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/#passthrough-ports) has none: it listens on `port`.

## Order of calls in a match

1. Read your port from `GS_PORT_GAME` (`GS_PORT_GAME_INTERNAL` in a Linux image) and start listening.
2. `GET /v0/self/allocation` without `wait`. A fresh start gets `200` with its match: set it up now. A game server from a pool gets `204`.
3. `POST /v0/self/ready`.
4. After a `204` in step 2, repeat `GET /v0/self/allocation?wait=30s` until it answers `200`, then set the match up.
5. `PUT /v0/self/players` whenever someone joins or leaves.
6. Keep a `GET /v0/self/shutdown?wait=30s` open. When it answers, save and exit before `deadline`.
7. When the match is over, write any results to `GS_ARTIFACT_DIR`, then `POST /v0/self/stop` or exit with code 0.

Every field is in the local API's OpenAPI file, `openapi.yaml`, kept with the local test agent's source. To try every call on your own computer, see [test on your own PC](https://studio.survivalservers.com/docs/game-server/test-locally/).
