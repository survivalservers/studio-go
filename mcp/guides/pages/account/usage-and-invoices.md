# Usage and invoices

> Read what you use by app, region and rate class, set budget alerts, and read each month's invoice and credits.

Usage is counted by the second for every game server, persistent server, reservation and machine of your own. Read it in the panel under **Billing**, then **Usage**, or with the calls below. Reading usage needs the `usage:read` scope.

## Read your usage

```bash
curl "https://studio.survivalservers.com/v0/usage?group_by=region,rate_class&granularity=month" \
  -H "Authorization: Bearer $API_KEY"
```

The reply for a studio inside its free vCPU-hours, trimmed to one region:

```json
{
  "currency": "USD",
  "totals": {
    "cpu_hours": "107.6295",
    "amount_usd": "0.0000",
    "free_cpu_hours_used": "107.6295",
    "credits_usd": "0.0000",
    "projected_month_usd": "0.0000",
    "backup_gb_months": "1.09",
    "free_backup_gb_months_used": "1.09"
  },
  "rows": [
    { "date": "2026-11", "region": "europe-west", "rate_class": "on_demand_2gb", "cpu_hours": "11.8541", "amount_usd": "0.4149" },
    { "date": "2026-11", "region": "europe-west", "rate_class": "on_demand_4gb", "cpu_hours": "44.8380", "amount_usd": "2.2419" },
    { "date": "2026-11", "region": "europe-west", "rate_class": "persistent_server", "cpu_hours": "50.6316", "amount_usd": "1.2658" },
    { "date": "2026-11", "region": "europe-west", "rate_class": "backup_storage", "cpu_hours": "0.0000", "backup_gb_hours": "788.39", "amount_usd": "0.0547" }
  ],
  "free_hours": [{ "month": "2026-11", "free_cpu_hours": 2000, "used_up_at": null }],
  "dates": [{ "date": "2026-11", "cpu_hours": "107.6295", "free_cpu_hours": "107.6295", "amount_usd": "0.0000" }]
}
```

- `from` and `to` take a date or a time. They default to this calendar month (UTC), and `to` is not included.
- `group_by` takes any of `app`, `region`, `rate_class` and `version`, comma-separated.
- `granularity` is `hour`, `day` (the default), `week` (from Monday), `month` or `year`. A report by hour covers at most 62 days.
- Each row's `amount_usd` is its price before free vCPU-hours. `totals` and `dates` count free vCPU-hours and credits as the invoice does.
- `free_hours` gives each month's free vCPU-hours, and `used_up_at` the minute they ran out (`null` while some are left).
- `projected_month_usd` is the whole month at the pace so far, after free vCPU-hours and credits.
- Amounts are decimal strings in US dollars. The current month is live and estimated; a closed month is exact to the cent.
- From a terminal: `survivalservers usage --group-by app`.

## Rate classes

| Rate class | What it covers |
|---|---|
| `on_demand_2gb` | Game servers, up to 2 GB per vCPU |
| `on_demand_4gb` | Game servers, above 2 GB per vCPU |
| `persistent_server` | Persistent servers, running |
| `persistent_server_paused` | Paused past the free minutes |
| `reserved` | Reserved capacity, used or not |
| `reserved_included` | Usage a reservation covered, at $0 |
| `own_machine` | Your own machines, per vCPU a month |
| `backup_storage` | Backup storage, per GB a month |

- Each class's price is on [pricing](https://studio.survivalservers.com/docs/account/pricing/).
- `warm` appears only if ready servers waiting in a pool get their own price. Today they count as on demand.
- New classes can be added. Treat one you do not know as an ordinary row.

## Usage records

The records behind the totals:

```bash
curl "https://studio.survivalservers.com/v0/usage/records?game_server=gs_...&limit=100" \
  -H "Authorization: Bearer $API_KEY"
```

- Each record has `id`, `game_server` or `persistent_server`, `app`, `region`, `tier`, `os`, `rate_class`, `cpu`, `memory_gb`, `start`, `end` and `seconds`.
- `end` is `null` while the record is still open. No record crosses a UTC hour.
- Billing ends at a game server's real end, even when we hear of the end late, say after a network outage. While we cannot hear a machine, its game servers are billed only up to the last time we heard it. If an end still reaches us after an hourly record billed past it, that record gets shorter, or drops out of the list when none of its time ran.
- Filter by `game_server`, `persistent_server`, `app`, `from` and `to`.
- `GET /v0/usage/records.csv` exports the matching records as CSV, with the same filters. One export covers at most 13 months: `from` defaults to 13 months before `to`, and `to` to now. A longer span is refused with 422, so export it in parts. Two exports can run at once.

## Budget alerts

```bash
curl -X PUT "https://studio.survivalservers.com/v0/budget-alerts" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{ "thresholds_usd": ["100.00", "500.00"], "email": ["grace@kingsmoor.example"] }'
```

