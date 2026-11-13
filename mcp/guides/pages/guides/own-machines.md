# Your own machines

> Run your game servers on machines you already have, with our agent, for $1 per usable vCPU a month.

You can run our agent on a machine of your own, and it takes your studio's game servers alongside our machines. Starts, stops, persistent servers, logs, files and the API work the same on it. Only your studio's game servers go there. Your starts in its region try your own machines first, then ours when yours are full.

Not run on real machines yet: adding, enrolling, placement and billing of your own machines.

## What the machine needs

| | Windows | Linux |
|---|---|---|
| System | Windows Server 2022 | Ubuntu 24.04, kernel 6.8+ |
| Processor | x86-64 | x86-64 |
| Game drive | `D:`, ReFS | Any disk |
| Container runtime | None | containerd and runc |
| Threads kept back | 4 | 2 |
| Free disk | Your builds plus 20 GiB | Your builds plus 20 GiB |

- **Game drive:** on Windows, `D:` holds builds, game folders and logs. It is formatted ReFS so each game server gets its own copy of the build by block clone.
- **Container runtime:** Linux game servers run as containers, under the containerd and runc releases the set-up script installs.
- **Threads kept back:** the agent and the system keep these hardware threads. The rest are the machine's usable vCPUs.
- **Free disk:** the agent keeps 20 GiB free when it copies builds to the machine.
- **Ports:** players reach game servers on UDP and TCP ports 20000 to 48999, so open that range inbound. Nothing else needs to reach the machine: the agent connects out to us, and we never need a login to it.
- **Set-up script:** one per system prepares the drive, the runtime, the firewall and the accounts for the agent. To get the agent and its set-up script, [write to us](https://studio.survivalservers.com/docs/help/get-help/).

A Linux machine with 24 hardware threads offers 22 usable vCPUs. Usable vCPUs are what your game servers can use and what the machine is billed for.

## Add and enroll a machine

In the panel, open **Machines** and choose **Add a machine**, or call the API:

```http
POST /v0/machines
{ "name": "office-rack-1", "region": "europe-west", "os": "linux", "public_ipv4": "203.0.113.20" }
```

`region` is the region the machine counts as for placement and players' pings: pick the nearest of our regions. `public_ipv4` and `public_ipv6` are optional; the agent reports them too. The reply carries a one-time enrollment token, valid 24 hours and shown only in this reply, and the address to enroll at:

```json
{
  "id": "mch_t01JA90B2N4M6Q8W0E2R4T6Y8U",
  "state": "waiting_for_agent",
  "enrollment": { "token": "gset_...", "expires_at": "2026-10-22T14:02:00.000Z", "enroll_url": "https://..." }
}
```

Install the agent on the machine and enroll it with `enroll_url`, the token and the machine's ID, as root on Linux or as an administrator on Windows.

The machine shows `active` once its agent has enrolled and first checked in. Its usable vCPUs show as `usable_vcpus` and its memory for game servers as `memory_mb`. From then on it takes your game servers.

To install the agent again on a rebuilt machine, make a new token: **New machine setup token** in the panel, or `POST /v0/machines/{id}/enrollment-token`. The machine keeps its ID, its history and its billing start. A studio can have up to 100 machines added and not removed; [talk to us](https://studio.survivalservers.com/#talk) to raise it.

## States

| State | Takes new game servers |
|---|---|
| `waiting_for_agent` | No, not enrolled yet |
| `active` | Yes |
| `maintenance` | No, running ones keep running |
| `unreachable` | No, game servers there may still run |
| `removed` | No, and it is not billed |

A machine shows `unreachable` once we stop placing game servers on it after missed check-ins, and always after 90 seconds without one.

## Maintenance and removal

Before an update or a reboot, stop new game servers from going there:

```http
PATCH /v0/machines/{id}
{ "maintenance": true }
```

Send `false` to put it back to work. `name` in the same call renames the machine.

To remove a machine, stop its game servers, ready ones in pools included, and move or delete the persistent servers placed there. Then call `DELETE /v0/machines/{id}`, or choose **Remove** in the panel. While anything runs there, the reply is `409 in_use` and names what is left. After removal, stop and uninstall the agent.

## How billing works

- **$1 per usable vCPU a month**, spread over the time the machine was enrolled. A machine with 22 usable vCPUs costs $22.00 for a whole month. Enrolled for 20 days of a 30-day month, it costs $14.67.
- Billing starts when the machine first enrolls and stops when you remove it. Installing the agent again does not start it over. `monthly_price` on the machine shows a full month at today's usable vCPUs.
- **Game servers on your own machines cost nothing per hour.** The month's free vCPU-hours go to your usage on our machines. Our [uptime](https://studio.survivalservers.com/docs/account/uptime/) aim covers our machines, not yours.
- On the invoice each region has one line for your own machines, in vCPU-months. In [usage](https://studio.survivalservers.com/docs/account/usage-and-invoices/) the charge shows under the rate class `own_machine`, shared over the days, and adds up to the invoice line.
- `GET /v0/prices` shows your rate as `own_machine_cpu_month`.

## Keys and roles

Reading your machines needs the scope `machines:read`. Adding, changing and removing them needs `machines:write`. Owners and admins have both scopes; developers and viewers can read. A key limited to some apps cannot add, change, enroll or remove machines (`403 key_restricted`).
