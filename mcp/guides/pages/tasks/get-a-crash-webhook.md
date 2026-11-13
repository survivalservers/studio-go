# Get a crash webhook

> Have your backend called when a game server crashes, runs out of memory or fails to start.

`POST /v0/webhook-endpoints` adds an address we call. `game_server.failed` fires when a game server ends through a fault.

```ts
const api = 'https://studio.survivalservers.com/v0';
const res = await fetch(`${api}/webhook-endpoints`, {
  method: 'POST',
  headers: {
    Authorization: `Bearer ${process.env.GS_API_KEY}`,
    'Content-Type': 'application/json',
    'Idempotency-Key': crypto.randomUUID(),
  },
  body: JSON.stringify({ url: 'https://backend.kingsmoor.example/hooks/game-servers', events: ['game_server.failed'] }),
});
const endpoint = await res.json();
if (!res.ok) throw new Error(`${res.status} ${endpoint.code}: ${endpoint.detail} (${endpoint.request_id})`);
console.log(endpoint.id, endpoint.secret);
```

```python
import json, os, urllib.error, urllib.request, uuid

api = "https://studio.survivalservers.com/v0"
body = {"url": "https://backend.kingsmoor.example/hooks/game-servers", "events": ["game_server.failed"]}
req = urllib.request.Request(f"{api}/webhook-endpoints", json.dumps(body).encode(), {
    "Authorization": f"Bearer {os.environ['GS_API_KEY']}",
    "Content-Type": "application/json",
    "Idempotency-Key": str(uuid.uuid4()),
})
try:
    with urllib.request.urlopen(req) as r:
        endpoint = json.load(r)
except urllib.error.HTTPError as e:
    problem = json.load(e)
    raise SystemExit(f"{e.code} {problem['code']}: {problem.get('detail')} ({problem['request_id']})")
print(endpoint["id"], endpoint["secret"])
```

With our TypeScript and Python clients:

```ts
import { createClient, idempotencyKey, unwrap } from '@survivalservers/studio';

const gs = createClient({ baseUrl: 'https://studio.survivalservers.com', apiKey: process.env.GS_API_KEY! });
const endpoint = await unwrap(gs.POST('/v0/webhook-endpoints', {
  params: { header: { 'Idempotency-Key': idempotencyKey() } },
  body: { url: 'https://backend.kingsmoor.example/hooks/game-servers', events: ['game_server.failed'] },
}));
console.log(endpoint.id, endpoint.secret);
```

```python
import os
import uuid

from survivalservers_studio import AuthenticatedClient
from survivalservers_studio.api.webhooks import create_webhook_endpoint
from survivalservers_studio.models import WebhookEndpointCreate

gs = AuthenticatedClient(base_url="https://studio.survivalservers.com", token=os.environ["GS_API_KEY"])
body = WebhookEndpointCreate.from_dict({
    "url": "https://backend.kingsmoor.example/hooks/game-servers",
    "events": ["game_server.failed"],
})
endpoint = create_webhook_endpoint.sync(client=gs, body=body, idempotency_key=str(uuid.uuid4()))
print(endpoint.id, endpoint.secret)
```

The reply shows `secret` once. Keep it on your backend, and [check the signature](https://studio.survivalservers.com/docs/guides/webhooks/#check-the-signature) of every call.

`data.exit.reason` in the event says why it ended: `crashed`, `out_of_memory`, `boot_timeout`, `health_timeout`, `machine_failure` or `lost_contact`. The address must use HTTPS and be on the public internet: a loopback, private or link-local address is refused when you save it and on every call.

Your key needs the `webhooks:write` scope.
