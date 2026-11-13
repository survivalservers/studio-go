# Show cost this month

> Read the vCPU-hours used this month, what they cost, and where the month is heading.

`GET /v0/usage` covers this month by default. `totals` has the sum, and `rows` the days.

```ts
const api = 'https://studio.survivalservers.com/v0';
const res = await fetch(`${api}/usage`, {
  headers: { Authorization: `Bearer ${process.env.GS_API_KEY}` },
});
const usage = await res.json();
if (!res.ok) throw new Error(`${res.status} ${usage.code}: ${usage.detail} (${usage.request_id})`);
const { totals } = usage;
console.log(totals.cpu_hours, 'vCPU-hours');
console.log('$' + totals.amount_usd, 'so far, $' + totals.projected_month_usd, 'by the end of the month');
```

```python
import json, os, urllib.error, urllib.request

api = "https://studio.survivalservers.com/v0"
req = urllib.request.Request(f"{api}/usage")
req.add_header("Authorization", f"Bearer {os.environ['GS_API_KEY']}")
try:
    with urllib.request.urlopen(req) as r:
        usage = json.load(r)
except urllib.error.HTTPError as e:
    problem = json.load(e)
    raise SystemExit(f"{e.code} {problem['code']}: {problem.get('detail')} ({problem['request_id']})")
totals = usage["totals"]
print(totals["cpu_hours"], "vCPU-hours")
print(f"${totals['amount_usd']} so far, ${totals['projected_month_usd']} by the end of the month")
```

With our TypeScript and Python clients:

```ts
import { createClient, unwrap } from '@survivalservers/studio';

const gs = createClient({ baseUrl: 'https://studio.survivalservers.com', apiKey: process.env.GS_API_KEY! });
const { totals } = await unwrap(gs.GET('/v0/usage'));
console.log(totals.cpu_hours, 'vCPU-hours');
console.log('$' + totals.amount_usd, 'so far, $' + totals.projected_month_usd, 'by the end of the month');
```

```python
import os

from survivalservers_studio import AuthenticatedClient
from survivalservers_studio.api.usage_and_billing import get_usage

gs = AuthenticatedClient(base_url="https://studio.survivalservers.com", token=os.environ["GS_API_KEY"])
totals = get_usage.sync(client=gs).totals
print(totals.cpu_hours, "vCPU-hours")
print(f"${totals.amount_usd} so far, ${totals.projected_month_usd} by the end of the month")
```

`amount_usd` is the bill so far, after free vCPU-hours and credits. `projected_month_usd` is the whole month at the pace so far; in a month's first three days it is the spend so far. Amounts are strings, so they keep their exact value. See [usage and invoices](https://studio.survivalservers.com/docs/account/usage-and-invoices/).

Your key needs the `usage:read` scope.
