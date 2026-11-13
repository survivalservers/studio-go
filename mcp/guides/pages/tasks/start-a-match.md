# Start a match

> Start a game server for one match from your backend and get the address to send your players.

`POST /v0/game-servers` starts a game server and answers with its address. Use the match ID as the `Idempotency-Key`, so a retry never starts a second one.

```ts
const api = 'https://studio.survivalservers.com/v0';
const res = await fetch(`${api}/game-servers`, {
  method: 'POST',
  headers: {
    Authorization: `Bearer ${process.env.GS_API_KEY}`,
    'Content-Type': 'application/json',
    'Idempotency-Key': 'match-8841',
  },
  body: JSON.stringify({ app: 'breakwire-windows', placement: { regions: ['europe-west'] }, ttl: '1h' }),
});
const server = await res.json();
if (!res.ok) throw new Error(`${res.status} ${server.code}: ${server.detail} (${server.request_id})`);
const { ipv4, ports } = server.address;
console.log(server.id, `${ipv4}:${ports.game.port}`);
```

```python
import json, os, urllib.error, urllib.request

api = "https://studio.survivalservers.com/v0"
body = {"app": "breakwire-windows", "placement": {"regions": ["europe-west"]}, "ttl": "1h"}
req = urllib.request.Request(f"{api}/game-servers", json.dumps(body).encode(), {
    "Authorization": f"Bearer {os.environ['GS_API_KEY']}",
    "Content-Type": "application/json",
    "Idempotency-Key": "match-8841",
})
try:
    with urllib.request.urlopen(req) as r:
        server = json.load(r)
except urllib.error.HTTPError as e:
    problem = json.load(e)
    raise SystemExit(f"{e.code} {problem['code']}: {problem.get('detail')} ({problem['request_id']})")
print(server["id"], f"{server['address']['ipv4']}:{server['address']['ports']['game']['port']}")
```

With our TypeScript and Python clients:

```ts
import { createClient, unwrap } from '@survivalservers/studio';

const gs = createClient({ baseUrl: 'https://studio.survivalservers.com', apiKey: process.env.GS_API_KEY! });
const server = await unwrap(gs.POST('/v0/game-servers', {
  params: { header: { 'Idempotency-Key': 'match-8841' } },
  body: { app: 'breakwire-windows', placement: { regions: ['europe-west'] }, ttl: '1h' },
}));
const { ipv4, ports } = server.address;
console.log(server.id, `${ipv4}:${ports.game.port}`);
```

```python
import os

from survivalservers_studio import AuthenticatedClient
from survivalservers_studio.api.game_servers import start_game_server
from survivalservers_studio.models import GameServerCreate

gs = AuthenticatedClient(base_url="https://studio.survivalservers.com", token=os.environ["GS_API_KEY"])
body = GameServerCreate.from_dict({"app": "breakwire-windows", "placement": {"regions": ["europe-west"]}, "ttl": "1h"})
server = start_game_server.sync(client=gs, body=body, idempotency_key="match-8841")
print(server.id, f"{server.address.ipv4}:{server.address.ports['game'].port}")
```

A start from a ready server answers `running`. A fresh start answers `starting`, and `game_server.running` fires when its ready check passes. `ttl` stops the game server after this long if nothing else does. See [start matches from your backend](https://studio.survivalservers.com/docs/guides/start-matches-from-your-backend/).

Your key needs the `game_servers:write` scope.
