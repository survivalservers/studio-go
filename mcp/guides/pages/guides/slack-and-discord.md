# Slack and Discord alerts

> Send alerts to a Slack or Discord channel and keep a live server status card in your players' Discord, with no backend to run.

A Slack or Discord channel can get an alert when a game server fails, a bulk action stops or a budget is passed. You add the channel in the panel, and nothing runs on your side.

Not run on real machines yet: channels and status cards. They have run against stand-ins for Slack and Discord.

## Add a channel

1. In the panel, open **Developers**, then **Alerts**.
2. Choose **Add Slack** or **Add Discord**.
3. Make a webhook in that service. The dialog lists the steps:
   - **Slack:** create an app at [Your Apps](https://api.slack.com/apps?new_app=1), turn on **Incoming Webhooks**, choose **Add New Webhook** and pick a channel.
   - **Discord:** open the channel's settings, then **Integrations**, **Webhooks**, **New Webhook**.
4. Paste the webhook URL, name the channel and choose **Add channel**.
5. Choose **Send test**. The row says **Test sent to Slack**, or shows the service's own reason if it refused.

The alerts a team usually wants are ticked for you. Choose the pencil on a row to change them.

## What a message looks like

Each message has a line that says what happened and a quiet line with the counts or the reason. The quiet line also has the time, in the reader's own time zone, and a link to the page in the panel.

The first alert of a kind posts at once. More of the same kind in the next 10 minutes fold into one summary when the 10 minutes end, so 200 crashes are two messages:

```text
Game server failed · Breakwire Server (Windows) · Europe West
Crashed · 14:02 · Open in panel

36 more game servers failed · Breakwire Server (Windows)
Europe West 36 · Crashed 33, out of memory 3 · 14:12 · Open in panel
```

Bulk action, billing and region alerts each post on their own.

## Alerts ticked by default

| Alert | Event |
|---|---|
| Game server failed | `game_server.failed` |
| Game server restarted | `game_server.restarted` |
| Memory over 90% | `game_server.memory_high` |
| Disk over its size | `game_server.disk_high` |
| Persistent server failed | `persistent_server.failed` |
| Persistent server moved | `persistent_server.moved` |
| Backups failing | `persistent_server.backup_failed` |
| Backup did not restore | `backup.check_failed` |
| Bulk action stopped | `bulk_action.stopped` |
| Bulk action finished | `bulk_action.finished` |
| Build failed | `build.failed` |
| No ready servers left | `pool.exhausted` |
| Ready servers failing to start | `pool.starts_failing` |
| Budget passed | `billing.budget_threshold` |
| Payment failed | `billing.payment_failed` |
| Region down or back up | `region.status_changed` |

You can also tick these. They are off by default, since running and stopped fire for every match:

- Game servers and persistent servers running or stopped.
- Backups made, restores done, a game at its limit of kept backups, and backups ready to download.
- Bulk actions started or paused.
- Builds ready, versions ready in a region, and invoices.

## Narrow a channel

Choose the pencil on a row to send a channel only some of your alerts:

- **Apps** and **Regions** limit it to those apps and regions. Alerts that belong to no app or region, such as billing, still arrive.
- **Quiet hours** hold alerts between two times in your studio's time zone. Critical alerts still go: failures, a bulk action stopping, a failed payment and a region going down.
- The rest wait and go as one summary when quiet hours end: "While quiet hours were on: 2 warnings, 5 updates".

`apps` and `regions` work on any webhook endpoint. `quiet_hours` is for channels only.

## When a channel stops working

- **The channel or webhook is gone, or the service refuses it.** The row turns **Off** with the reason. Your studio's owners get an email, though our emails do not reach inboxes yet. Choose **Reconnect** and paste a new webhook URL. The channel keeps its name and alerts.
- **The service is slow or down.** The row says **Failing**, and each message is tried again for an hour, then dropped.
- **The service asks for a wait.** The message waits as long as the service says, and the channel's other messages wait too.

Every message and the service's answer are listed under **Deliveries**.

## Use the API

A channel is a [webhook endpoint](https://studio.survivalservers.com/docs/guides/webhooks/) with a `format`:

```http
POST /v0/webhook-endpoints
Authorization: Bearer $API_KEY
Content-Type: application/json

{ "format": "discord", "url": "https://discord.com/api/webhooks/1234/...", "description": "Breakwire ops" }
```

- `url` must be the service's own webhook address. It is the secret: it is stored encrypted and shown only masked from then on.
- `events` defaults to the alerts above, not every event. It takes the same types and wildcards as any endpoint.
- There is no signing secret, so `default: true` and `rotate-secret` answer `422`.
- `POST /v0/webhook-endpoints/{id}/test` sends a test message. `GET /v0/webhook-deliveries?event=...` shows what the service answered.
- `PATCH` with a new `url` and `enabled: true` reconnects a channel.
- AI agents can't read, add or change a channel or a status card, as with every webhook.

## Keep a status card in Discord

A status card is one message in a Discord channel that shows your game's server status. The same message is edited as things change, so the channel never fills up.

```text
Brinewatch Server (Linux) · Server status
Europe West · Online

Version 1.8.2, live since 2 hours ago

EU Official PvE 1 · Online · 23 players

Checked 1 minute ago
```

Times show in each player's own time zone. The card never shows a link, an ID or an address.

1. In Discord, open the settings of a channel only your team can post in, then **Integrations**, **Webhooks**, **New Webhook**, and copy the webhook URL.
2. In the panel, open **Developers**, then **Alerts**, and choose **Add status card**.
3. Pick the app, paste the URL and choose **Add status card**. The message appears in the channel at once, and the page shows it as players see it.

By default the card lists every region the `live` branch's version is ready in, and no persistent servers. Choose **Edit** to pick the branch, the regions and the persistent servers to list.

| Line | Where it comes from |
|---|---|
| Online, Problems | Our status checks for that region |
| Maintenance | Maintenance bulk actions, from 24 hours ahead |
| Version, Updating | The branch's version, and a running rollout |
| Persistent servers | Their status and player count |

Two bad minutes in a row show Problems, and three good minutes clear it. If our checks stop, each region keeps the last state they gave it, and a region with no check in the last day is left off the card.

Each card is checked every minute. Its message is edited only when it changed, or every 10 minutes to move **Checked**. **Update now** edits it at once.

If a player or moderator deletes the message, a new one is posted. If the webhook is deleted, the card turns **Off** and your studio's owners get an email. Our emails do not reach inboxes yet. **Reconnect** takes a new webhook URL.

Through the API, `POST /v0/status-cards` takes `url`, `app` and optional `branch`, `regions` and `persistent_servers`. The reply's `preview` is the exact Discord message body. `POST /v0/status-cards/{id}/refresh` edits the message now, and `DELETE` removes the message too.
