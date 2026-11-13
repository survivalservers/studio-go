# Ban a player everywhere

> Send one ban command to every persistent server of a game, with a dry run first.

A bulk action sends one command to every persistent server a selector matches. Run it with `dry_run` first, and send it only after a person has seen the plan.

```ts
const api = 'https://studio.survivalservers.com/v0';
const headers = { Authorization: `Bearer ${process.env.GS_API_KEY}`, 'Content-Type': 'application/json' };
async function post(body: object) {
  const res = await fetch(`${api}/bulk-actions/commands`, {
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
  command: { type: 'console', line: 'ban Vex_77' },
};
const { plan } = await post({ ...body, dry_run: true });
console.log(plan.total, 'persistent servers,', plan.skipped.length, 'skipped');

// After a person has seen the plan
const { bulk_action } = await post(body);
console.log(bulk_action?.id, bulk_action?.status);
```

```python
import json, os, urllib.error, urllib.request, uuid

api = "https://studio.survivalservers.com/v0"

def post(body):
    headers = {"Authorization": f"Bearer {os.environ['GS_API_KEY']}", "Content-Type": "application/json"}
    req = urllib.request.Request(f"{api}/bulk-actions/commands", json.dumps(body).encode(), {**headers, "Idempotency-Key": str(uuid.uuid4())})
    try:
        with urllib.request.urlopen(req) as r:
            return json.load(r)
    except urllib.error.HTTPError as e:
        problem = json.load(e)
        raise SystemExit(f"{e.code} {problem['code']}: {problem.get('detail')} ({problem['request_id']})")

body = {
    "selector": {"target": "persistent_servers", "app": "brinewatch-linux"},
    "command": {"type": "console", "line": "ban Vex_77"},
}
plan = post({**body, "dry_run": True})["plan"]
print(plan["total"], "persistent servers,", len(plan["skipped"]), "skipped")

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
  command: { type: 'console' as const, line: 'ban Vex_77' },
};
const { plan } = await unwrap(gs.POST('/v0/bulk-actions/commands', {
  params: { header: { 'Idempotency-Key': idempotencyKey() } },
  body: { ...body, dry_run: true },
}));
console.log(plan.total, 'persistent servers,', plan.skipped.length, 'skipped');

// After a person has seen the plan
const { bulk_action } = await unwrap(gs.POST('/v0/bulk-actions/commands', {
  params: { header: { 'Idempotency-Key': idempotencyKey() } },
  body,
}));
console.log(bulk_action?.id, bulk_action?.status);
```

```python
import os
import uuid

from survivalservers_studio import AuthenticatedClient
from survivalservers_studio.api.bulk_actions import send_bulk_command
from survivalservers_studio.models import BulkActionCommandCreate

gs = AuthenticatedClient(base_url="https://studio.survivalservers.com", token=os.environ["GS_API_KEY"])
body = {
    "selector": {"target": "persistent_servers", "app": "brinewatch-linux"},
    "command": {"type": "console", "line": "ban Vex_77"},
}
plan = send_bulk_command.sync(client=gs, body=BulkActionCommandCreate.from_dict({**body, "dry_run": True}),
                              idempotency_key=str(uuid.uuid4())).plan
print(plan.total, "persistent servers,", len(plan.skipped), "skipped")

# After a person has seen the plan
result = send_bulk_command.sync(client=gs, body=BulkActionCommandCreate.from_dict(body), idempotency_key=str(uuid.uuid4()))
print(result.bulk_action.id, result.bulk_action.status)
```

A persistent server that is stopped or paused is skipped, with `not_running` or `paused` as its reason in `plan.skipped`. The ban reaches only the persistent servers running now. `GET /v0/bulk-actions/{id}/replies` lists what each game answered.

Your key needs the `persistent_servers:write` scope.
