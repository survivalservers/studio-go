# Pricing

> What game servers, persistent servers, reserved capacity and your own machines cost, what is free, and how a monthly bill adds up.

You pay per vCPU-hour for the vCPUs assigned to your game servers and persistent servers, billed by the second with no minimum. One vCPU is one hardware thread. To see what you have used so far, go to [usage and invoices](https://studio.survivalservers.com/docs/account/usage-and-invoices/).

## Rates

| What | Rate class | Per vCPU-hour |
|---|---|---|
| On demand, up to 2 GB per vCPU | `on_demand_2gb` | $0.035 |
| On demand, up to 4 GB per vCPU | `on_demand_4gb` | $0.05 |
| Persistent server, running | `persistent_server` | $0.025 |
| Persistent server, paused | `persistent_server_paused` | $0.025 |
| Reserved capacity | `reserved` | $0.025 |

- The prices are the same on Windows and Linux, and on the `standard` and `high-frequency` tiers.
- Memory runs from 0.5 GB to 4 GB per vCPU. Up to 2 GB per vCPU bills at $0.035; above 2 GB, at $0.05.
- A running persistent server bills at $0.025 whatever its memory per vCPU.
- The first 60 minutes of each pause are free (`paused_free_minutes`). A persistent server that stopped when empty costs nothing apart from its backup storage.
- After a pause, billing starts when a player connects. One exception: a stopped persistent server whose machine is full moves when a player keeps trying to join and another machine in the region has room. It bills as running from the move until it pauses again, even if no player connects: 10 to 12 minutes with the default settings.
- [Your own machines](https://studio.survivalservers.com/docs/guides/own-machines/) bill $1 per usable vCPU a month (`own_machine`), from enrollment to removal. Game servers on them have no hourly charge. Not run on real machines yet: billing for your own machines.
- Ready servers waiting in a pool bill at the on-demand rate for their size, from their launch.
- Backup storage is $0.05 per GB a month, after the first 100 GB each month (`free_backup_gb_months`). Restores, rollbacks and downloads are free.
- Bandwidth is included, within fair use: outbound traffic that averages up to 10 Mbit/s per running vCPU over the month. Above that, we tell you what we measured and work with you on it. If you stay above it 30 days after our notice, we may limit the bandwidth of the game servers causing it, or stop hosting them with a further 30 days' notice. We never charge for bandwidth without your written agreement.

## Free vCPU-hours

Every studio gets 2,000 free vCPU-hours each calendar month (UTC). They come off the month's invoice. Add a card in the panel to start game servers.

- They cover game servers on demand, persistent servers running or paused, and ready servers waiting in a pool. At month close they go to the month's most expensive usage first.
- They never cover reserved capacity, your own machines or backup storage, which has its own free 100 GB.
- Unused free vCPU-hours do not carry over to the next month.
- A studio can run 512 vCPUs at once and keep up to 100 persistent servers, running or stopped. We raise these [on request](https://studio.survivalservers.com/docs/help/get-help/) when we have room.
- With no card on file, new starts, new persistent servers, and backup, file and container image uploads answer `402 card_required`. Game servers and persistent servers already running keep running.
- `GET /v0/studio` shows `free_tier.cpu_hours_used` and `card_on_file`.

## Price lock

Your prices and free allowances are locked for 24 months from the day your studio is created. `locked_until` in `GET /v0/prices` and `price_locked_until` in `GET /v0/studio` give the date.

- If list prices drop during the lock, you pay the lower price at once.
- If the free vCPU-hours rise during the lock, you get the higher number.
- When the lock ends, a new 24-month lock starts at that day's list prices. If any of them is higher, we tell you at least 90 days ahead.

## Your price list

```bash
curl "https://studio.survivalservers.com/v0/prices" -H "Authorization: Bearer $API_KEY"
```

```json
{
  "currency": "USD",
  "locked_until": "2028-10-21T01:14:05.207902Z",
  "rates": {
    "on_demand_cpu_hour_2gb": "0.0350",
    "on_demand_cpu_hour_4gb": "0.0500",
    "persistent_server_cpu_hour": "0.0250",
    "paused_cpu_hour": "0.0250",
    "reserved_cpu_hour": "0.0250",
    "own_machine_cpu_month": "1.0000",
    "backup_gb_month": "0.0500"
  },
  "free_cpu_hours_per_month": 2000,
  "free_backup_gb_months": 100,
  "paused_free_minutes": 60,
  "bandwidth": "included"
}
```

- The call needs the `usage:read` scope. Money is a decimal string in US dollars, never a float.
- `survivalservers prices` prints the same list. The MCP server's `get_prices` and `estimate_cost` tools use it too.

## Reserved capacity

A reservation holds whole vCPUs for your studio in one region, tier and system for a set time.

- It bills at $0.025 per vCPU-hour for the whole time, whether or not anything runs. If the region cannot hold all of it, you pay only for the vCPUs it really held, hour by hour.
- So it costs less than on demand only for vCPUs that are busy most of the time: more than about 71% of the hours at the 2 GB rate, or half of them at the 4 GB rate. The price calculator reserves enough for your busiest hour, all month.
- Your game servers and persistent servers there are covered up to the vCPUs it holds. Covered usage shows as `reserved_included` at $0.
- Staff set reservations up for you. There is no API call for reservations yet.

## Worked examples

### A thousand short matches

A game server of 2 vCPUs and 4 GB (2 GB per vCPU) runs a 30-minute match: 2 × 0.5 = 1 vCPU-hour, or $0.035. A thousand of them in a month are 1,000 vCPU-hours, inside the free vCPU-hours, so the month costs $0.

### One persistent server all month

A persistent server of 4 vCPUs runs all month (720 hours): 2,880 vCPU-hours at $0.025 is $72.00. The 2,000 free vCPU-hours take off $50.00, so the month costs $22.00.

### Both in one month

The same persistent server, plus 1,000 vCPU-hours of matches at 4 GB per vCPU ($50.00). The free vCPU-hours go to the matches first ($50.00 off), then to 1,000 hours of the persistent server ($25.00 off). The month costs $122.00 less $75.00: $47.00.

## Credits

- **Uptime credits:** none for now. See [uptime](https://studio.survivalservers.com/docs/account/uptime/).
- **Switching from another provider:** a studio that moves a game here gets its first month free, up to $1,000 of charges. Our engineers help with the move. Ask within 90 days of creating your studio; the offer is once per business. Staff grant it, the credit shows as `kind: switching` in `GET /v0/credits`, and it applies after any uptime credit.
