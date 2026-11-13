# Get the join address

> Read a game server's IP address and game port, the address your players join.

Every game server carries its address. `ports` has one entry per named port in the version.

```ts
const api = 'https://studio.survivalservers.com/v0';
const res = await fetch(`${api}/game-servers/gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2`, {
  headers: { Authorization: `Bearer ${process.env.GS_API_KEY}` },
});
const server = await res.json();
if (!res.ok) throw new Error(`${res.status} ${server.code}: ${server.detail} (${server.request_id})`);
const { ipv4, ports } = server.address;
console.log(`${ipv4}:${ports.game.port}`);
```

```python
import json, os, urllib.error, urllib.request

api = "https://studio.survivalservers.com/v0"
req = urllib.request.Request(f"{api}/game-servers/gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2")
req.add_header("Authorization", f"Bearer {os.environ['GS_API_KEY']}")
try:
    with urllib.request.urlopen(req) as r:
        server = json.load(r)
except urllib.error.HTTPError as e:
    problem = json.load(e)
    raise SystemExit(f"{e.code} {problem['code']}: {problem.get('detail')} ({problem['request_id']})")
print(f"{server['address']['ipv4']}:{server['address']['ports']['game']['port']}")
```

With our TypeScript and Python clients:

```ts
import { createClient, unwrap } from '@survivalservers/studio';

const gs = createClient({ baseUrl: 'https://studio.survivalservers.com', apiKey: process.env.GS_API_KEY! });
const server = await unwrap(gs.GET('/v0/game-servers/{id}', {
  params: { path: { id: 'gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2' } },
}));
const { ipv4, ports } = server.address;
console.log(`${ipv4}:${ports.game.port}`);
```

```python
import os

from survivalservers_studio import AuthenticatedClient
from survivalservers_studio.api.game_servers import get_game_server

gs = AuthenticatedClient(base_url="https://studio.survivalservers.com", token=os.environ["GS_API_KEY"])
server = get_game_server.sync("gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2", client=gs)
print(f"{server.address.ipv4}:{server.address.ports['game'].port}")
```

The address is final as soon as the start call answers. Players can join once `status` is `running`: at once from a ready server, or after a fresh start's ready check passes.

Your key needs the `game_servers:read` scope.
