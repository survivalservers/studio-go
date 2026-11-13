# Take a backup

> Take a backup of a persistent server now, before a risky change.

`POST /v0/persistent-servers/{id}/backups` starts a backup now. It is listed with `kind` `manual`, and `label` names it so you can find it later.

```ts
const api = 'https://studio.survivalservers.com/v0';
const res = await fetch(`${api}/persistent-servers/ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P/backups`, {
  method: 'POST',
  headers: {
    Authorization: `Bearer ${process.env.GS_API_KEY}`,
    'Content-Type': 'application/json',
    'Idempotency-Key': crypto.randomUUID(),
  },
  body: JSON.stringify({ label: 'Before wipe' }),
});
const backup = await res.json();
if (!res.ok) throw new Error(`${res.status} ${backup.code}: ${backup.detail} (${backup.request_id})`);
console.log(backup.id, backup.status);
```

```python
import json, os, urllib.error, urllib.request, uuid

api = "https://studio.survivalservers.com/v0"
body = {"label": "Before wipe"}
req = urllib.request.Request(f"{api}/persistent-servers/ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P/backups", json.dumps(body).encode(), {
    "Authorization": f"Bearer {os.environ['GS_API_KEY']}",
    "Content-Type": "application/json",
    "Idempotency-Key": str(uuid.uuid4()),
})
try:
    with urllib.request.urlopen(req) as r:
        backup = json.load(r)
except urllib.error.HTTPError as e:
    problem = json.load(e)
    raise SystemExit(f"{e.code} {problem['code']}: {problem.get('detail')} ({problem['request_id']})")
print(backup["id"], backup["status"])
```

With our TypeScript and Python clients:

```ts
import { createClient, idempotencyKey, unwrap } from '@survivalservers/studio';

const gs = createClient({ baseUrl: 'https://studio.survivalservers.com', apiKey: process.env.GS_API_KEY! });
const backup = await unwrap(gs.POST('/v0/persistent-servers/{id}/backups', {
  params: { path: { id: 'ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P' }, header: { 'Idempotency-Key': idempotencyKey() } },
  body: { label: 'Before wipe' },
}));
console.log(backup.id, backup.status);
```

```python
import os
import uuid

from survivalservers_studio import AuthenticatedClient
from survivalservers_studio.api.persistent_servers import create_persistent_server_backup
from survivalservers_studio.models import BackupUpdate

gs = AuthenticatedClient(base_url="https://studio.survivalservers.com", token=os.environ["GS_API_KEY"])
body = BackupUpdate.from_dict({"label": "Before wipe"})
backup = create_persistent_server_backup.sync("ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P", client=gs, body=body, idempotency_key=str(uuid.uuid4()))
print(backup.id, backup.status)
```

The reply says `in_progress`. It is `ready` when the copy has finished, and `persistent_server.backup_created` fires then. See [back up and restore](https://studio.survivalservers.com/docs/guides/back-up-and-restore/).

The persistent server must be running. A stopped one answers `409 invalid_state`, because its last backup is from its stop.

Your key needs the `persistent_servers:write` scope.
