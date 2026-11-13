# List game servers

> List the game servers running now in one app, with each one's region and players.

`GET /v0/game-servers` lists your game servers, newest first. It shows those starting, running and stopping unless you pass `status`.

```ts
const api = 'https://studio.survivalservers.com/v0';
const res = await fetch(`${api}/game-servers?app=breakwire-windows&status=running`, {
  headers: { Authorization: `Bearer ${process.env.GS_API_KEY}` },
});
const page = await res.json();
if (!res.ok) throw new Error(`${res.status} ${page.code}: ${page.detail} (${page.request_id})`);
for (const server of page.data) console.log(server.id, server.region, server.players.count);
```

```python
import json, os, urllib.error, urllib.request

api = "https://studio.survivalservers.com/v0"
req = urllib.request.Request(f"{api}/game-servers?app=breakwire-windows&status=running")
req.add_header("Authorization", f"Bearer {os.environ['GS_API_KEY']}")
try:
    with urllib.request.urlopen(req) as r:
        page = json.load(r)
except urllib.error.HTTPError as e:
    problem = json.load(e)
    raise SystemExit(f"{e.code} {problem['code']}: {problem.get('detail')} ({problem['request_id']})")
for server in page["data"]:
    print(server["id"], server["region"], server["players"]["count"])
```

With our TypeScript and Python clients:

```ts
import { createClient, unwrap } from '@survivalservers/studio';

const gs = createClient({ baseUrl: 'https://studio.survivalservers.com', apiKey: process.env.GS_API_KEY! });
const { data } = await unwrap(gs.GET('/v0/game-servers', {
  params: { query: { app: 'breakwire-windows', status: ['running'] } },
}));
for (const server of data) console.log(server.id, server.region, server.players.count);
```

```python
import os

from survivalservers_studio import AuthenticatedClient
from survivalservers_studio.api.game_servers import list_game_servers
from survivalservers_studio.models import GameServerStatus

gs = AuthenticatedClient(base_url="https://studio.survivalservers.com", token=os.environ["GS_API_KEY"])
page = list_game_servers.sync(client=gs, app="breakwire-windows", status=[GameServerStatus.RUNNING])
for server in page.data:
    print(server.id, server.region, server.players.count)
```

Each page holds 50 game servers unless `limit` asks for up to 500. Pass `next_cursor` from the reply as `cursor` to get the next one. Ended game servers stay listed for 90 days with `status=stopped,failed`. Ready servers waiting in a pool are listed only once a start takes them.

Your key needs the `game_servers:read` scope.