- Setting alerts needs the `billing:write` scope. You can set up to 10 thresholds and 10 email addresses.
- Each threshold alerts once a month, when `projected_month_usd` passes it.
- A month's first three days have no projection: `projected_month_usd` is the spend so far, and an alert means that money was spent.
- Each alert is also the `billing.budget_threshold` event, with `threshold_usd` and `projected_month_usd`. Every [webhook](https://studio.survivalservers.com/docs/guides/webhooks/) endpoint that takes it gets it.
- Alerts never stop anything. A budget alert will not end a match.

## Upcoming invoice

```bash
curl "https://studio.survivalservers.com/v0/billing/upcoming-invoice" -H "Authorization: Bearer $API_KEY"
```

- The reply is what your next invoice would say if its month closed now, with the same lines as an invoice. Reading it bills nothing.
- `as_of` says when it was worked out, at most a minute ago.
- Usage that reaches us after its month's invoice was made goes on the next invoice. Those lines start with the month, such as `From October 2026:`, and carry `usage_period`.
- Late lines use that month's prices and its free vCPU-hours, so they cost what they would have cost on time.
- When a game server's end reaches us after its month's invoice was made and that invoice billed time past the end, the next invoice gives it back. The line starts `Correction for October 2026` and has a negative amount. If free vCPU-hours had covered part of that time, a second line takes that part back off, so you get back exactly what you were overcharged.
- If a correction is more than the month it lands in, that invoice comes to $0 and the rest is a credit on the next one.

## Invoices

Each month's invoice is created after 02:00 UTC on the 1st, once all of the month's usage is in, and by the 3rd at the latest. The `billing.invoice_created` webhook says when the invoice is ready.

```bash
curl "https://studio.survivalservers.com/v0/invoices" -H "Authorization: Bearer $API_KEY"
```

An invoice for the month in the [pricing](https://studio.survivalservers.com/docs/account/pricing/#both-in-one-month) example, trimmed:

```json
{
  "id": "inv_...",
  "period": "2026-10",
  "status": "open",
  "subtotal_usd": "47.0000",
  "credits_usd": "0.0000",
  "total_usd": "47.0000",
  "lines": [
    {
      "description": "On demand, up to 4 GB per vCPU, europe-west: 1000.0000 vCPU-hours",
      "rate_class": "on_demand_4gb",
      "region": "europe-west",
      "cpu_hours": "1000.0000",
      "amount_usd": "50.0000"
    },
    {
      "description": "Persistent servers, europe-west: 2880.0000 vCPU-hours",
      "rate_class": "persistent_server",
      "region": "europe-west",
      "cpu_hours": "2880.0000",
      "amount_usd": "72.0000"
    },
    {
      "description": "Free vCPU-hours (2000 a month): 2000.0000 used",
      "cpu_hours": "2000.0000",
      "amount_usd": "-75.0000"
    }
  ],
  "hosted_invoice_url": "https://..."
}
```

- `GET /v0/invoices` lists invoices newest first. `GET /v0/invoices/{id}` reads one.
- There is one line per rate class and region. The free vCPU-hours and the free backup storage each come off as one line.
- `subtotal_usd` is after the free allowances. `credits_usd` comes off it to give `total_usd`.
- `status` is `draft`, `open`, `paid`, `void` or `uncollectible`.
- `hosted_invoice_url` opens the invoice at our payment provider.
- A bill under $0.50 goes to $0. The line `Under $0.50, not charged` takes it off, and the invoice is marked `paid` with no charge and no email.

## When payment is needed

Owners, admins and members with the `billing` role manage the card in the panel. A studio with a card is charged at month close; one without gets an invoice to pay within 14 days. A start of a game server or persistent server answers `402 card_required` when:

- there is no card on file, or
- a payment has failed and stayed unpaid for 14 days, or an invoice is still unpaid 14 days after its due date.

With no card on file, creating a persistent server and uploading backups, files or container images answer `402 card_required` too. Nothing running is stopped at that point. If an invoice is still unpaid 30 days after the failed payment or its due date, we may stop your running game servers with at least 7 days' notice. Persistent servers are saved and backed up first, and never deleted for non-payment. Add a card or pay the open invoice; a card you save pays open invoices at once. New starts then work again within 30 seconds. The `billing.payment_failed` event carries `data.starts_stop_at`, the time new starts stop if the invoice stays unpaid.

## Emails

- Budget alerts go to the addresses in `email`. An address that belongs to a member of your studio gets them at once. Any other address first gets one email asking it to confirm, and gets budget alerts only after it confirms.
- Invoices go to the studio's owners and `billing` members, and so do reminders of a failed payment on days 0, 3 and 7.
- A studio with no card gets an email when it has used 80% of the month's free vCPU-hours and again when all are used, once a month each, to its owners and `billing` members. Each is also the `billing.free_hours` event.
- Our emails do not reach inboxes yet. Until they do, use the webhooks and the API.

## Credits

```bash
curl "https://studio.survivalservers.com/v0/credits" -H "Authorization: Bearer $API_KEY"
```

- `kind` is `switching`, `manual` (added by staff, or the rest of a correction carried to the next month) or `sla` (an uptime credit, with its `continent`; none for now).
- Each credit has its `period`, `amount_usd` and `reason`, and the `invoice` it went on once applied.
- Credits apply in this order: uptime, switching, staff. A staff credit larger than the month carries the rest to the next month.
