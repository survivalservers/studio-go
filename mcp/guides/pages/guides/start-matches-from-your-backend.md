# Start from your backend

> The calls your game backend makes for each match: start, hand the address to players, then stop.

Your game backend starts one game server per match with `POST /v0/game-servers`. The game client never calls the API: it gets the address from your backend, and only your backend holds an API key.

## One match, step by step

1. Your matchmaker makes a match, with each player's pings to our regions (see [regions and placement](https://studio.survivalservers.com/docs/concepts/regions-and-placement/)).
2. Your backend starts a game server, with the match ID as the `Idempotency-Key` and as `external_id`.
3. The reply has the address: send it to the players.
4. A start from a ready server is `running` in the reply. A fresh start is `starting`, and the `game_server.running` webhook fires when its ready check passes.
5. When the match ends, the game server stops itself: `POST /v0/self/stop` on the local API, or exit code 0. The `game_server.stopped` webhook fires with `exit.reason` `self`.

Your backend can also stop it with `POST /v0/game-servers/{id}/stop`.

```http
POST /v0/game-servers
Authorization: Bearer $API_KEY
Idempotency-Key: match-88231
Content-Type: application/json

{
  "app": "breakwire-windows",
  "branch": "live",
  "placement": { "player_pings": { "p_1": { "europe-west": 31, "europe-central": 44 }, "p_2": { "europe-west": 52, "europe-central": 29 } }, "max_ping_ms": 80 },
  "external_id": "88231",
  "labels": { "match_id": "88231", "queue": "ranked" },
  "env": { "MATCH_MODE": "ranked" },
  "ttl": "2h"
}
```

```json
{
  "id": "gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2",
  "status": "running",
  "start_mode": "warm",
  "region": "europe-west",
  "address": { "ipv4": "203.0.113.24", "ipv6": null, "ports": { "game": { "port": 31042, "protocol": "udp" } } }
}
```

- The reply is the whole game server object; the one above is trimmed.
- Only `app` is required when the app has a `default_placement`. Without one, send `placement` too: a start with neither answers `422`. Without `version` or `branch`, the start runs the `live` branch.
- A fresh start gets `env` and `args` at launch. A start from a ready server gets them as allocation data (see [Environment variables and allocation](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/)).
- `ttl` stops the game server after that long, with `exit.reason` `ttl`. The machine enforces it, so it holds even if the machine loses contact with us.
- `webhook_url` sends this game server's events to one more address (see [webhooks](https://studio.survivalservers.com/docs/guides/webhooks/)).

## Use the match ID as the key

The same call sent twice (a timeout, a retry, a second backend instance) must not start two game servers. With the match ID as the `Idempotency-Key`, a repeat within 24 hours gets the first game server back and starts nothing. That reply carries `Idempotent-Replayed: true`.

- The same key with a different body is refused with `409 idempotency_key_reused`.
- A repeat sent while the first start is still running gets `409 idempotency_key_in_use` with `Retry-After: 1`. Wait, then send it again.

## Choose the start mode

| `start_mode` | Starts |
|---|---|
| `auto` (default) | From a ready server if one matches, else fresh |
| `warm` | From a ready server only; `503 capacity_unavailable` if none |
| `cold` | Always a new process |

