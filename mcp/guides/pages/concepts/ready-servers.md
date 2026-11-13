# Ready servers

> When a game server is joinable at the reply because it was ready ahead of time, when it starts fresh, and how long each start takes.

Every start returns the game server's final address, so you can send players right away. A start from a ready server takes a game server that is already running; the API calls it `warm`. A fresh start launches a new one; the API calls it `cold`.

## Choose for each start

```json
POST /v0/game-servers
{ "app": "breakwire-windows", "branch": "live", "start_mode": "auto" }
```

| `start_mode` | What happens |
|---|---|
| `auto` (default) | From a ready server if one is waiting, else fresh |
| `warm` | From a ready server only; `503 capacity_unavailable` if none |
| `cold` | Always a new process |

The reply's `start_mode` says which one you got: `warm` or `cold`.

## Start times

On October 20 and 21, 2026, 50 starts of each kind, one at a time, took:

| Start | System | Median | 95 in 100 under |
|---|---|---|---|
| From a ready server | Windows | 34.5 ms | 51.6 ms |
| From a ready server | Linux | 19 ms | 27 ms |
| Fresh | Windows | 75 ms | 86 ms |
| Fresh | Linux | 108 ms | 127.5 ms |

- These are the API's own times, from the start call to the reply with the address.
- A fresh game server is joinable after its own boot time on top of that.
- Our target is a median of 250 ms or less, with 95 in 100 starts under 1 s.
- `GET /v0/stats/starts` returns your own median (`p50_ms`) and the time 95 in 100 starts were under (`p95_ms`), by region, app or start mode.

## Fresh starts

A fresh start launches a new process or container from the version's build, which is already copied to the machine.

- The reply has `"status": "starting"` and the address. Players who connect early wait for the boot.
- The game server turns `running` when its ready check passes, and `game_server.running` fires.
- If it is not ready within the version's `boot_timeout` (5 minutes by default), it ends `failed` with reason `boot_timeout`.
- A game that opens its port before its world loads needs a `log` ready check, such as V Rising's `Startup Completed` line.
- A `port` check on such a game marks it running too early, and a stop before the world loads saves nothing.

## Starts from a ready server

A pool keeps game servers of one version and size started and ready in a region. A start takes one of them and replies with `"status": "running"`: joinable at the reply.

- Its ID does not change when it is taken.
- The start's settings reach it as [allocation data](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/), because its process is already running.
- A ready game server goes only to a start with the same version and size, and only once its ready check has passed.

### One code path for both

```bash
# In the game server: say ready, then wait for the match or a stop.
curl -s -X POST "$GS_LOCAL_API_URL/v0/self/ready" -H "Authorization: Bearer $GS_TOKEN"
while [ "$(curl -s -o allocation.json -w '%{http_code}' \
    "$GS_LOCAL_API_URL/v0/self/allocation?wait=30s" -H "Authorization: Bearer $GS_TOKEN")" != 200 ]; do
  [ "$(curl -s -o /dev/null -w '%{http_code}' \
    "$GS_LOCAL_API_URL/v0/self/shutdown" -H "Authorization: Bearer $GS_TOKEN")" = 200 ] && exit 0
done
```

- `GET /v0/self/allocation` answers `200` with the allocation, or `204` when `wait` runs out first. The game can watch `GS_ALLOCATION_FILE` instead.
- After a stop request it answers `204` at once, every time. `GET /v0/self/shutdown` then answers `200`, so the loop ends.
- A fresh start is allocated before its process starts, so the same loop returns at once.
- To set a fresh start's match up before its first player, read the allocation once without `wait` before you call ready. It answers `200` on a fresh start and `204` on a ready server.
- `env` and `args` from the start also reach a fresh game server on its environment and command line. Both kinds get them in the allocation data, so read them there.

## Pools

