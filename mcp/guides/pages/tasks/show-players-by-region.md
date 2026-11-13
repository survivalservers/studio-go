# Show players by region

> Read how many players are online in each region, hour by hour, for a chart or a dashboard.

`GET /v0/stats/game-servers` returns one bucket per step and group. With `group_by` set to `region`, each bucket is one region for one hour.

```ts
const api = 'https://studio.survivalservers.com/v0';
const res = await fetch(`${api}/stats/game-servers?step=1h&group_by=region`, {
  headers: { Authorization: `Bearer ${process.env.GS_API_KEY}` },
});
const stats = await res.json();
if (!res.ok) throw new Error(`${res.status} ${stats.code}: ${stats.detail} (${stats.request_id})`);
for (const b of stats.buckets) if (b.group?.region) console.log(b.t, b.group.region, b.players_online, b.players_peak);
```

```python
import json, os, urllib.error, urllib.request

api = "https://studio.survivalservers.com/v0"
req = urllib.request.Request(f"{api}/stats/game-servers?step=1h&group_by=region")
req.add_header("Authorization", f"Bearer {os.environ['GS_API_KEY']}")
try:
    with urllib.request.urlopen(req) as r:
        stats = json.load(r)
except urllib.error.HTTPError as e:
    problem = json.load(e)
    raise SystemExit(f"{e.code} {problem['code']}: {problem.get('detail')} ({problem['request_id']})")
for b in stats["buckets"]:
    region = (b.get("group") or {}).get("region")
    if region:
        print(b["t"], region, b["players_online"], b["players_peak"])
```

With our TypeScript and Python clients:

```ts
import { createClient, unwrap } from '@survivalservers/studio';

const gs = createClient({ baseUrl: 'https://studio.survivalservers.com', apiKey: process.env.GS_API_KEY! });
const { buckets } = await unwrap(gs.GET('/v0/stats/game-servers', {
  params: { query: { step: '1h', group_by: ['region'] } },
}));
for (const b of buckets) if (b.group?.region) console.log(b.t, b.group.region, b.players_online, b.players_peak);
```

```python
import os

from survivalservers_studio import AuthenticatedClient
from survivalservers_studio.api.game_servers import get_game_server_stats
from survivalservers_studio.models import GetGameServerStatsGroupByItem, StatsStep

gs = AuthenticatedClient(base_url="https://studio.survivalservers.com", token=os.environ["GS_API_KEY"])
stats = get_game_server_stats.sync(client=gs, step=StatsStep("1h"), group_by=[GetGameServerStatsGroupByItem.REGION])
for b in stats.buckets:
    if b.group.region:
        print(b.t, b.group.region, b.players_online, b.players_peak)
```

By default it covers the last 24 hours. An hour with no game servers running has no region, so the samples skip it. `players_online` is the average over the hour and `players_peak` the most at once. Players are each game server's count. It comes from the game on the [local API](https://studio.survivalservers.com/docs/game-server/local-api/), from your own calls, or from the machine reading the game's Steam query port or profile.

Your key needs the `game_servers:read` scope.
