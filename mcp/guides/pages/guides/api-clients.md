# Call the API from code

> Call the API from your backend or tools with the HTTP client your language already has, with examples in TypeScript, Python, C# and Go.

The API is HTTPS and JSON, so the HTTP client your language already has is all you need. Nothing needs installing.

## Requests

- `https://studio.survivalservers.com/v0` is the address every call starts with.
- Send your API key as `Authorization: Bearer <key>`. Keep the key in your backend's secrets, never in a game client or a web page.
- Send bodies as JSON, with `Content-Type: application/json`.
- Send a stable `Idempotency-Key` on any write you may retry, such as a match ID on a start (see [start from your backend](https://studio.survivalservers.com/docs/guides/start-matches-from-your-backend/)).

## Errors

A refused call answers with the API's problem details (`application/problem+json`): `status`, `code`, `detail` and `request_id`. Quote `request_id` to [support](https://studio.survivalservers.com/docs/help/get-help/). Over a rate limit the answer is `429 rate_limited`, with `Retry-After`.

## Start a game server

Each example starts a game server of `breakwire-windows` with the match ID as the key, then prints the address players join.

### TypeScript

```ts
const res = await fetch('https://studio.survivalservers.com/v0/game-servers', {
  method: 'POST',
  headers: {
    Authorization: `Bearer ${process.env.GS_API_KEY}`,
    'Content-Type': 'application/json',
    'Idempotency-Key': 'match-88231',
  },
  body: JSON.stringify({ app: 'breakwire-windows' }),
});
const server = await res.json();
if (!res.ok) throw new Error(`${res.status} ${server.code}: ${server.detail} (${server.request_id})`);
console.log(`${server.address.ipv4}:${server.address.ports.game.port}`);
```

### Python

```python
import json, os, urllib.error, urllib.request

req = urllib.request.Request("https://studio.survivalservers.com/v0/game-servers", json.dumps({"app": "breakwire-windows"}).encode(), {
    "Authorization": f"Bearer {os.environ['GS_API_KEY']}",
    "Content-Type": "application/json",
    "Idempotency-Key": "match-88231",
})
try:
    with urllib.request.urlopen(req) as r:
        server = json.load(r)
except urllib.error.HTTPError as e:
    problem = json.load(e)
    raise SystemExit(f"{e.code} {problem['code']}: {problem.get('detail')} ({problem['request_id']})")
print(f"{server['address']['ipv4']}:{server['address']['ports']['game']['port']}")
```

### C#

```csharp
using System;
using System.Net.Http;
using System.Net.Http.Json;
using System.Text.Json;

using var http = new HttpClient();
using var req = new HttpRequestMessage(HttpMethod.Post, "https://studio.survivalservers.com/v0/game-servers");
req.Headers.Add("Authorization", "Bearer " + Environment.GetEnvironmentVariable("GS_API_KEY"));
req.Headers.Add("Idempotency-Key", "match-88231");
req.Content = JsonContent.Create(new { app = "breakwire-windows" });
using var res = await http.SendAsync(req);
var body = await res.Content.ReadFromJsonAsync<JsonElement>();
if (!res.IsSuccessStatusCode)
{
    Console.Error.WriteLine($"{(int)res.StatusCode} {body}"); // the problem details: code, detail, request_id
    return;
}
var address = body.GetProperty("address");
Console.WriteLine($"{address.GetProperty("ipv4").GetString()}:{address.GetProperty("ports").GetProperty("game").GetProperty("port").GetInt32()}");
```

### Go

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	req, err := http.NewRequest(http.MethodPost, "https://studio.survivalservers.com/v0/game-servers", bytes.NewBufferString(`{"app": "breakwire-windows"}`))
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("GS_API_KEY"))
	req.Header.Set("Idempotency-Key", "match-88231")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()
	var server struct {
		Code      string `json:"code"`
		Detail    string `json:"detail"`
		RequestID string `json:"request_id"`
		Address   struct {
			IPv4  string `json:"ipv4"`
			Ports map[string]struct {
				Port int `json:"port"`
			} `json:"ports"`
		} `json:"address"`
	}
	if err := json.NewDecoder(res.Body).Decode(&server); err != nil {
		log.Fatal(err)
	}
	if res.StatusCode >= 300 {
		log.Fatalf("%d %s: %s (%s)", res.StatusCode, server.Code, server.Detail, server.RequestID)
	}
	fmt.Printf("%s:%d\n", server.Address.IPv4, server.Address.Ports["game"].Port)
}
```

[Start matches from your backend](https://studio.survivalservers.com/docs/guides/start-matches-from-your-backend/) has the same call with player pings, and the [tasks](https://studio.survivalservers.com/docs/tasks/list-game-servers/) show other calls in TypeScript and Python.

## Client libraries

Use the API client libraries to call the API from your game backend or tools. The TypeScript and Python packages are generated from the API file, so every call, field and error is typed. Go has a smaller hand-written client. In October 2026, each of these clients started, listed and stopped a game server.

### Packages

| Language | Package | Needs |
|---|---|---|
| TypeScript | `@survivalservers/studio` | Node 20+, Deno or Bun |
| Python | `survivalservers-studio` | Python 3.11+ |
| Go | `github.com/survivalservers/studio-go` | Go 1.25 |

```bash
npm install @survivalservers/studio
pip install survivalservers-studio
go get github.com/survivalservers/studio-go
```

- The TypeScript package also runs in a browser, but a browser must never hold an API key.
- Python imports the package as `survivalservers_studio`.
- [studio-packages](https://github.com/survivalservers/studio-packages) holds the source of the TypeScript, Python and Go clients and of the command-line tool.

### Create the client

| Language | Create the client |
|---|---|
| TypeScript | `createClient({ baseUrl, apiKey })` |
| Python | `AuthenticatedClient(base_url, token)` |
| Go | `client.New(baseURL, key, nil, agent)` |

- Pass `https://studio.survivalservers.com` to each client.

