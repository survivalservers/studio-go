# Webhooks

> Get signed HTTPS calls when game servers, persistent servers and builds change state, and check each signature.

A webhook is a signed HTTPS `POST` the API sends to your backend when a game server, persistent server, build, bulk action or bill changes. Each delivery is retried for 24 hours until your endpoint answers with a 2xx. To get alerts in a channel instead, see [Slack and Discord alerts](https://studio.survivalservers.com/docs/guides/slack-and-discord/).

## Add an endpoint

```http
POST /v0/webhook-endpoints
Authorization: Bearer $API_KEY
Content-Type: application/json

{ "url": "https://backend.kingsmoor.example/hooks/game-servers", "events": ["game_server.*", "persistent_server.*", "pool.exhausted"], "default": true }
```

The reply holds the endpoint's signing secret (`whsec_...`). It is shown only in this reply.

- `url` must be `https://`. Redirects are not followed. A delivery to an address that is not public fails.
- `events` takes exact types and `game_server.*` style wildcards. Leave it out, or send `["*"]`, for every event.
- `apps` and `regions` limit an endpoint to those apps and regions. Events with no app or region, such as billing, still arrive.
- `default: true` marks the endpoint whose secret also signs per-request `webhook_url` deliveries. Marking another endpoint default unmarks it.
- A start (`POST /v0/game-servers`) or a persistent server can name its own `webhook_url`. It gets that object's events. A start with `webhook_url` is refused until one endpoint is the default. A persistent server's `webhook_url` deliveries give up without one.
- The key needs `webhooks:write` to add or change endpoints and `webhooks:read` to list them.

To manage an endpoint:

- `POST /v0/webhook-endpoints/{id}/test` sends a `test.ping`.
- `POST /v0/webhook-endpoints/{id}/rotate-secret` makes a new secret.
- `PATCH /v0/webhook-endpoints/{id}` changes `url`, `events`, `apps`, `regions`, `default`, `enabled` or `description`.
- `DELETE /v0/webhook-endpoints/{id}` removes the endpoint. Pending deliveries to it stop.

After a rotation the old secret keeps signing too for 24 hours, so each delivery carries both signatures. Switch your receiver to the new secret inside that day.

## Event types

| Type | When |
|---|---|
| `game_server.running` | Players can join |
| `game_server.stopped` | It ended normally |
| `game_server.failed` | It ended through a fault |
| `game_server.restarted` | Started again by its restart policy |
| `game_server.memory_high` | Over 90% of its memory for a minute |
| `game_server.disk_high` | Its files passed its disk size |
| `persistent_server.running` | It is running |
| `persistent_server.stopped` | It stopped |
| `persistent_server.failed` | It failed |
| `persistent_server.moved` | It runs on another machine now |
| `persistent_server.restored` | A restore finished |
| `persistent_server.backup_created` | A backup finished |
| `persistent_server.keep_limit_reached` | Past 20 kept backups |
| `persistent_server.backup_failed` | Its backups started failing |
| `backup.check_failed` | A backup failed its check |
| `backup.export_ready` | A backup's zip is ready |
| `bulk_action.started` | A bulk action began |
| `bulk_action.paused` | Its first targets ran; it waits |
| `bulk_action.stopped` | Too many targets failed; it stopped |
| `bulk_action.finished` | It ended |
| `build.ready`, `build.failed` | A build finished processing |
| `version.region_ready` | A version is ready in a region |
| `pool.exhausted` | A start found its pool empty |
| `pool.starts_failing` | A pool's new ready servers keep failing to start |
| `billing.budget_threshold` | Projected spend passed a threshold |
| `billing.free_hours` | Free vCPU-hours 80% or all used, with no card |
| `billing.invoice_created` | A month closed |
| `billing.payment_failed` | A payment failed or is past due |
| `region.status_changed` | A region went down or came back |
| `test.ping` | The test call |

- `game_server.running` fires as a start from a ready server is answered, and after the ready check for a fresh start.
- `game_server.failed` has `exit.reason` `crashed`, `out_of_memory`, `boot_timeout`, `health_timeout`, `machine_failure` or `lost_contact`. `out_of_memory` means it went over its memory size and was stopped for it.
- `lost_contact` means the machine stopped a persistent server's run because it lost contact with the platform.
- `game_server.restarted`: `data.crash` says how the run before ended.
- `game_server.memory_high` comes before an `out_of_memory` end: `data.memory_high` has `memory_mb`, `size_mb` and `since`. It comes once per run, and again only after the run has gone back under 80%.
- `game_server.disk_high` has `data.disk_high` with `used_mb`, `size_mb`, `free_mb` and `since`. It comes once per run until the files fall back under the size. Going on to 120% of it saves and stops the game server with the exit reason `out_of_disk`.
- `persistent_server.moved` comes instead of `persistent_server.running` when a persistent server is restored on another machine. It carries the new address.
- `build.failed` also fires when a ready Linux build's image is gone from our registry. That one has `data.sequence` 2.
- `persistent_server.backup_created` is not sent for continuous backups, which follow each save.
- `persistent_server.restored` fires when a restore is done and the persistent server is back.
- `persistent_server.keep_limit_reached` comes at most once a day, when a game asks to keep more than 20 backups of one persistent server.
- `persistent_server.backup_failed` comes when a persistent server's backups start failing, then at most once a day while they still fail. Its `data.backups_failing` has `reason`, `detail`, `failures` and `since` (see [when a backup fails](https://studio.survivalservers.com/docs/guides/back-up-and-restore/#when-a-backup-fails)).
- `backup.check_failed` fires when a backup did not restore whole in its check, or is under half the size of the one before.
- `bulk_action.paused` comes after its `rollout.first` targets ran and their `wait` passed. `POST /v0/bulk-actions/{id}/resume` takes it on to the rest.
- `bulk_action.stopped` means more targets failed than its `max_failures`. `bulk_action.finished` has `data.status` `succeeded`, `failed` or `cancelled`.
- `version.region_ready` fires once per version and region.
- `pool.exhausted` under `start_mode: auto` means the start was fresh instead.
- `pool.starts_failing` fires when 3 starts on a pool's version have failed in a row, and again with `data.held: true` at 10, when the pool stops starting that version. `data.last_failure` says how the newest ended.
- `billing.budget_threshold` is sent for each budget alert (`PUT /v0/budget-alerts`) the month passes.
- `billing.free_hours` is sent once a month at each step, `data.percent` 80 and 100, while your studio has no card. At 100 new game servers won't start until a card is added; nothing running stops.
- `billing.payment_failed` is sent once for each invoice whose payment failed, or that passed its due date unpaid (`data.past_due`). `data.starts_stop_at` says when new starts are refused if it stays unpaid.
- `region.status_changed` is sent to studios with game servers, persistent servers or ready servers in a region that goes down or comes back.
- `region.status_changed` has `data.status` `down` (new game servers may not start there) or `up`. Two down minutes in a row mean down, and three up minutes mean back up.

## Delivery format

```http
POST /hooks/gs HTTP/1.1
Content-Type: application/json
webhook-id: evt_a01JA8Z7V3B5N7M9Q1W3E5R7T9
webhook-timestamp: 1791571900
webhook-signature: v1,K5oZfzN95Z9UVu1EsfQmfVNQhnkZ2pj9o9NDN/H/pI4=

{"id": "evt_a01JA8Z7V3B5N7M9Q1W3E5R7T9", "type": "game_server.stopped", "created_at": "2026-10-09T18:51:40.231Z",
 "studio": "std_g01JA6R4T6Y8U0J2N4P6A8S0D2",
 "data": {"object": "game_server", "sequence": 5, "id": "gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2", "status": "stopped",
          "exit": {"code": 0, "reason": "self", "crashed": false, "killed": false}}}
```

`data` holds the object as the API shows it (trimmed here), plus `object` and `sequence`.

Answer with any 2xx within 10 seconds. Any other answer, or none, is tried again after about 5 seconds, 30 seconds, 2, 5, 15 and 30 minutes, then 1 hour. Then it is tried every 2 hours until 24 hours after the event. After that the delivery is `gave_up`.

An endpoint that keeps failing for 3 days, with no delivery succeeding in that time, is turned off. Its `status` becomes `disabled`, with a `disabled_reason`, and your studio's owners get an email. Our emails do not reach inboxes yet. Send `PATCH` with `"enabled": true` to turn it back on.

## Check the signature

The signature follows the Standard Webhooks scheme, so a Standard Webhooks library can check it too. The signed text is the three parts joined by periods:

```text
{webhook-id}.{webhook-timestamp}.{raw body}
```

- The key is the secret after `whsec_`, base64-decoded.
- `webhook-signature` is `v1,` and the base64 HMAC-SHA256. During a rotation it holds two, separated by a space.
- Check the raw body as it arrived, before you parse it. Refuse a `webhook-timestamp` more than 5 minutes off.

Each `receive` function below checks the signature, drops a repeat, then handles the event. Header names are not case-sensitive, and the samples read them in lower case.

```go
package hooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

func verify(secret string, h http.Header, body []byte) bool {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	ts, terr := strconv.ParseInt(h.Get("webhook-timestamp"), 10, 64)
	if err != nil || terr != nil || math.Abs(float64(time.Now().Unix()-ts)) > 300 {
		return false
	}
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%s.%s.", h.Get("webhook-id"), h.Get("webhook-timestamp"))
	mac.Write(body)
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	for _, part := range strings.Fields(h.Get("webhook-signature")) {
		if v, sig, ok := strings.Cut(part, ","); ok && v == "v1" && hmac.Equal([]byte(sig), []byte(want)) {
			return true
		}
	}
	return false
}

var seen sync.Map // webhook-id -> when it was handled; with several receivers, a shared store

func receive(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || !verify(os.Getenv("GS_WEBHOOK_SECRET"), r.Header, body) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	id := r.Header.Get("webhook-id")
	if _, repeat := seen.Load(id); repeat {
		w.WriteHeader(http.StatusOK) // already handled: a 2xx stops the retries
		return
	}
	// Handle the event here (its type and data are in body), then remember it; forget IDs after a day.
	seen.Store(id, time.Now())
	w.WriteHeader(http.StatusOK)
}
```

```ts
import { createHmac, timingSafeEqual } from 'node:crypto';

type Headers = Record<string, string | undefined>;

export function verify(secret: string, headers: Headers, rawBody: Buffer): boolean {
	const id = headers['webhook-id'];
	const ts = headers['webhook-timestamp'] ?? '';
	if (!id || !/^\d+$/.test(ts) || Math.abs(Date.now() / 1000 - Number(ts)) > 300) return false;
	const key = Buffer.from(secret.replace(/^whsec_/, ''), 'base64');
	const want = Buffer.from(createHmac('sha256', key).update(`${id}.${ts}.`).update(rawBody).digest('base64'));
	return (headers['webhook-signature'] ?? '').split(' ').some((part) => {
		const [version, sig] = part.split(',');
		const got = Buffer.from(sig ?? '');
		return version === 'v1' && got.length === want.length && timingSafeEqual(got, want);
	});
}

const seen = new Map<string, number>(); // webhook-id -> when it was handled; a shared store with several receivers

export function receive(headers: Headers, rawBody: Buffer): number {
	if (!verify(process.env.GS_WEBHOOK_SECRET ?? '', headers, rawBody)) return 401;
	const id = headers['webhook-id']!;
	if (seen.has(id)) return 200; // already handled: a 2xx stops the retries
	const event = JSON.parse(rawBody.toString('utf8'));
	// Handle event.type and event.data here, then remember the ID; forget IDs after a day.
	seen.set(id, Date.now());
	return 200;
}
```

```python
import base64, hashlib, hmac, json, os, time

def verify(secret: str, headers, raw_body: bytes) -> bool:
    msg_id = headers.get("webhook-id", "")
    ts = headers.get("webhook-timestamp", "")
    if not msg_id or not ts.isdigit() or abs(time.time() - int(ts)) > 300:
        return False
    key = base64.b64decode(secret.removeprefix("whsec_"))
    signed = f"{msg_id}.{ts}.".encode() + raw_body
    want = base64.b64encode(hmac.new(key, signed, hashlib.sha256).digest())
    for part in headers.get("webhook-signature", "").split():
        version, _, sig = part.partition(",")
        if version == "v1" and hmac.compare_digest(sig.encode(), want):
            return True
    return False

seen = {}  # webhook-id -> when it was handled; a shared store with several receivers

def receive(headers, raw_body: bytes) -> int:
    if not verify(os.environ["GS_WEBHOOK_SECRET"], headers, raw_body):
        return 401
    msg_id = headers["webhook-id"]
    if msg_id in seen:
        return 200  # already handled: a 2xx stops the retries
    event = json.loads(raw_body)
    # Handle event["type"] and event["data"] here, then remember the ID; forget IDs after a day.
    seen[msg_id] = time.time()
    return 200
```

```csharp
using System;
using System.Collections.Concurrent;
using System.Linq;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

public static class Webhooks
{
    public static bool Verify(string secret, string id, string timestamp, string signatures, byte[] rawBody)
    {
        if (string.IsNullOrEmpty(id) || !long.TryParse(timestamp, out var ts)) return false;
        if (Math.Abs(DateTimeOffset.UtcNow.ToUnixTimeSeconds() - ts) > 300) return false;
        var key = Convert.FromBase64String(secret.StartsWith("whsec_") ? secret[6..] : secret);
        using var hmac = new HMACSHA256(key);
        var signed = Encoding.UTF8.GetBytes($"{id}.{timestamp}.").Concat(rawBody).ToArray();
        var want = Encoding.UTF8.GetBytes(Convert.ToBase64String(hmac.ComputeHash(signed)));
        foreach (var part in (signatures ?? "").Split(' ', StringSplitOptions.RemoveEmptyEntries))
        {
            var pieces = part.Split(',', 2);
            if (pieces.Length == 2 && pieces[0] == "v1" &&
                CryptographicOperations.FixedTimeEquals(Encoding.UTF8.GetBytes(pieces[1]), want))
                return true;
        }
        return false;
    }

    // webhook-id -> when it was handled; a shared store with several receivers
    static readonly ConcurrentDictionary<string, DateTimeOffset> Seen = new();

    public static int Receive(string secret, string id, string timestamp, string signatures, byte[] rawBody)
    {
        if (!Verify(secret, id, timestamp, signatures, rawBody)) return 401;
        if (Seen.ContainsKey(id)) return 200; // already handled: a 2xx stops the retries
        using var evt = JsonDocument.Parse(rawBody);
        // Handle evt.RootElement's "type" and "data" here, then remember the ID; forget IDs after a day.
        Seen[id] = DateTimeOffset.UtcNow;
        return 200;
    }
}
```

## Duplicates and ordering

Delivery is at least once, and messages can arrive out of order. The same message can arrive twice, for example when your endpoint answered after 10 seconds. An older state can arrive after a newer one.

- **Drop repeats by `webhook-id`.** It is the event's ID and stays the same on every try. Keep handled IDs for a day and answer a repeat with a 2xx. With several receivers, keep the IDs in a store they share.
- **Use `data.sequence`.** It counts up per object. Ignore an event whose `sequence` is lower than the last one you applied for that object.

## Past deliveries and retries

```http
GET /v0/webhook-deliveries?endpoint=whe_...&status=failed
Authorization: Bearer $API_KEY
```

The list shows every attempt, newest first, with `http_status`, `duration_ms`, the first 512 bytes of your answer (`response_snippet`) and `next_attempt_at`. Filter by `endpoint`, `event`, `status` (`pending`, `succeeded`, `failed`, `gave_up`), `type` or `format`. `POST /v0/webhook-deliveries/{id}/retry` sends that event again now, as a new attempt. The panel shows the same list.

## Watch the event stream

`GET /v0/events/stream` sends the same payloads as server-sent events, with no retries. Pass `types=game_server.*,persistent_server.*` to narrow it.

- Send `Last-Event-ID` to carry on after a break. Events are kept 24 hours for this.
- After a resume, events from the few seconds before `Last-Event-ID` can come again. Drop repeats by event ID.
- A browser's `EventSource` cannot send the `Authorization` header. Read the stream with `fetch()` and a stream reader.

The stream also carries the watching events below. They are never sent to webhook endpoints, whatever their `events` say.

| Type | When |
|---|---|
| `game_server.starting` | A fresh start, before players can join |
| `game_server.stopping` | A stop was asked for |
| `game_server.players_changed` | Its player count or list changed |
| `persistent_server.starting` | It is starting |
| `persistent_server.stopping` | It is stopping |
| `persistent_server.paused` | It paused |
| `persistent_server.resuming` | It is resuming |
| `persistent_server.resumed` | It is running again |
| `bulk_action.progress` | Its counts or its step changed |
