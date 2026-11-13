# Stop a game server

> Stop a running game server, giving it time to save and quit first.

`POST /v0/game-servers/{id}/stop` asks the game server to stop. Billing ends when it has.

```ts
const api = 'https://studio.survivalservers.com/v0';
const res = await fetch(`${api}/game-servers/gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2/stop`, {
  method: 'POST',
  headers: {
    Authorization: `Bearer ${process.env.GS_API_KEY}`,
    'Content-Type': 'application/json',
    'Idempotency-Key': crypto.randomUUID(),
  },
  body: JSON.stringify({ reason: 'match_over' }),
});
const server = await res.json();
if (!res.ok) throw new Error(`${res.status} ${server.code}: ${server.detail} (${server.request_id})`);
console.log(server.id, server.status);
```

```python
import json, os, urllib.error, urllib.request, uuid

api = "https://studio.survivalservers.com/v0"
body = {"reason": "match_over"}
req = urllib.request.Request(f"{api}/game-servers/gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2/stop", json.dumps(body).encode(), {
    "Authorization": f"Bearer {os.environ['GS_API_KEY']}",
    "Content-Type": "application/json",
    "Idempotency-Key": str(uuid.uuid4()),
})
try:
    with urllib.request.urlopen(req) as r:
        server = json.load(r)
except urllib.error.HTTPError as e:
    problem = json.load(e)
    raise SystemExit(f"{e.code} {problem['code']}: {problem.get('detail')} ({problem['request_id']})")
print(server["id"], server["status"])
```

With our TypeScript and Python clients:

```ts
import { createClient, idempotencyKey, unwrap } from '@survivalservers/studio';

const gs = createClient({ baseUrl: 'https://studio.survivalservers.com', apiKey: process.env.GS_API_KEY! });
const server = await unwrap(gs.POST('/v0/game-servers/{id}/stop', {
  params: { path: { id: 'gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2' }, header: { 'Idempotency-Key': idempotencyKey() } },
  body: { reason: 'match_over' },
}));
console.log(server.id, server.status);
```

```python
import os
import uuid

from survivalservers_studio import AuthenticatedClient
from survivalservers_studio.api.game_servers import stop_game_server
from survivalservers_studio.models import GameServerStop

gs = AuthenticatedClient(base_url="https://studio.survivalservers.com", token=os.environ["GS_API_KEY"])
body = GameServerStop.from_dict({"reason": "match_over"})
server = stop_game_server.sync("gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2", client=gs, body=body, idempotency_key=str(uuid.uuid4()))
print(server.id, server.status)
```

The reply says `stopping`. The game server gets its shutdown time to save and quit, and `shutdown_time` sets a different time for this stop. A game that waits for the shutdown notice gets the stop signal only if it still runs halfway through that time. A game server that has already ended answers `409 already_stopped`.

Your key needs the `game_servers:write` scope.
