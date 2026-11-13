# Send a game command

> Send a console line to one persistent server, such as kicking a player, and read what the game answered.

`POST /v0/persistent-servers/{id}/command` sends a line to the running game and answers with what it wrote back. The line is your game's own command.

```ts
const api = 'https://studio.survivalservers.com/v0';
const res = await fetch(`${api}/persistent-servers/ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P/command`, {
  method: 'POST',
  headers: {
    Authorization: `Bearer ${process.env.GS_API_KEY}`,
    'Content-Type': 'application/json',
    'Idempotency-Key': crypto.randomUUID(),
  },
  body: JSON.stringify({ command: { type: 'console', line: 'kick Vex_77' } }),
});
const result = await res.json();
if (!res.ok) throw new Error(`${res.status} ${result.code}: ${result.detail} (${result.request_id})`);
console.log(result.output);
```

```python
import json, os, urllib.error, urllib.request, uuid

api = "https://studio.survivalservers.com/v0"
body = {"command": {"type": "console", "line": "kick Vex_77"}}
req = urllib.request.Request(f"{api}/persistent-servers/ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P/command", json.dumps(body).encode(), {
    "Authorization": f"Bearer {os.environ['GS_API_KEY']}",
    "Content-Type": "application/json",
    "Idempotency-Key": str(uuid.uuid4()),
})
try:
    with urllib.request.urlopen(req) as r:
        result = json.load(r)
except urllib.error.HTTPError as e:
    problem = json.load(e)
    raise SystemExit(f"{e.code} {problem['code']}: {problem.get('detail')} ({problem['request_id']})")
print(result["output"])
```

With our TypeScript and Python clients:

```ts
import { createClient, idempotencyKey, unwrap } from '@survivalservers/studio';

const gs = createClient({ baseUrl: 'https://studio.survivalservers.com', apiKey: process.env.GS_API_KEY! });
const result = await unwrap(gs.POST('/v0/persistent-servers/{id}/command', {
  params: { path: { id: 'ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P' }, header: { 'Idempotency-Key': idempotencyKey() } },
  body: { command: { type: 'console', line: 'kick Vex_77' } },
}));
console.log(result.output);
```

```python
import os
import uuid

from survivalservers_studio import AuthenticatedClient
from survivalservers_studio.api.persistent_servers import send_persistent_server_command
from survivalservers_studio.models import CommandSend

gs = AuthenticatedClient(base_url="https://studio.survivalservers.com", token=os.environ["GS_API_KEY"])
body = CommandSend.from_dict({"command": {"type": "console", "line": "kick Vex_77"}})
result = send_persistent_server_command.sync("ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P", client=gs, body=body, idempotency_key=str(uuid.uuid4()))
print(result.output)
```

`output` holds the console lines written in the next 2 seconds, and `capture` sets another time, up to 30 seconds. `command` can also be `rcon` or `http`. See [bulk actions](https://studio.survivalservers.com/docs/guides/bulk-actions/#send-a-command) for those shapes.

A persistent server that is stopped or paused answers `409 invalid_state`. A console line reaches a game that reads whole lines from its console. A game that reads single key presses, such as Unturned on Windows, does not see it; use RCON if it has one.

Your key needs the `persistent_servers:write` scope.
