# Switching from Edgegap

> Point your Edgegap deployment calls at our address: deploy, status, stop and self-stop keep working, and your image keeps its ARBITRIUM variables.

Your backend can keep making its Edgegap deployment calls here: change the base address and the token. Deploy, status, stop, list, bulk stop and your game server's own self-stop work. Your container image runs as it is and still finds its `ARBITRIUM_*` variables.

In October 2026, Edgegap's documented requests started, read, listed, stopped and bulk-stopped real game servers through this address. Deployments were placed by IP address and by coordinates, and a deployment was stopped with its own delete URL and token, sent as its game server sends them. In November 2026, Edgegap's own server SDK (`@edgegap/sdk` 1.1.0) read its deployment and match settings inside a game server here.

## Change the address

Point your backend's Edgegap base address at `https://studio.survivalservers.com/compat/edgegap`, and send one of our API keys where you sent Edgegap's token:

```bash
curl -X POST "https://studio.survivalservers.com/compat/edgegap/v2/deployments" \
  -H "Authorization: token $API_KEY" -H "Content-Type: application/json" \
  -d '{"application": "your-game", "version": "2.3.0",
       "users": [{"user_type": "ip_address", "user_data": {"ip_address": "198.51.100.7"}}],
       "environment_variables": [{"key": "MODE", "value": "ranked", "is_hidden": false}],
       "tags": ["ranked"]}'
```

The reply is Edgegap's, with `202`:

```json
{ "request_id": "9f511e17a2c4" }
```