Use `warm` when a match must be joinable at once or not start at all. See [ready servers](https://studio.survivalservers.com/docs/concepts/ready-servers/).

## Errors to handle

| Answer | What to do |
|---|---|
| `503 capacity_unavailable` | Try again later, or allow more regions |
| `503 unavailable` | Retry after `Retry-After` seconds |
| `409 version_not_ready` | Retry later. If it was removed, use a branch |
| `409 idempotency_key_in_use` | Retry in a moment, same key |
| `409 idempotency_key_reused` | Use a new key for a new start |
| `402 card_required` | Add a card in the panel |
| `403 limit_reached` | Stop game servers, or [talk to us](https://studio.survivalservers.com/#talk) to raise it |
| `400 bad_request` | An unknown field or bad JSON: see `errors` |
| `422 validation_failed` | Fix the fields listed in `errors` |
| `429 rate_limited` | Wait `Retry-After` seconds, same key |
| `500 internal` | Send the same call again, same key |

- `capacity_unavailable`: no allowed region had room. `regions_tried` gives each region's reason: `no_capacity`, `version_not_ready`, `version_removed`, `not_offered`, `over_max_ping` or `unavailable`.
- `over_max_ping` comes only with `"fallback": false` in `placement`. By default, when no region is within `max_ping_ms`, the start goes to the best region with room.
- A start placed that way carries `placement.fallback_used: true` in the reply, with the worst player's ping in `placement.worst_ping_ms`.
- `version_not_ready`: no region this start may use has the version. Usually it is still being copied, so retry later. A region in `regions_tried` with `version_removed` will not get it by waiting: the machines there removed it (it is not one of the app's three newest versions and nothing uses it). Put it on a branch to have it copied again.
- `card_required`: there is no card on file, or a payment is 14 days overdue. Add a card in the panel to start game servers. Nothing running is stopped at that point. If an invoice is still unpaid 30 days after the failed payment or its due date, we may stop your running game servers with at least 7 days' notice. Persistent servers are saved and backed up first, and never deleted for non-payment.
- `limit_reached`: the studio's running vCPUs would pass its limit. By default that is 512. Ready servers waiting in your pools count, and so do persistent servers from the moment they are asked to start.

Every error is a problem details body with a stable `code`, a `detail` and a `request_id`.

## Fill running matches

When the game server reports its players (`PUT /v0/self/players` on the local API), your backend can find one with room instead of starting a new one:

```http
GET /v0/game-servers?app=breakwire-windows&status=running&label=queue:ranked&players_below=10
Authorization: Bearer $API_KEY
```

`label` takes `key:value` and can repeat; every label must match.

## Examples by language

Each function starts one game server with the match ID as the key and returns the address, with nothing installed. Keep the API key in your backend's secrets, never in a client. Errors and retries: [call the API from code](https://studio.survivalservers.com/docs/guides/api-clients/).

```ts
async function startMatch(matchId: string, pings: Record<string, Record<string, number>>): Promise<string> {
	const res = await fetch('https://studio.survivalservers.com/v0/game-servers', {
		method: 'POST',
		headers: { Authorization: `Bearer ${process.env.GS_API_KEY}`, 'Idempotency-Key': `match-${matchId}`, 'Content-Type': 'application/json' },
		body: JSON.stringify({ app: 'breakwire-windows', branch: 'live', placement: { player_pings: pings }, external_id: matchId }),
	});
	const body = await res.json();
	if (!res.ok) throw new Error(`${res.status} ${body.code}: ${body.detail}`);
	return `${body.address.ipv4}:${body.address.ports.game.port}`;
}
```

```csharp
using System;
using System.Collections.Generic;
using System.Net.Http;
using System.Net.Http.Json;
using System.Text.Json;
using System.Threading.Tasks;

public static class Matches
{
    public static async Task<string> StartMatch(HttpClient http, string matchId, Dictionary<string, Dictionary<string, int>> pings)
    {
        using var req = new HttpRequestMessage(HttpMethod.Post, "https://studio.survivalservers.com/v0/game-servers");
        req.Headers.Add("Authorization", "Bearer " + Environment.GetEnvironmentVariable("GS_API_KEY"));
        req.Headers.Add("Idempotency-Key", "match-" + matchId);
        req.Content = JsonContent.Create(new { app = "breakwire-windows", branch = "live", placement = new { player_pings = pings }, external_id = matchId });
        using var res = await http.SendAsync(req);
        var body = await res.Content.ReadFromJsonAsync<JsonElement>();
        if (!res.IsSuccessStatusCode) throw new Exception($"{(int)res.StatusCode} {body.GetProperty("code")}: {body.GetProperty("detail")}");
        var address = body.GetProperty("address");
        return $"{address.GetProperty("ipv4").GetString()}:{address.GetProperty("ports").GetProperty("game").GetProperty("port").GetInt32()}";
    }
}
```

```go
package matches

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

func startMatch(ctx context.Context, matchID string, pings map[string]map[string]int) (string, error) {
	body, err := json.Marshal(map[string]any{"app": "breakwire-windows", "branch": "live",
		"placement": map[string]any{"player_pings": pings}, "external_id": matchID})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://studio.survivalservers.com/v0/game-servers", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("GS_API_KEY"))
	req.Header.Set("Idempotency-Key", "match-"+matchID)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var gs struct {
		Code    string `json:"code"`
		Detail  string `json:"detail"`
		Address struct {
			IPv4  string `json:"ipv4"`
			Ports map[string]struct {
				Port int `json:"port"`
			} `json:"ports"`
		} `json:"address"`
	}
	if err := json.NewDecoder(res.Body).Decode(&gs); err != nil {
		return "", err
	}
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("%d %s: %s", res.StatusCode, gs.Code, gs.Detail)
	}
	return fmt.Sprintf("%s:%d", gs.Address.IPv4, gs.Address.Ports["game"].Port), nil
}
```

```python
import json
import os
import urllib.error
import urllib.request

def start_match(match_id: str, pings: dict) -> str:
    req = urllib.request.Request(
        "https://studio.survivalservers.com/v0/game-servers",
        method="POST",
        headers={"Authorization": f"Bearer {os.environ['GS_API_KEY']}", "Idempotency-Key": f"match-{match_id}",
                 "Content-Type": "application/json"},
        data=json.dumps({"app": "breakwire-windows", "branch": "live", "placement": {"player_pings": pings},
                         "external_id": match_id}).encode(),
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as res:
            body = json.load(res)
    except urllib.error.HTTPError as e:
        problem = json.load(e)
        raise RuntimeError(f"{e.code} {problem.get('code')}: {problem.get('detail')}") from None
    return f"{body['address']['ipv4']}:{body['address']['ports']['game']['port']}"
```

For Unity Matchmaker, a Cloud Code module makes this call for every match: see [Unity Matchmaker](https://studio.survivalservers.com/docs/switching/unity-matchmaker/).
