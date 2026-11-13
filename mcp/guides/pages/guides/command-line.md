# Command line

> Call the API from a terminal or a CI job with curl: start, read and stop game servers and follow their logs.

From a terminal or a CI job, call the API with `curl`. You need nothing else installed: the API is HTTPS and JSON, and every guide shows its calls with curl.

## Set your API key

```bash
export API_KEY=gsk_live_...
```

Send it as `Authorization: Bearer $API_KEY` with every call. Keep it in an environment variable or your CI's secrets, never in a file you commit or in a game build.

## Common calls

```bash
# Running game servers of one app
curl "https://studio.survivalservers.com/v0/game-servers?app=breakwire-windows&status=running" -H "Authorization: Bearer $API_KEY"

# Start one. The match ID as the key means a retry never starts a second one.
curl -X POST "https://studio.survivalservers.com/v0/game-servers" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: match-88231" -H "Content-Type: application/json" \
  -d '{"app": "breakwire-windows", "branch": "live"}'

# Read it: its status and the address players join
curl "https://studio.survivalservers.com/v0/game-servers/gs_..." -H "Authorization: Bearer $API_KEY"

# Follow its console
curl -N "https://studio.survivalservers.com/v0/game-servers/gs_.../logs/stream?follow=true" -H "Authorization: Bearer $API_KEY"

# Stop it
curl -X POST "https://studio.survivalservers.com/v0/game-servers/gs_.../stop" -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)"
```

