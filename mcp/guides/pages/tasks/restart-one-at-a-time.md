# Restart one at a time

> Restart every persistent server of a game one by one, with a dry run first.

A bulk action restarts persistent servers in batches and watches each one come back. `batch_size: 1` restarts one at a time.

```ts
const api = 'https://studio.survivalservers.com/v0';
const headers = { Authorization: `Bearer ${process.env.GS_API_KEY}`, 'Content-Type': 'application/json' };
async function post(body: object) {
  const res = await fetch(`${api}/bulk-actions/restarts`, {
    method: 'POST',
    headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
    body: JSON.stringify(body),
  });
  const answer = await res.json();
  if (!res.ok) throw new Error(`${res.status} ${answer.code}: ${answer.detail} (${answer.request_id})`);
  return answer;
}

const body = {
  selector: { target: 'persistent_servers', app: 'brinewatch-linux' },
  rollout: { batch_size: 1 },
};
const { plan } = await post({ ...body, dry_run: true });
console.log(plan.total, 'persistent servers,', plan.players, 'players on them');

// After a person has seen the plan
const { bulk_action } = await post(body);
console.log(bulk_action?.id, bulk_action?.status);
```

```python
import json, os, urllib.error, urllib.request, uuid

api = "https://studio.survivalservers.com/v0"

def post(body):
    headers = {"Authorization": f"Bearer {os.environ['GS_API_KEY']}", "Content-Type": "application/json"}
    req = urllib.request.Request(f"{api}/bulk-actions/restarts", json.dumps(body).encode(), {**headers, "Idempotency-Key": str(uuid.uuid4())})
    try:
        with urllib.request.urlopen(req) as r:
            return json.load(r)
    except urllib.error.HTTPError as e:
        problem = json.load(e)
        raise SystemExit(f"{e.code} {problem['code']}: {problem.get('detail')} ({problem['request_id']})")

body = {
    "selector": {"target": "persistent_servers", "app": "brinewatch-linux"},
    "rollout": {"batch_size": 1},
}
plan = post({**body, "dry_run": True})["plan"]
print(plan["total"], "persistent servers,", plan["players"], "players on them")

# After a person has seen the plan
bulk_action = post(body)["bulk_action"]
print(bulk_action["id"], bulk_action["status"])
```

With our TypeScript and Python clients:

```ts
import { createClient, idempotencyKey, unwrap } from '@survivalservers/studio';

const gs = createClient({ baseUrl: 'https://studio.survivalservers.com', apiKey: process.env.GS_API_KEY! });
const body = {
  selector: { target: 'persistent_servers' as const, app: 'brinewatch-linux' },
  rollout: { batch_size: 1 },
};
const { plan } = await unwrap(gs.POST('/v0/bulk-actions/restarts', {
  params: { header: { 'Idempotency-Key': idempotencyKey() } },
  body: { ...body, dry_run: true },
}));
console.log(plan.total, 'persistent servers,', plan.players, 'players on them');

// After a person has seen the plan
const { bulk_action } = await unwrap(gs.POST('/v0/bulk-actions/restarts', {
  params: { header: { 'Idempotency-Key': idempotencyKey() } },
  body,
}));
console.log(bulk_action?.id, bulk_action?.status);
```

```python
import os
import uuid

from survivalservers_studio import AuthenticatedClient
from survivalservers_studio.api.bulk_actions import bulk_restart
from survivalservers_studio.models import BulkActionRestartCreate

gs = AuthenticatedClient(base_url="https://studio.survivalservers.com", token=os.environ["GS_API_KEY"])
body = {
    "selector": {"target": "persistent_servers", "app": "brinewatch-linux"},
    "rollout": {"batch_size": 1},
}
plan = bulk_restart.sync(client=gs, body=BulkActionRestartCreate.from_dict({**body, "dry_run": True}),
                         idempotency_key=str(uuid.uuid4())).plan
print(plan.total, "persistent servers,", plan.players, "players on them")

# After a person has seen the plan
result = bulk_restart.sync(client=gs, body=BulkActionRestartCreate.from_dict(body), idempotency_key=str(uuid.uuid4()))
print(result.bulk_action.id, result.bulk_action.status)
```

Each persistent server counts as back once its ready check passes and it keeps running for 30 seconds (`healthy_for`). The next one waits 30 seconds more (`pause`). By default the first failure stops the restart and leaves the rest as they are; `max_failures` allows more. A stopped persistent server is skipped, and a failed one is started again. See [bulk actions](https://studio.survivalservers.com/docs/guides/bulk-actions/).

Your key needs the `persistent_servers:write` scope.