Each client hands a refusal's problem details back in its own way:

- TypeScript: `unwrap` throws `ApiError`.
- Python: the call returns a `Problem`.
- Go: the call returns a `*client.Problem` error.

### What each package has

- **TypeScript.** Every path, body and answer is typed from the API file, on `openapi-fetch`. `unwrap` returns the answer or throws `ApiError` with `status`, `code`, `problem`, `requestId` and `retryAfter`. `idempotencyKey()` makes a fresh key. Fields with a default in the API file, such as `start_mode`, are optional.
- **Python.** Each call is a module under `survivalservers_studio.api`, grouped by area, such as `survivalservers_studio.api.game_servers`. Each module has `sync`, `sync_detailed`, `asyncio` and `asyncio_detailed`. `sync` returns the object or a `Problem`. The `_detailed` forms add the status code and headers.
- **Go.** It sends the JSON you give it and decodes the answer into your own types. It adds a random `Idempotency-Key` to a `POST` or `PATCH` that has none. Its `Problem` message names the next step to take.

### Start a game server with a client

#### TypeScript

```ts
import { createClient, unwrap, ApiError } from '@survivalservers/studio';

const gs = createClient({ baseUrl: 'https://studio.survivalservers.com', apiKey: process.env.GS_API_KEY! });
try {
	const server = await unwrap(gs.POST('/v0/game-servers', {
		params: { header: { 'Idempotency-Key': 'match-88231' } },
		body: { app: 'breakwire-windows' },
	}));
	console.log(`${server.address.ipv4}:${server.address.ports.game.port}`);
} catch (e) {
	if (!(e instanceof ApiError)) throw e;
	console.error(e.status, e.code, e.message, e.requestId);
}
```

#### Python

```python
import os
from survivalservers_studio import AuthenticatedClient
from survivalservers_studio.api.game_servers import start_game_server
from survivalservers_studio.models import GameServer, GameServerCreate

gs = AuthenticatedClient(base_url="https://studio.survivalservers.com", token=os.environ["GS_API_KEY"])
server = start_game_server.sync(client=gs, body=GameServerCreate.from_dict({"app": "breakwire-windows"}),
                                idempotency_key="match-88231")
if not isinstance(server, GameServer):
    raise RuntimeError(f"{server.code}: {server.detail} ({server.request_id})")
print(f"{server.address.ipv4}:{server.address.ports['game'].port}")
```

#### Go

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/survivalservers/studio-go"
)

func main() {
	gs, err := client.New("https://studio.survivalservers.com", os.Getenv("GS_API_KEY"), nil, "breakwire-backend/2.3")
	if err != nil {
		log.Fatal(err)
	}
	var server struct {
		Address struct {
			IPv4  string `json:"ipv4"`
			Ports map[string]struct {
				Port int `json:"port"`
			} `json:"ports"`
		} `json:"address"`
	}
	err = gs.JSON(context.Background(), client.Call{Method: http.MethodPost, Path: "/v0/game-servers",
		IdempotencyKey: "match-88231", Body: map[string]any{"app": "breakwire-windows"}}, &server)
	if err != nil {
		log.Fatal(err) // a refusal is a *client.Problem: its message has the code, detail, request ID and next step
	}
	fmt.Printf("%s:%d\n", server.Address.IPv4, server.Address.Ports["game"].Port)
}
```

The Go client also has `UploadZip` (a Windows build in parts), `Wait` and `CheckVersion`. The [command-line tool](https://studio.survivalservers.com/docs/guides/command-line/) is built on it.
