# Uptime

> What we aim for today, the promises and credits to come, and how uptime is measured.

Today we aim for 99.5% uptime each month (about 3.6 hours down) and give no uptime credits. The promises and credits below start on the day we announce them.

## Promises

| What | Monthly uptime on each continent |
|---|---|
| The API | 99.99% |
| Game server starts | 99.95% |

- The API promise covers calls made directly or through the panel.
- It also covers calls made through survivalservers and the MCP server.
- A start is a request to start a game server or a persistent server. It is on time when the address comes back within 5 seconds.
- The promises cover the regions where `GET /v0/regions` shows `sla_eligible: true`. A region leaving them shows the day in `sla_ends_at`, set at least 30 days ahead, on the 1st of a month.
- They do not cover game servers on your own machines, or backups and storage.

## Credits

| Monthly uptime on a continent | Credit |
|---|---|
| API under 99.99%, or starts under 99.95% | 25% |
| Either under 99.0% | 50% |
| Either under 95.0% | 100% |

- A credit is that share of your charges on that continent for the month, after free vCPU-hours. It leaves out your own machines and backup storage.
- When the API and starts both miss on one continent, you get the larger credit, not both.
- Credits come off that month's invoice before you are charged, and you do not have to ask. `GET /v0/credits` lists them as `kind: sla`, with the `continent`.
- If you saw failed or late calls in minutes we did not count, tell us through [Talk to us](https://studio.survivalservers.com/#talk) within 30 days of the month's end. Include the times, regions and `Request-Id` values.

## How uptime is measured

- Every minute, a probe starts a small game server in each covered region, on each system the region offers, through the public API. Three probes check that it answers.
- A region's minute is down when that start fails or is late, or when two of the three probes cannot reach the game server.
- It is also down when more than 1% of the real starts there fail on our side or are late, in a minute with at least 20.
- A continent is up in a minute while at least one of its measured regions is up. A start can go to any of them.
- An API minute is down when two or more probes get a `5xx` answer, a `429`, no answer, or an answer after 5 seconds.
- Starts refused for your own reasons never count against us: your build, your limits, a missing card or a key that is not allowed.
- These never count against us: your own builds, settings, keys, limits and network; a suspended studio or game server; traffic we limit to your game servers under attack to protect other studios; and starts refused for lack of room when your running vCPUs on a continent are more than twice your highest in 30 days and 64 above it, unless you told us 7 days ahead.
- Planned maintenance is never left out. Any downtime it causes counts against us.

The probes run on our machines today. There is no public status page: your own starts and their reply times are on the panel's Overview and in `GET /v0/stats/starts`, your uptime credits are on your invoices and in `GET /v0/credits`, and we tell you what we measured for any month when you ask through [Talk to us](https://studio.survivalservers.com/#talk).

## Your start times

Your own starts are counted too. Read their counts and reply times:

```bash
curl "https://studio.survivalservers.com/v0/stats/starts?group_by=region&from=2026-10-28T00:00:00Z&to=2026-10-29T00:00:00Z" \
  -H "Authorization: Bearer $API_KEY"
```

The reply, trimmed to one region:

```json
{
  "from": "2026-10-28T00:00:00Z",
  "to": "2026-10-29T00:00:00Z",
  "data": [
    { "region": "europe-west", "starts": 49, "failed": 0, "warm": 3, "cold": 46, "p50_ms": 132, "p95_ms": 196 }
  ]
}
```

- `p50_ms` (the median) and `p95_ms` (95 in 100 were faster) are the reply times of successful starts, measured at our API edge from request in to address out.
- `warm` counts starts from a ready server and `cold` fresh starts. `failed` counts only failures on our side.
- `group_by` takes `region`, `app` and `start_mode`. A period is at most 92 days and defaults to the last 24 hours.
- The call needs the `game_servers:read` scope. The panel's overview shows the same figures.

On October 20 and 21, 2026, 50 starts of each kind ran one at a time. A start from a ready server answered in 19 ms at the median on Linux and 34.5 ms on Windows. A fresh start took 108 ms at the median on Linux and 75 ms on Windows.
