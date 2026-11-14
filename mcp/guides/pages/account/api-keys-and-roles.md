# API keys and roles

> Create API keys with only the scopes they need, and give each person in your studio the right role.

API keys are for your backend or build pipeline. Never put a key in your game build: players can extract it. People sign in to the panel instead, and each person gets the scopes of their role.

<figure class="key-map not-content" aria-label="Which parts of your game hold an API key">
<div class="key-row"><p class="key-row-label">Call our API with a key</p>
<div class="key-card"><b>Your backend</b><span class="key-chip">Game backend key</span><span>Starts and stops game servers</span></div>
<div class="key-card"><b>Your build pipeline</b><span class="key-chip">Build key</span><span>Uploads new builds</span></div>
</div>
<div class="key-row"><p class="key-row-label">Never hold a key</p>
<div class="key-card"><b>Your game server</b><span class="key-chip is-none">No key</span><span>Calls the local API on its own machine</span></div>
<div class="key-card"><b>Players</b><span class="key-chip is-none">No key</span><span>Connect straight to the game server</span></div>
</div>
</figure>

## Create a key

Make your first key in the panel under **Developers**, then **API keys**. After that, a key with the `keys:write` scope can make more:

```bash
curl -X POST "https://studio.survivalservers.com/v0/api-keys" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Idempotency-Key: key-game-backend-1" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Game backend (production)",
    "scopes": ["game_servers:write", "game_servers:read"],
    "apps": ["breakwire-windows"],
    "allowed_ips": ["198.51.100.0/24"],
    "expires_at": "2027-04-01T00:00:00Z"
  }'
```

The reply is `201`, trimmed here:

```json
{
  "id": "key_...",
  "name": "Game backend (production)",
  "scopes": ["game_servers:write", "game_servers:read"],
  "key_prefix": "gsk_live_EXAMPLE",
  "key": "gsk_live_EXAMPLE_not_a_real_key",
  "expires_at": "2027-04-01T00:00:00Z"
}
```

- `key` is shown once, in this reply, and can't be seen again. Store it in your secrets store.
- A key is `gsk_live_`, a 13-character key ID, `_`, then a 43-character secret. `key_prefix` is the part that is safe to show.
- Send it as `Authorization: Bearer <key>` on every call.
- A studio can make 20 keys an hour, agent keys included, and every try counts, a refused one too. Past that the reply is `429 rate_limited`, and `Retry-After` says how many seconds to wait.

## Key fields

- `name`: 1 to 100 characters.
- `scopes`: at least one. A key never gets a scope its creator lacks; asking for one answers `403 missing_scope`.
- `apps`: up to 100 app IDs or slugs. Other apps look as if they do not exist (`404 not_found`). The key cannot create apps or change your own machines (`403 key_restricted`). The keys it makes must be limited to some of the same apps.
- `allowed_ips`: up to 50 IPv4 or IPv6 ranges in CIDR form, or single addresses. A call from any other address answers `403 key_restricted`. Each range must be one on the internet: a private, loopback, link-local, carrier-grade NAT or unspecified range, a range that takes one in, or a range that holds the address of one of our machines (where game servers run) answers `422 validation_failed`, with the entry's own field (`allowed_ips[0]` and so on). No call from the internet comes from such a range, so a key limited to it would never pass for your own servers. To allow any address, leave `allowed_ips` out.
- `expires_at`: a time in the future. After it, the key answers `401 unauthenticated`.

## Revoke a key

```bash
curl -X DELETE "https://studio.survivalservers.com/v0/api-keys/key_..." -H "Authorization: Bearer $API_KEY"
```

- `204` means revoked. The key stops working everywhere within about a second and then answers `401 unauthenticated`.
- `GET /v0/api-keys` lists your keys without their secrets, with `last_used_at`.
- To rotate a key, make the new one, move your program to it, then revoke the old one.
- Test-mode keys (`gsk_test_`) are not available yet. The API refuses them with `401 unauthenticated`.

## Game servers need no key