- The key goes as `token <key>` (the form Edgegap's docs give), as `Bearer <key>` or bare.
- The request ID has 12 characters, in Edgegap's shape.
- Each call becomes a call on our own API with your key, so the key's scopes, app limits and rate limits apply.

## How the terms map

| Edgegap | Survival Servers Studio |
|---|---|
| Application | App (its slug or ID) |
| Application version | Version |
| Deployment | Game server, started fresh |
| Request ID | `external_id` |
| Users | `placement.player_pings` |
| Webhooks | [Webhooks](https://studio.survivalservers.com/docs/guides/webhooks/) |

## Your image

1. Create a Linux app whose slug is your Edgegap application name, and a build from the image your application version uses, from any registry:

   ```bash
   curl -X POST "https://studio.survivalservers.com/v0/apps" \
     -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
     -d '{"name": "Your Game", "slug": "your-game", "os": "linux"}'
   curl -X POST "https://studio.survivalservers.com/v0/apps/your-game/builds" \
     -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
     -d '{"os": "linux", "source": {"type": "image", "image": "registry.example.com/you/your-server:2.3.0"}}'
   ```

   The build is ready to use once `GET /v0/builds/{id}` shows `status` `ready`.
   With the command-line tool, which waits for the build:

   ```bash
   survivalservers apps create your-game --os linux
   survivalservers builds image your-game registry.example.com/you/your-server:2.3.0 --wait
   ```

2. Create a version named after your Edgegap version, with the same ports as `container_port`, the same size, and `env_aliases: ["edgegap"]`. Send it to `POST /v0/apps/your-game/versions`:

   ```json
   {
     "name": "2.3.0",
     "build": "<build-id>",
     "size": { "cpu": 1, "memory_gb": 2 },
     "launch": {
       "ports": [{ "name": "gameport", "protocol": "udp", "container_port": 7777 }],
       "ready_check": { "type": "port", "port": "gameport" },
       "env_aliases": ["edgegap"]
     }
   }
   ```

   A game whose port opens before its world has loaded needs a `log` ready check on the line it prints when ready.

3. Point the `live` branch at it:

   ```bash
   curl -X PUT "https://studio.survivalservers.com/v0/apps/your-game/branches/live" \
     -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" -d '{"version": "2.3.0"}'
   ```
   Or `survivalservers branches set your-game live 2.3.0`.

With `env_aliases: ["edgegap"]` each game server gets Edgegap's variables. Our own `GS_*` variables are there as well.

| Variable | Value |
|---|---|
| `ARBITRIUM_REQUEST_ID` | The request ID |
| `ARBITRIUM_PUBLIC_IP` | The public IP address |
| `ARBITRIUM_HOST_ID` | The machine's ID |
| `ARBITRIUM_DEPLOYMENT_TAGS` | The tags, comma-separated |
| `ARBITRIUM_PORT_<NAME>_INTERNAL` | The listening port |
| `ARBITRIUM_PORT_<NAME>_EXTERNAL` | The public port |
| `ARBITRIUM_PORT_<NAME>_PROTOCOL` | `UDP` or `TCP` |
| `ARBITRIUM_PORTS_MAPPING` | Every port, as JSON |
| `ARBITRIUM_DEPLOYMENT_LOCATION` | The region, as JSON |
| `ARBITRIUM_DEPLOYMENT_VCPU_UNITS` | vCPUs times 1024 |
| `ARBITRIUM_DEPLOYMENT_MEMORY_MB` | Memory in MiB |
| `ARBITRIUM_PRIVATE_FLEET_ID` | Always `PUBLIC_CLOUD` |
| `ARBITRIUM_HOST_IN_PRIVATE_FLEET` | Always `false` |
| `ARBITRIUM_DELETE_URL` | The self-stop address, kept secret |
| `ARBITRIUM_DELETE_TOKEN` | The self-stop token, kept secret |

- Port names keep their meaning: the port `gameport` gives `ARBITRIUM_PORT_GAMEPORT_*`. Any character but a letter or digit becomes `_`.
- `ARBITRIUM_DEPLOYMENT_LOCATION` holds the region's name, country, latitude and longitude. Its continent, division and time zone are empty.
- `ARBITRIUM_DELETE_URL` and `ARBITRIUM_DELETE_TOKEN` are set only for deployments made through this address.
- A game server your backend starts through our own API gets its own ID in `ARBITRIUM_REQUEST_ID` and no tags.

## Supported calls

| Edgegap call | Here |
|---|---|
| `POST /v2/deployments` | A fresh start placed by its users |
| `GET /v1/status/{request_id}` | The deployment's status |
| `DELETE /v1/stop/{request_id}` | A stop; `410` once stopped |
| `GET /v1/deployments` | Live deployments, filtered |
| `POST /v1/deployments/bulk-stop` | A stop for each match |
| `DELETE /v1/self/stop/{request_id}/{key}` | A self-stop |
| `GET /v1/ip` | The caller's IP address |

- **Deploy.** `application`, `version` and at least one user are required. `environment_variables`, `tags` and `resources` are optional. `is_hidden` values are kept secret. `resources` sets the size: `cpu_units` / 1024 vCPUs and `memory_mib` / 1024 GB.
- **Placement.** Each user's `geo_coordinates`, or `ip_address` located in a table, gives an estimated ping to each region. IP Geolocation by [DB-IP](https://db-ip.com). With no user located, the app's default placement applies.
- **Status.** `Status.DEPLOYING`, then `Status.READY` once the version's ready check passes, then `Status.TERMINATING` and `Status.TERMINATED`, or `Status.ERROR`.
- **List.** It shows the deployments made through this address that have not ended. `query` filters on `request_id`, `tags`, `application`, `version` and `status`, with `eq`, `neq`, `in` and `nin`, and orders by `created_at`. A filter on any other field is ignored. `page` and `limit` page it (10 by default, at most 100).
- **Bulk stop.** It takes the same fields, with tags as `deployment_tags` as Edgegap's bulk stop names them (`tags` works too). The reply is `202`, with each stopped deployment under `processable`. A filter on any other field answers `400` and stops nothing.
- **Self-stop.** The game server calls `ARBITRIUM_DELETE_URL` with `ARBITRIUM_DELETE_TOKEN` in `Authorization`, and no API key. It answers `202`, or `410` once stopped. A wrong token answers `401`.

When no machine can take a deployment, the deploy answers `422` with Edgegap's "couldn't allocate" message and the request ID. So does a version not yet ready in the regions the deployment may use. Your retry logic keeps working.

## Differences

- **Ready means ready.** Edgegap's docs call a deployment Ready once its container has started. Here `Status.READY` comes once the version's ready check passes, so a client polling for it connects when the game server can take players.
- **`fqdn` in Edgegap's server SDK.** The SDK makes `fqdn` from the request ID (`<request_id>.pr.edgegap.net`), which does not lead here. Use `publicIp`, or each port's `link`, which holds the IP address and public port.
- **No hostname per deployment.** `fqdn` and each port's `link` carry the IP address, and `tls_upgrade` is always `false`. WebSocket over TLS is not available yet.
- **Request IDs are ours**, in the same shape. Deployments made before the switch stay on Edgegap.
- **Webhooks are refused.** A deploy with `webhook_on_ready`, `webhook_on_error` or `webhook_on_terminated` answers `400`. Subscribe a webhook endpoint to `game_server.running`, `game_server.failed` and `game_server.stopped` instead: signed and retried, with a different body.

## What does not carry over

- **Applications and versions.** These calls answer `404`. Make apps, builds and versions with our API or the panel.
  The command-line tool `survivalservers` makes them too.
- **The container registry and caching.** `require_cached_locations` is ignored, because a version is copied ahead of time to every region it targets.
- **`ARBITRIUM_CONTEXT_URL` and its token, container logs, the deployment report and metrics.** Read the game server, its logs and its metrics on our API.
- **The v1 deploy call.** Use `POST /v2/deployments`.
- **Private fleets and host beacons.** Not offered. `ARBITRIUM_HOST_BASE_CLOCK_FREQUENCY` and the beacon variables are not set.
- **Relays, lobbies, matchmaking, the server browser and sessions.** Not available yet. If you used Edgegap's matchmaker, keep your matchmaker logic and call our start when it makes a match. For Unity Matchmaker, a Cloud Code module makes the two calls: see [Unity Matchmaker](https://studio.survivalservers.com/docs/switching/unity-matchmaker/). No module has run in a live Unity queue yet.

## Migration steps

1. Create the app, a build from your image and a version named after your Edgegap version, with `env_aliases: ["edgegap"]`, and point `live` at it.
2. Make an API key for your backend with `game_servers:write`, `game_servers:read` and `apps:read`, limited to the app. `apps:read` lets replies show container ports.
3. Change the backend's Edgegap base address and token.
4. Play a match on a test deployment, then move your players.

You can move to our own API call by call. A deployment is a game server (`POST /v0/game-servers`), and its address is in the start reply, so there is no status to poll. Every call is in the [API reference](https://studio.survivalservers.com/docs/reference/game-servers-and-regions/).

If you move from another provider, your first month is free up to $1,000 of use, and we move the game for you.

## Sources

- Edgegap's calls, fields and answers: [Edgegap's deployment API](https://docs.edgegap.com/docs/api/dedicated-servers), read October 8, 2026.
- The `token` header: [Edgegap API](https://docs.edgegap.com/docs/api), read October 8, 2026.
- Ready, the `ARBITRIUM_*` variables, list filters and webhooks: [Deployments](https://docs.edgegap.com/learn/orchestration/deployments), read October 8, 2026.
