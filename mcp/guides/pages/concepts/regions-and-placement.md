# Regions and placement

> How each game server gets its region and machine, from player pings, your preferences and where each version is ready.

Every start picks a region first, then a machine in that region. You choose which regions a start can use, and placement picks the best one that has room and has the version ready.

```bash
curl "https://studio.survivalservers.com/v0/regions?app=breakwire-windows&branch=live" -H "Authorization: Bearer $API_KEY"
```

```json
{
  "data": [
    {
      "id": "europe-west",
      "name": "Europe West",
      "country": "FR",
      "continent": "europe",
      "status": "active",
      "offers": [{ "tier": "standard", "os": ["windows"] }, { "tier": "high-frequency", "os": ["linux"] }],
      "beacon": { "host": "203.0.113.40", "udp_port": 9001, "tcp_port": 9001 },
      "version_ready": true
    }
  ],
  "next_cursor": null
}
```

## Regions and continents

- `status`: `active` regions take starts. `planned` ones are listed but take none yet. `draining` ones are closing and take no new starts. Studios that use one get at least 90 days' notice, less only when a provider or the law gives us less.
- Today one region is `active`: `europe-west` (Europe West, in France). Other regions open as machines are added there.
- A region takes starts only while its `status` is `active`, so read the list rather than a region you remember.
- `offers`: the tiers and systems the region sells. Filter the list with `continent`, `tier` or `os`.
- With `app` and a `version` or `branch`, each region also carries `version_ready`.
- Region IDs such as `europe-west` never change.
- `GET /v0/continents` lists each continent with its regions, such as `europe` with `europe-west`.

DDoS protection is included on every machine. It comes from the data center's own network filtering and starts by itself when an attack is detected. Filtering tuned to your game's own traffic is set up only after we test it with that traffic.

## Measure pings

Each region's `beacon` echoes UDP packets of up to 64 bytes on `udp_port`, accepts TCP connects on `tcp_port`, and answers ICMP ping. It echoes up to 20 packets a second from each address.

`europe-west` lists its beacon. A region without one leaves `beacon` out; for it, send `player_ips` with the start instead.

1. Have the game client ping each region five times and keep the median.
2. Send the results to your backend with the player.
3. Put them in the start's `placement.player_pings`.

## Choose where it starts

```json
"placement": {
  "regions": ["europe-west", "europe-central"],
  "continents": ["europe"],
  "player_pings": { "p_1": { "europe-west": 31, "europe-central": 44 }, "p_2": { "europe-west": 52, "europe-central": 29 } },
  "max_ping_ms": 80
}
```

- `regions` are tried in your order, then the regions of `continents`. With neither, the regions named in `player_pings` are the candidates.
- `player_pings` gives each player's ping in ms to each region. Candidates are then ranked by the worst player's ping, so the match is fair for everyone in it.
- With pings, your order only breaks ties. A region that some player has no ping for goes last.
- `player_ips` stands in when you have no pings. Each address is located and its ping to each region estimated from the distance, then regions are ranked as with pings.
- Measured pings always win over `player_ips`. Private or unknown addresses are left out, and an entry that is not an address answers `422`.
- IP Geolocation by [DB-IP](https://db-ip.com).
- `max_ping_ms` works with pings. Regions over it are tried last, and the reply's `placement.fallback_used` says when one was used.
- With `"fallback": false`, regions over `max_ping_ms` are left out instead.
- With no `placement`, the app's `default_placement` is used. With neither, the start answers `422`.

In October 2026, across two regions, starts with player pings went to the region with the lower worst ping. Once that region was full, the next start went to the other one.

In November 2026, a start with only `player_ips` was placed from their estimated pings. With `max_ping_ms` under the region's ping and `"fallback": false` it answered `503` with `over_max_ping`; without `fallback` set it started there with `fallback_used: true`.

## When no region has room

```json
{
  "status": 503,
  "code": "capacity_unavailable",
  "regions_tried": [
    { "region": "europe-west", "reason": "no_capacity" },
    { "region": "europe-central", "reason": "version_not_ready" }
  ]
}
```

A start tries each candidate region in turn. When none can take it, the answer is `503 capacity_unavailable`, and `regions_tried` says why each region was passed over.

| `reason` | Meaning |
|---|---|
| `no_capacity` | The region is full now |
| `version_not_ready` | The version is still being copied there |
| `version_removed` | No longer kept on the machines there |
| `not_offered` | Not active, or no such tier and system |
| `over_max_ping` | Worst ping over `max_ping_ms` |
| `unavailable` | The region cannot be reached now |

- When every region the start may use lacks the version, the answer is `409 version_not_ready` instead. Its `regions_tried` lists any region whose machines removed the version, as `version_removed`: waiting does not bring it back there, [putting it on a branch](https://studio.survivalservers.com/docs/concepts/apps-builds-versions/#what-stays-on-the-machines) does.
- Try again later, or allow more regions or continents.
- [Ready servers](https://studio.survivalservers.com/docs/concepts/ready-servers/) in your busiest regions hold room there ahead of the starts.