- Send an `Idempotency-Key` with every `POST`, so a retry repeats nothing.
- A refused call answers with `status`, `code`, `detail` and `request_id`. Quote `request_id` to [support](https://studio.survivalservers.com/docs/help/get-help/).
- `jq` reads fields out of an answer in a script, for example `jq -r .address.ipv4`.

## Where the other calls are

- Builds, and a CI step that uploads one: [upload builds](https://studio.survivalservers.com/docs/guides/upload-builds/).
- Versions and branches: [roll out and roll back](https://studio.survivalservers.com/docs/guides/rollouts-and-rollbacks/).
- A game server for each match: [start game servers from your backend](https://studio.survivalservers.com/docs/guides/start-matches-from-your-backend/).
- Persistent servers and their saves: [back up and restore](https://studio.survivalservers.com/docs/guides/back-up-and-restore/).
- Many game servers at once: [bulk actions](https://studio.survivalservers.com/docs/guides/bulk-actions/).
- Every call, field and error: the [API reference](https://studio.survivalservers.com/docs/reference/overview/).

## The command-line tool

`survivalservers` calls the API from a terminal or a CI job. You can upload a build, create a version, start a game server, follow its console, and run persistent servers and pools. It is a single program with no dependencies, for Windows, macOS and Linux on x86-64 and ARM.

### Get it

The [latest release](https://github.com/survivalservers/studio-packages/releases/latest) page lists the file for each system and a `SHA256SUMS` file to check it against. Download both, check your file, then unpack it and put `survivalservers` on your `PATH`:

```bash
sha256sum -c --ignore-missing SHA256SUMS          # on macOS: shasum -a 256 -c --ignore-missing SHA256SUMS
```

### Its API key

```bash
export GS_API_URL=https://studio.survivalservers.com
export GS_API_KEY=gsk_live_...        # or: survivalservers account --key-file ~/.config/gs-key
survivalservers account
```

```text
Studio Kingsmoor Interactive (kingsmoor-interactive), active
Free vCPU-hours this month: 12.5000 of 2000 used
```

- The key comes only from `GS_API_KEY` or the file named by `--key-file`.
- A key typed on the command line is refused with exit code `2`, because other programs and your shell history can read it. Revoke a key you typed there.
- `GS_API_URL`, or `--api`, is the API address. Without either, it is `https://studio.survivalservers.com`.

### Upload and start

```bash
survivalservers apps create breakwire-windows --os windows --continent europe
survivalservers builds upload breakwire-windows build/BreakwireServer-Win64.zip --wait
survivalservers versions create breakwire-windows version.json --build bld_... --name 2.3.0 --wait
survivalservers branches set breakwire-windows live 2.3.0
survivalservers game-servers start breakwire-windows --continent europe --wait
```

```text
Game server gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2: running in europe-west (cold windows, version 2.3.0)
Address: 203.0.113.24:31042
Players: 0
```

- `builds upload` sends the zip in parts straight to storage, each part with its exact length. `--wait` waits until the build is `ready` or `failed`.
- A Linux build comes from an image: `survivalservers builds image breakwire-linux registry.kingsmoor.example/breakwire-server:2.3.0 --wait`. The image needs a version tag other than `latest`, or a digest. `--registry-credential rcr_...` names a saved login for a private registry.
- `survivalservers registry login` logs Docker in to our registry. It hands Docker the password on its standard input, then prints where to push.
- The settings file is a version in the API's own shape, like the samples' `version-windows.json`. `--build` and `--name` override its `build` and `name`.
- `versions create --wait` waits until one region has the version ready.

Before it sends a version, `versions create` checks the settings. When one fails, it lists what to change and sends nothing. It checks:

- the build: a ready build of this app;
- the size against the tier: 0.25 vCPU steps on standard, whole vCPUs on high-frequency, 0.5 to 4 GB per vCPU in 0.25 GB steps;
- the ports, and that each `{{port.name}}` in `launch.args` names one of them;
- that no `launch.env` name starts with `GS_`;
- the ready check and the stop method for the app's system;
- on Windows, that `launch.executable` is in the zip.

`--dry-run` runs the checks and prints the request without making the version.

### Monitor and stop

```bash
survivalservers game-servers list --app breakwire-windows             # starting and running; --status stopped,failed for ended ones
survivalservers game-servers list --label queue:ranked            # by label, key:value
survivalservers logs gs_... --follow                         # the console as it happens
survivalservers logs gs_... --tail 500                       # the last 500 lines, also after it exited (kept 90 days)
survivalservers logs list gs_...                             # its other log files
survivalservers files gs_... Saved/Logs                      # its folder while it runs; the kept files after exit
survivalservers files get gs_... Saved/Crashes/CrashContext.runtime-xml  # -o FILE to choose where it goes
survivalservers game-servers events gs_...                        # its timeline
survivalservers game-servers stop gs_... --shutdown-time 30s
```

`game-servers start` also takes `--version` or `--branch`, `--region` (repeatable), `--env NAME=value`, `--arg`, `--label name=value`, `--external-id`, `--mode auto|warm|cold` and `--ttl 2h`.

`files` takes the path the game sees. On Windows it starts at the game server's own folder. On Linux it is the path in the image, such as `/opt/game/Saved/Logs`.

### Persistent servers and pools

```bash
survivalservers persistent-servers create server.json --wait      # in the API's own shape
survivalservers backups take ps_... --label "Before the patch" --lock
survivalservers backups download bkp_... -o saves.zip             # checks every file's SHA-256
survivalservers backups upload ps_... saves.zip                   # the persistent server stays as it is until you restore
survivalservers persistent-servers update ps_... --version 1.8.2  # at its next restart; --apply now for at once
survivalservers persistent-servers stop ps_... --shutdown-time 60s --wait
survivalservers restore ps_... --backup bkp_...                  # or --at "14:00" --tz Europe/Berlin
survivalservers rollback --app brinewatch-linux --before "2026-10-30 14:00 Europe/Berlin"   # shows the preview, then asks
survivalservers pools set breakwire-windows europe-west --branch live --cpu 2 --memory-gb 4 --mode auto --min 2 --max 20
```

`persistent-servers update --file changes.json` sends other changes too: `env`, `args`, `labels`, `backup`, `restart` and `when_empty`. `pools set` keeps `--min` ready under `--mode fixed`, the default. Under `auto` the pool grows with recent starts up to `--max`.

### Commands

```bash
survivalservers profile get brinewatch-linux 1.8.2 > game-profile.json     # a version's connections and game commands
survivalservers profile set brinewatch-linux 1.8.2 game-profile.json
survivalservers command ps_... save                               # a game command on one persistent server
survivalservers command ps_... announce text="Double XP starts now"
survivalservers command ps_... --line "status" --via rcon          # a line as it is
survivalservers bulk-actions command --app brinewatch-linux --label mode:pvp --dry-run announce text="Restart at 06:00"
survivalservers bulk-actions command --app brinewatch-linux --wait save   # waits, then prints each different reply
survivalservers bulk-actions replies bulk_...
survivalservers players ps_...                                     # who is playing on it now
survivalservers config get ps_...                                  # a persistent server's game settings
survivalservers config set ps_... xp_rate=2 max_players=60 --live
survivalservers saved create "Double XP weekend" command request.json --cron "0 18 * * 5" --time-zone America/New_York
survivalservers saved list
survivalservers saved run sav_...
```

- Game commands and connections come from the version's [game profile](https://studio.survivalservers.com/docs/concepts/game-profiles/).
- `command` and `players` take a persistent server's ID or a game server's ID.
- A console line reaches a game that reads whole lines. A Windows game that reads single key presses, such as Unturned, won't see it: use RCON if the game has it.
- A bulk command goes to persistent servers, or to game servers with `--target game_servers`. Pick them with `--app`, `--version`, `--branch`, `--region`, `--continent`, `--label`, `--status`, `--id` or `--empty`.
- `--dry-run` shows who a bulk command reaches and who it skips, and sends nothing.
- Not run on real machines yet: saved actions (`saved`).

### Use it in CI

`survivalservers deploy` releases a build in one step. It uploads the zip, or makes the build from an image, and waits for the build. Then it creates the version from your settings file, waits until a region has it, and points the branch at it.

```yaml
- name: Release the server
  env:
    GS_API_KEY: ${{ secrets.GAME_SERVERS_API_KEY }}
  run: >-
    survivalservers deploy breakwire-windows --zip build/BreakwireServer-Win64.zip --settings version.json
    --name "1.4.${{ github.run_number }}" --branch beta --idempotency-key "release-${{ github.run_id }}"
```

```text
Deployed breakwire-windows 1.4.7 (build bld_...): ready in 1 of 1 regions; branch beta points at it.
```

- `--idempotency-key K` sends `K` with the command's first write and `K-2`, `K-3` with later ones. A re-run of the same job repeats nothing: a finished upload is found again instead of made twice.
- Give CI its own key with `apps:read`, `apps:write` and `builds:write`, limited to the app it releases.
- `deploy` waits up to 45 minutes for the build, then up to 45 more for the version. `--timeout` changes both.

### Set up your AI tools

`survivalservers ai setup` adds our MCP server and rules to the AI coding tools on your computer. Each tool starts the server on your computer, and the server reads your key from `GS_API_KEY`: set it in the environment you start your tools from. Setup writes no key. See [Build with AI](https://studio.survivalservers.com/docs/guides/build-with-ai/).

```bash
survivalservers ai setup
```

```text
This adds no key. Each tool starts our MCP server on this computer, which reads GS_API_KEY from your environment.

  Cursor       add survival-servers to ~/.cursor/mcp.json
  Rules        write ~/.agents/skills/survival-servers/SKILL.md

Make these changes? [y/N]
```

- It finds Claude Code, Codex, Cursor and VS Code.
- `--project` writes the files for the repository in this folder instead.
- `--yes` skips the question.
- It leaves an entry that is already there as it is.
- No AI tool has started the local server from these files yet.

### Output and exit codes

Every command takes `--json` to print the API's own JSON for your scripts, and with it an error from the API is JSON on standard error too. The exit code says how it went:

| Code | Meaning |
|---|---|
| `0` | Done |
| `1` | Refused or failed; the reason is printed |
| `2` | A wrong command line |
| `3` | A wait ran out before the object settled |
| `4` | The API was busy or out of reach (a rate limit, an outage): try again |

After exit code `3`, run the command the error names to keep waiting. After a start or a create it is a `get ... --wait`, and what was made is printed first (as JSON with `--json`), so nothing is made twice. `--timeout` sets how long a `--wait` waits: 30 minutes for builds and versions, 10 for game servers, 15 for persistent servers.

### Command reference

`COMMAND --help` (or `help COMMAND`) lists a command's flags.

| Command | Does |
|---|---|
| `account` | Studio, limits and free vCPU-hours |
| `regions`, `continents` | Where game servers can run |
| `apps list`, `get`, `create` | Apps |
| `apps icon`, `banner`, `color` | An app's look |
| `studio rename`, `icon`, `accent` | The studio's look |
| `builds upload`, `image`, `get`, `list`, `delete` | Builds |
| `registry login` | Docker login to our registry |
| `versions create`, `list`, `get`, `delete`, `deprecate` | Versions |
| `branches set`, `list` | Set or list branches |
| `deploy` | Build, version and branch in one step |
| `game-servers start`, `list`, `get`, `stop` | Game servers |
| `game-servers events` | A game server's timeline |
| `logs`, `logs list` | Read, follow or list logs |
| `files`, `files get` | List a folder; download a file |
| `persistent-servers list`, `get` | Persistent servers |
| `persistent-servers create` | Make one |
| `persistent-servers start`, `stop` | Start or stop one |
| `persistent-servers restart` | Restart one |
| `persistent-servers update` | Change one |
| `backups list`, `take`, `delete` | Backups of a persistent server |
| `backups lock`, `unlock` | Keep one until you unlock it |
| `backups download`, `upload` | A zip of a backup, or saves as one |
| `restore` | A persistent server back to a backup or a time |
| `persistent-servers backup` | Same as `backups take` |
| `persistent-servers backups` | Same as `backups list` |
| `persistent-servers restore` | Same as `restore` |
| `rollback` | Many persistent servers back to a past time |
| `bulk-actions watch`, `undo` | Follow or undo one |
| `bulk-actions retry`, `resume` | Retry or resume one |
| `pools list`, `set`, `remove` | Pools of ready servers |
| `profile get`, `set` | A version's game profile |
| `profile templates` | Public profiles for known games |
| `command` | A command on one game or persistent server |
| `bulk-actions command` | A command on many game or persistent servers |
| `bulk-actions list`, `get`, `cancel` | Bulk actions |
| `bulk-actions replies` | Replies, grouped by text |
| `players` | Players on a game or persistent server |
| `config get`, `set` | A persistent server's game settings |
| `saved list`, `create`, `delete` | Saved actions |
| `saved run`, `pause`, `resume` | Run or hold one |
| `usage`, `prices` | vCPU-hours, cost and prices |
| `ai setup` | Set up your AI tools |
| `mcp` | The MCP server an AI tool starts |
| `dev` | Your game server on this computer, [as our machines run it](https://studio.survivalservers.com/docs/game-server/test-locally/) |
| `version`, `help` | Version and help |

`survivalservers help` lists every command and its main flags. A command given a wrong flag prints all of its own flags.