Each game server gets its own `GS_TOKEN` for the [local API](https://studio.survivalservers.com/docs/game-server/local-api/), good only for itself.

## Scopes

| Scope | Lets a key |
|---|---|
| `apps:read` | Read apps, builds, versions, branches, pools |
| `apps:write` | Change apps, versions, branches and pools |
| `builds:write` | Upload builds and push images |
| `versions:write` | Make and change versions, but delete nothing |
| `game_servers:read` | Read game servers and start times |
| `game_servers:write` | Start and stop game servers |
| `logs:read` | Logs, files, crash traces, metrics |
| `persistent_servers:read` | Read persistent servers, backups and files |
| `persistent_servers:write` | Run persistent servers, take backups |
| `backups:read` | Download backups |
| `backups:write` | Upload saves as backups |
| `webhooks:read` | Read endpoints and deliveries |
| `webhooks:write` | Change endpoints, retry deliveries |
| `usage:read` | Usage, prices, invoices and credits |
| `billing:write` | Set budget alerts |
| `keys:write` | Make, list and revoke API keys |
| `members:write` | Members and invitations |
| `audit:read` | Read the audit log |
| `machines:read` | Read your own machines |
| `machines:write` | Add, enroll, change and remove them |
| `studio:write` | The studio's name, icon and accent color |
| `player_sessions:write` | Erase a player's sessions when they ask |
| `players:write` | Make player passes for your players (not open yet) |

- `game_servers:write` also reports players. `logs:read` also reads each game server's timeline.
- `persistent_servers:write` also sends game commands and runs bulk actions. `persistent_servers:read` reads them.
- `apps:write` includes everything `versions:write` allows. `versions:write` does not point branches.
- `usage:read` also reads budget alerts. `billing:write` also opens the card page, from the panel only.
- Crash dumps need the crash-dump permission, not a scope. Only owners and admins can have it, and only in the panel: no key ever reaches a dump. See [who can reach a dump](https://studio.survivalservers.com/docs/concepts/logs-and-crash-dumps/#who-can-reach-a-dump).

Keys for common jobs:

- A game backend: `game_servers:write` and `game_servers:read`.
- A build pipeline (CI) that ships builds: `builds:write` and `versions:write`, plus `apps:read` to wait until a build is ready. Add `apps:write` only if it also points a branch at the new version: `apps:write` can also delete apps.
- A dashboard: the `:read` scopes for what it shows.

## Roles

Invite someone with a role:

```bash
curl -X POST "https://studio.survivalservers.com/v0/members/invitations" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Idempotency-Key: invite-sam-1" \
  -H "Content-Type: application/json" \
  -d '{ "email": "sam@kingsmoor.example", "role": "developer" }'
```

- We email the person a link. It works once, for the invited email address, until `expires_at`, 7 days after the invitation. The person signs in, or signs up, with that address and opens the link.
- Our emails do not reach inboxes yet. When an invitation is not emailed, the reply carries the link in `accept_url`, once, and the panel's invite dialog shows it with a copy button. Send it to the person yourself.
- `DELETE /v0/members/invitations/{invitation}` cancels an invitation, and its link stops working at once.
- `PATCH /v0/members/{user}` changes a role or the apps a member is limited to. `DELETE /v0/members/{user}` removes a member, and their panel sessions end at once.
- These calls need the `members:write` scope.

| Role | Can |
|---|---|
| `owner` | Everything, including owners and single sign-on |
| `admin` | Everything except owners and single sign-on |
| `developer` | Build, run and watch; read usage |
| `operator` | Start, stop, restart, saved commands and backups |
| `billing` | Usage, invoices, alerts and the card |
| `viewer` | Read only: no keys, members or audit log |

The scopes each role gets:

- `owner` and `admin` hold every scope. Only an owner can grant, change or remove the `owner` role, and only an owner can set single sign-on.
- A studio always keeps one owner. Removing or changing the last one answers `409 invalid_state`.
- `developer` holds every scope except `billing:write`, `keys:write`, `members:write`, `audit:read`, `machines:write`, `studio:write` and `player_sessions:write`.
- `operator` holds the `viewer`'s scopes plus `game_servers:write` and `persistent_servers:write`, and uses them only to start and stop game servers, start, stop and restart persistent servers, send saved commands (with numbers and listed choices only), run saved bulk actions that restart or send a saved command, take backups, and pause, resume or cancel those bulk actions. Every other change answers `403 missing_scope`. An operator connects no AI tool and makes no key.
- `billing` holds `usage:read` and `billing:write`.
- `viewer` holds `apps:read`, `game_servers:read`, `logs:read`, `persistent_servers:read`, `webhooks:read`, `usage:read` and `machines:read`.

### Limit a member to some apps

```bash
curl -X PATCH "https://studio.survivalservers.com/v0/members/$USER_ID" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{ "apps": ["kingsmoor-linux"] }'
```

A developer, operator or viewer can be limited to some apps. They then see and change only those apps and what belongs to them, as a key limited to some apps does. Anything that covers the whole studio is refused (`403`): members, invoices, credits, budget alerts, settings, single sign-on and registry logins. An owner, admin or billing member is never limited.

- `[]` lifts the limit.
- The limit holds from the member's next call.
- A limit is refused with `409 invalid_state` while a key the member made reaches beyond those apps; the answer names the keys. Revoke them first. The member's AI tool connections that reach beyond the apps are disconnected.
- An invitation can carry a limit: add `apps` to `POST /v0/members/invitations`. It applies when the person accepts.
- To make a limited member an owner, admin or billing member, send `"apps": []` in the same call.

## Two-factor authentication

Everyone who signs in to the panel with a password uses two-factor authentication (2FA). A person without it sets it up at their next sign-in. The set-up offers two ways:

- An authenticator app, such as Google Authenticator, 2FAS or Twilio Authy.
- A passkey, which signs you in with your face or fingerprint. A passkey sign-in counts as 2FA by itself.

Codes by text message are not offered yet. An owner's or admin's panel call without 2FA answers `403 mfa_required`. API keys never need it. `mfa_enabled` in `GET /v0/members` shows who uses it. People who sign in through your own single sign-on follow your provider's 2FA rules instead.

## Single sign-on

An owner can send your studio's people to your own single sign-on provider through OIDC or SAML (the two standard ways a company signs its people in), by their email domain. Open **Single sign-on** from the account menu. The page sends `PUT /v0/sso`, which works only from the panel and only for owners.

1. Save your provider and domains with single sign-on off:

   ```json
   {
     "enabled": false,
     "protocol": "oidc",
     "domains": ["kingsmoor.example"],
     "oidc_issuer": "https://login.kingsmoor.example",
     "oidc_client_id": "game-panel",
     "oidc_client_secret": "<client secret>"
   }
   ```

2. Register the reply's `redirect_uri` at your provider. For SAML, send `saml_metadata_url` instead of the `oidc_` fields, and import `saml_sp_metadata_url` at your provider.
3. Add the DNS TXT record from `domain_records` for each domain: its `record_name`, such as `_gs-sso.kingsmoor.example`, with its `record_value`. The domain counts once its `verified` is `true`.
4. Sign in to the panel through your provider. `signed_in_through_provider: true` shows that it works.
5. Send `enabled: true` with `emergency_owner`: an owner who keeps a password and 2FA, as the way in if your provider fails.

From then on (`active: true`), anyone who signs in with an email in a proven domain goes to your provider. Their accounts lose their passwords and join the provider at their next sign-in; `moving` counts them before you switch on. A password sign-in from those domains answers `403 sso_required`, except for the emergency owner.

If your provider is down, the emergency owner signs in with their password and 2FA and turns single sign-on off. Everyone else then chooses a new password with "Forgot password?" on the sign-in page, which needs our emails to reach them. Turn single sign-on on again once your provider is back.

- You can connect up to 20 domains. Public email services such as gmail.com cannot be connected.
- A domain another studio has proven answers `409 name_taken`.
- Leave out `oidc_client_secret` to keep the saved one. While single sign-on is on, the provider cannot change: switch it off first.
- Your provider can sign in only emails in the domains you proved.
- OIDC and SAML sign-in have worked with a provider we run ourselves. No studio's own provider has been connected yet.

## The audit log

```bash
curl "https://studio.survivalservers.com/v0/audit-log?action=api_key.create" -H "Authorization: Bearer $API_KEY"
```

- The log has every write by any key or member of your studio, newest first, with `actor`, `action`, `target`, `request_id`, `ip` and `at`.
- One thing is left out: a key's own match starts and stops. A matchmaker makes thousands an hour, so they are kept on each game server instead.
  - Its start and stop steps in the timeline name the key and the address the call came from, and are kept a year.
  - `GET /v0/game-servers?by=key_...` finds every game server a key started or stopped. Add `status=stopped,failed` for those that ended.
- Everything else about starts and stops is in the log:
  - every start, stop and restart a member makes in the panel, or an AI agent makes;
  - every persistent server start, stop and restart, whoever makes it;
  - a key's stop of a match someone else started;
  - a stop of many game servers at once: one line, `game_servers.stop`, with how many in `details.count`.
- `actor.type` is `key`, `user`, `agent` (an AI agent), `staff` or `system`.
- Filter by `actor` (a key ID or user ID), `action` (such as `api_key.create`), `target`, `from`, `to` and `by_ai`.
- It reads back up to one year. The call needs the `audit:read` scope. In the panel, open **Audit log** from the account menu.
