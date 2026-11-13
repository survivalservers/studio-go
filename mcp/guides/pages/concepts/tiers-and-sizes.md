# Tiers and sizes

> The standard and high-frequency tiers, what each holds for your game server, vCPU and memory sizes, and how a size is billed.

Every game server has a tier and a size. The tier is set on the app and can change per version. The size is set on the version and can change per start.

```json
POST /v0/game-servers
{ "app": "breakwire-windows", "branch": "live", "size": { "cpu": 4, "memory_gb": 8 } }
```

A start with another size than its version's is a fresh start, unless a pool of that size has a ready server.

## The two tiers

| Tier | Sizes | vCPUs held as |
|---|---|---|
| `standard` | 0.25 vCPU steps | A share of your studio's cores |
| `high-frequency` | Whole vCPUs | Pinned to this game server |

- **Standard** suits co-op, survival, PvE, slower PvP and most persistent servers.
- A standard game server is capped at its share of CPU time inside the cores held for your studio. Placement packs standard game servers onto as few machines as fit.
- **High-frequency** suits fast PvP at 64 or 128 ticks a second, where frame times count.
- A high-frequency game server's vCPUs are pinned to it alone. Placement spreads high-frequency matches across machines.
- A machine sells one tier, so high-frequency game servers never share a machine with standard ones.
- Not run on real machines yet: packing and spreading across several machines in one region.

## What counts as a vCPU

One vCPU is one hardware thread. A core has two. The game server reads its size from `GS_CPU`, which may be fractional such as `0.5`, and `GS_MEMORY_MB`, in MiB.

It is held to that size: on Windows by a Job Object that caps its vCPUs and committed memory, on Linux by its container's limits. Two rules hold on both tiers:

- **No CPU core is shared between two studios.** Your game servers run on cores held for your studio alone.
- **Committed memory is never oversold:** on Windows a machine takes a game server only when its RAM and page file can hold all it may commit.

## Memory

- Memory goes in 0.25 GB steps: at least 0.5 GB, and at most 4 GB per vCPU.
- A size the tier or your limits do not allow answers `422 size_not_allowed`, with the field and the reason.
- A high-frequency size with a part of a vCPU is refused with "high-frequency sizes are whole vCPUs".
- More than 4 GB per vCPU is refused with "at most 4 GB per vCPU".

## Studio limits

```bash
curl "https://studio.survivalservers.com/v0/studio" -H "Authorization: Bearer $API_KEY"
```

```json
"limits": {
  "running_cpus": 512,
  "cpus_per_game_server": 32,
  "memory_gb_per_game_server": 128,
  "zip_build_gb": 30,
  "image_build_gb": 40,
  "versions_per_app": 200,
  "build_storage_gb": 500
}
```

These are a studio's defaults.

- `running_cpus`: the vCPUs your studio can run at once, 512 by default.
- Ready servers waiting in a pool do not count toward `running_cpus`. A start past it answers `403 limit_reached`.
- A studio may keep up to 100 persistent servers, running or stopped. One more answers `403 limit_reached`: delete one first or ask us to raise it.
- `cpus_per_game_server` and `memory_gb_per_game_server`: the largest size of one game server.
- `zip_build_gb`, `image_build_gb` and `versions_per_app` are explained on [apps, builds and versions](https://studio.survivalservers.com/docs/concepts/apps-builds-versions/).
- [Talk to us](https://studio.survivalservers.com/#talk) to raise `running_cpus` or a build limit. One game server cannot go past 32 vCPUs and 128 GB.

## Disk

A game server's disk size is its build plus 10 GiB, or plus 50 GiB for a persistent server. A persistent server's is never less than its files held when its run began.

- Past its size, it gets one warning: the timeline's `disk_high` and the `game_server.disk_high` webhook.
- Past 120% of its size, it is warned and then saved and stopped as any stop is, with the exit reason `out_of_disk`. It is never killed outright, so it ends `stopped`.
- The same happens when the machine's disk is under 5% free, to the game server that has written the most since its run began. It must have written at least 256 MiB.
- Not run on real machines yet: the disk limit.

## How a size is billed

| Memory per vCPU | Rate class | Per vCPU-hour |
|---|---|---|
| Up to 2 GB | `on_demand_2gb` | $0.035 |
| Over 2 GB, up to 4 GB | `on_demand_4gb` | $0.05 |
| A persistent server, while running | `persistent_server` | $0.025 |

- Usage is counted per second. The tier and the system do not change the price.
- `{"cpu": 2, "memory_gb": 4}` bills 2 vCPU-hours an hour at $0.035. `{"cpu": 2, "memory_gb": 6}` bills them at $0.05.
- `GET /v0/prices` returns your studio's own locked prices. See [pricing](https://studio.survivalservers.com/docs/account/pricing/).

## Choose a size

Start with the smallest size your game server runs well at. Watch its CPU and memory in real matches, in the panel or with `GET /v0/game-servers/{id}/metrics`.

For high-frequency, watch `tick_p99_ms`: the slowest 1% of frames. It appears once the game server sends its frame times with `POST /v0/self/tick`. The budget is 15.6 ms at 64 Hz and 7.8 ms at 128 Hz. When the game server sends its rate (`hz`), the metrics carry the budget as `tick_budget_ms` and the panel shows frame time against it.

`gsbench` is our synthetic game loop, not a real game. In October 2026, 16 copies at 128 Hz kept the slowest 1% of frames at 3.79 ms (Windows) and 4.12 ms (Linux).
