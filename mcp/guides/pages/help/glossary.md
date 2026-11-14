# Glossary

> The words these docs use, each explained in one place.

Each term is the name the API, the panel and these docs use for it.

## A to C

- **Admin port**: a port with `"port_policy": "none"`, such as RCON, that gets no public port. Only its own machine reaches it.
- **Agent key**: an API key for an AI agent, with the switches, time limit and monthly spending cap you pick. What it does is marked "By AI".
- **AI agent**: an AI tool, such as Claude Code or Cursor, that calls the API for you with an agent key.
- **Allocation**: a match taking a game server. Its `external_id`, labels, `env` and `args` are the allocation data.
- **API key**: a secret your backend sends as `Authorization: Bearer`, held to its scopes. It starts `gsk_live_`.
- **App**: one game server program for one system, Windows or Linux, with its builds, versions and branches.
- **Artifact**: a file kept after a game server exits. Its `kind` is `log` or `file`. Crash dumps are kept apart: see Crash dump.
- **Audit log**: every write by any key or member of your studio, read with `GET /v0/audit-log`.
- **Backup**: a copy of a persistent server's `save_paths`, taken on a schedule, at each stop, or when you ask.
- **Branch**: a name, such as `live`, that points at a version. Moving it is a rollout or a rollback.
- **Budget alert**: a threshold on the projected month total. It alerts once a month when passed and never stops anything.
- **Build**: your game server's files, a zip for Windows or an image for Linux, copied ahead to the regions its versions use.
- **Bulk action**: one action, such as a command, a config change or a restart, on many persistent servers or game servers in batches.
- **CCU**: players online at once. Peak CCU is the most at your busiest hour.
- **Continent**: a group of regions, such as `europe`.
- **Crash dump**: a file of a crashed process's memory, caught with no code in your game.
- **Credit**: an amount taken off an invoice. Its `kind` is `sla`, `switching` or `manual`.

## D to L

- **Draining**: a region being closed. Studios that use it get at least 90 days' notice, less only when a provider or the law gives us less.
- **Dry run**: a bulk action sent with `"dry_run": true`. It answers with its plan and changes nothing.
- **Event stream**: the same events as webhooks, live, from `GET /v0/events/stream`.
- **Exit reason**: why a game server ended: `api`, `bulk`, `self`, `ttl`, `max_duration`, `crashed`, `out_of_memory`, `out_of_disk`, `boot_timeout`, `health_timeout`, `machine_failure`, `lost_contact` or `restart`.
- **Free vCPU-hours**: 2,000 vCPU-hours a month at no charge, applied to the most expensive usage first.
- **Fresh start**: a start that launches a new process (`cold` in the API). The game server is joinable once its ready check passes.
- **Game command**: a command in a game profile, such as Save or Kick, that the panel shows as a button and any API call can run by its `id`.
- **Game profile**: one version's description of your game in plain JSON: how to reach it, its game commands, its settings, how to read its players, and its maps.
- **Game server**: one running copy of a version, usually for one match.
- **High-frequency**: the tier with whole vCPUs held for one game server, for fast PvP.
- **`Idempotency-Key`**: a header you send on each `POST` and `PATCH`, so that a repeat never does the work twice.
- **Label**: a `name: value` pair on a game server or persistent server, for finding and filtering.
- **Local API**: the HTTP API on each machine, at `$GS_LOCAL_API_URL`, that a game server can call.

## M to R

- **Machine agent**: our program on each machine. It starts, watches and stops your game servers.
- **Maintenance**: a bulk action at a set time that warns players in game, then restarts, rolls out or applies shared settings.
- **Member**: a person in your studio, with one role.
- **Own machine**: a machine of yours that runs our machine agent and takes only your studio's game servers. Not run on real machines yet.
- **Panel**: your studio's web control panel, at https://studio.survivalservers.com/app.
- **Passthrough port**: on Linux, a public port with `"port_policy": "passthrough"`. The game listens on the public port's own number inside its container, so it can tell Steam's server list the port players reach.
- **Pause when empty**: a persistent server pausing, or stopping, when nobody plays, and resuming on the first player. Its states are Running, Paused and Stopped.
- **Paused**: a persistent server held in memory while using almost no vCPU time. It resumes when a player arrives.
- **Persistent server**: a named game server that keeps its saves, address and ID across restarts, and runs until you stop it.
- **Placement**: how a start picks its region and machine, from your regions and continents, player pings and where the version is ready.
- **Pool**: ready servers of one version and size started and waiting in a region, so a start takes one at once.
- **Price lock**: your prices held for 24 months from sign-up. A lower list price still reaches you.
- **Rate class**: the price group a piece of usage bills under, such as `on_demand_2gb` or `persistent_server`.
- **Ready check**: how a game server counts as joinable: `local_api`, `log`, `port`, `delay` or `none`. A game that opens its port before its world loads needs `log`.
- **Ready server**: a game server started ahead of time in a pool. A start from a ready server (`warm` in the API) is joinable in the reply.
- **Region**: a place game servers run, such as `europe-west` (Europe West). Each region belongs to one continent.
- **Reserved capacity**: whole vCPUs held for your studio in one region, tier and system, billed whether used or not. Our staff set it up.
- **Role**: what a member may do: `owner`, `admin`, `developer`, `billing` or `viewer`.
- **Rollback**: putting the earlier version back on a branch or on persistent servers.
- **Rollout**: putting a new version on a branch, or moving persistent servers to it in batches.
- **Saved action**: a bulk action's request kept under a name, to run again in one call or on a schedule.

## S to Z

- **Scope**: one permission on a key or a role, such as `game_servers:write`.
- **Session-based**: game servers started for one match and ended after it, as opposed to persistent servers.
- **Shared settings**: environment variables, launch arguments, config files and game settings applied to many persistent servers at once (a config set in the API).
- **Shutdown notice**: what a game server reads from `GET /v0/self/shutdown` when it is asked to stop, with the deadline to save by.
- **Shutdown time**: how long a game server has to save and exit after a stop before it is killed (`shutdown_time` in the API). The default is 30 seconds.
- **Single sign-on**: signing your people in to the panel through your company's own sign-in provider, over OIDC or SAML (the two standards for it).
- **Standard**: the tier for most games, from 0.25 vCPU in steps of 0.25.
- **Start time**: the time from the start call to the reply with the address.
- **Status card**: one Discord message for your players that we keep current: region status, your version, the persistent servers you choose. Not run on real machines yet.
- **Studio**: your account, with its apps, keys, members, usage and invoices.
- **Switching credit**: your first month free when you move from another provider, up to $1,000 of use.
- **Tier**: the kind of machine time a game server gets: `standard` or `high-frequency`.
- **Timeline**: a game server's steps from start to exit, read with `GET /v0/game-servers/{id}/events`.
- **TTL**: a time limit on one game server (`ttl`), held on its machine. When it runs out, the exit reason is `ttl`.
- **Two-factor authentication (2FA)**: a one-time code after the password. Everyone who signs in to the panel with a password uses it.
- **Uptime credit**: a credit for a month that misses an uptime promise. None for now.
- **vCPU**: one hardware thread. Sizes and prices count vCPUs.
- **vCPU-hour**: one vCPU for one hour, the unit you pay for, counted per second.
- **Version**: a build plus how to run it, under a name that always means the same thing.
- **Webhook**: a signed HTTPS `POST` to your backend when something changes, retried for 24 hours.
