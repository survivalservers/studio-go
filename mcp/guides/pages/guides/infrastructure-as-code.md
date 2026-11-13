# Infrastructure as code

> Keep your apps, versions, branches, pools and webhook endpoints in your repo, reviewed like code and applied from CI.

Keep your game's setup in your repo, reviewed like the rest of your code, and apply it from CI. Each file is the JSON body the API takes, sent with `curl` from a pipeline step. You need nothing else installed.

## The files and their calls

Your job makes each part with these calls:

- **The app.** `POST /v0/apps` once, then `PATCH /v0/apps/{app}` to change it.
- **A version.** `POST /v0/apps/{app}/versions`, once for each new version.
- **A branch.** `PUT /v0/apps/{app}/branches/{branch}` with `{"version": "2.3.0"}`.
- **A pool.** `PUT /v0/apps/{app}/pools/{region}`.
- **A webhook endpoint.** `POST /v0/webhook-endpoints` once, then `PATCH /v0/webhook-endpoints/{id}` to change it.

A re-run of the same job makes nothing twice:

- A `PUT` sets the branch or the pool to what the file says, so sending the same file again changes nothing.
- A version never changes once made, apart from `deprecated` and its `regions`. A new build or new settings is a new version with a new name.
- Send an `Idempotency-Key` with each `POST`, such as the version's name.

Give the CI key `apps:read` and `apps:write`, and `webhooks:write` for webhook endpoints, limited to the apps it manages.

## A pipeline step

This GitHub Actions step keeps the key in the repository's secrets and sends two files from `settings/`:

```yaml
- name: Apply the game server settings
  env:
    API_KEY: ${{ secrets.GAME_SERVERS_API_KEY }}
  run: |
    api=https://studio.survivalservers.com/v0/apps/breakwire-linux
    auth="Authorization: Bearer $API_KEY"
    json="Content-Type: application/json"
    curl -sS --fail -X PUT "$api/branches/live" -H "$auth" -H "$json" -d @settings/branch-live.json
    curl -sS --fail -X PUT "$api/pools/europe-west" -H "$auth" -H "$json" -d @settings/pool-europe-west.json
```

`settings/pool-europe-west.json`:

```json
{ "branch": "live", "size": { "cpu": 2, "memory_gb": 4 }, "mode": "auto", "min": 2, "max": 20 }
```

To ship a new build in the same job, upload it first: [from your CI](https://studio.survivalservers.com/docs/guides/upload-builds/#from-your-ci).