```bash
curl -X PUT "https://studio.survivalservers.com/v0/apps/breakwire-windows/pools/europe-west" \
  -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" \
  -d '{"branch": "live", "size": {"cpu": 2, "memory_gb": 4}, "mode": "auto", "min": 2, "max": 40}'
```

```json
{
  "region": "europe-west",
  "branch": "live",
  "version": "2.3.0",
  "mode": "auto",
  "min": 2,
  "max": 40,
  "status": {
    "ready": 6, "starting": 2, "target": 8, "allocations_last_hour": 212,
    "versions": [{ "version": "2.3.0", "ready": 6, "starting": 2 }],
    "failed_starts": 0
  }
}
```

- A pool is per app and region. It follows a `branch` (default `live`) or stays on one `version`.
- `fixed` keeps `min` ready.
- `auto` sets `target` to the rate of starts that wanted a ready server in the busiest minute of the last 10, times the version's average boot time, doubled. Starts that found the pool empty count too; a start that asked for a fresh game server does not. It stays between `min` and `max`.
- The boot time is the average of the version's fresh starts over the last day, or 30 seconds until it has one.
- `auto` needs `max`. `fixed` takes no `max`.
- A start that could have used a ready server but found none fires `pool.exhausted`. Under `auto` that start goes fresh.
- When the pool's branch gets a new version, new ready servers start once that version is ready in the region. Starts on the branch are fresh until then.
- The old version's ready servers keep waiting, for starts that still name that version, until the new ones take their place.
- `status.ready` and `status.starting` count the pool's own version only. `status.versions` lists the waiting game servers by version, the pool's own first, so old ones left from a switch show there and never as the pool's own.
- Waiting game servers do not appear in game server lists until a start takes one.
- A pool that needs more ready servers and cannot start one says why in `status.waiting`: no machine in the region has room for its size for your studio now (`no_capacity`), no machine there sells its tier on its system (`not_offered`), its version is still copying there (`version_not_ready`), your studio's running vCPUs are at its limit (`limit_reached`), or its starts need a card (`card_required`). Its `detail` is a sentence for you, and it clears once one starts.
- Waiting game servers count in your studio's running vCPUs, like any game server, so a pool never takes your studio past its limit. Taking one for a start adds nothing.
- While your studio is suspended or closed its pools keep no ready servers: the waiting ones stop, `status.target` reads 0, and `status.waiting` says why (`studio_suspended`, `studio_closed`). While its starts need a card, the waiting ones stay but no new one starts.

### When new ready servers fail

A failed start is a waiting game server of the pool's version that ended on its own before a start took it: before its ready check for any reason (it crashed, quit or was not ready in time), or after it by crashing or running out of memory. One we stopped, one lost with its machine, or one whose game quit cleanly after its ready check is not.

- `status.failed_starts` counts them in a row. It goes back to 0 once one is ready. `status.last_failure` says how the newest ended, with its exit code.
- While starts fail, the pool starts one at a time. It waits 5 seconds after the first failure and twice as long after each one after, up to 2 minutes, and starts at most 4 a minute. `status.next_start_at` says when the next one goes.
- At 3 failed in a row `pool.starts_failing` fires.
- While its new version's starts fail, the pool keeps its full number of ready servers of the version it ran before. If one of those is taken by a start, or made room for a new start that then crashed, another starts in its place.
- At 10 the pool holds: it starts no more on that version, and `status.held` says why. Ready servers of its other versions keep waiting, as many as the pool keeps. `pool.starts_failing` fires again with `held: true`.
- Saving the pool again (`PUT`, as it is or changed) tries again, with the count from 0. So does moving it to another version, by a rollout or its branch.

A rollout to a pool stops at its first failed start and puts the pool back: see [bulk actions](https://studio.survivalservers.com/docs/guides/bulk-actions/#restart-and-roll-out).
- `DELETE /v0/apps/{app}/pools/{region}` stops the waiting ones. Game servers already taken keep running.
- A waiting game server's time counts from its launch, at the on-demand rate for its size.
