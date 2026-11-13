# Build with AI

> Give your AI coding tool our rules and a key, and let it upload your build, start game servers and read why one failed.

Your AI coding tool can do what these docs show: upload your build, start game servers and read why one failed. It needs our rules and an API key. Nothing else needs installing: the tool calls the API over HTTPS with `curl`.

No AI tool such as Claude Code or Cursor has followed these steps here yet.

## 1. Give it our rules

Our rules tell your tool how to work here. It keeps keys out of game clients, sends an `Idempotency-Key` with every start, dry-runs bulk actions and stops what it starts. They are one [`SKILL.md`](https://studio.survivalservers.com/docs/SKILL.md) file, in the Agent Skills format. Save it where your tool reads skills:

```bash
# Claude Code
mkdir -p ~/.claude/skills/survival-servers
curl -o ~/.claude/skills/survival-servers/SKILL.md https://studio.survivalservers.com/docs/SKILL.md

# Codex, Cursor and VS Code
mkdir -p ~/.agents/skills/survival-servers
curl -o ~/.agents/skills/survival-servers/SKILL.md https://studio.survivalservers.com/docs/SKILL.md
```

`survivalservers ai setup` adds them. For one repository, run this in its folder and commit the files:

```bash
survivalservers ai setup --project
```

It writes `AGENTS.md`, a Claude Code skill, and the MCP settings for Claude Code, Codex, Cursor and VS Code. The MCP settings start our server on your computer and hold no key.

## 2. Give it a key

In the panel, open **Developers**, then **API keys**, choose **New key** and pick **Agent key**. You choose what it may do and on which apps, a time limit for the game servers it starts and a monthly spending cap. [AI agents](https://studio.survivalservers.com/docs/guides/ai-agents/) explains each choice.

Put the key in `GS_API_KEY` in the terminal you start your tool from. Never paste it into the chat:

```bash
export GS_API_KEY=gsk_live_...
```

Your tool sends it in the `Authorization: Bearer` header of each call, as every `curl` example in these docs does.

### Scopes

A plain API key works too. API keys carry scopes, and you can limit each key to apps (`apps`), address ranges (`allowed_ips`) and an end date (`expires_at`). Give the agent only the scopes its work needs:

| Scope | Lets the agent |
|---|---|
| `apps:read` | Read apps, builds, versions and pools |
| `game_servers:read` | List and read game servers |
| `logs:read` | Read logs, files, timelines and crash stack traces |
| `persistent_servers:read` | Read persistent servers and their backups |
| `usage:read` | Read usage, prices and estimates |
| `builds:write` | Upload builds, get a registry login |
| `apps:write` | Make apps, versions, branches and pools |
| `game_servers:write` | Start and stop game servers |
| `persistent_servers:write` | Create, start, stop and restore them |
| `backups:read` | Download a backup as a zip |
| `backups:write` | Upload saves as a backup |

An agent that only reads and diagnoses needs the first five scopes.

Each app, build, version, branch, pool and persistent server the agent makes or changes is in the audit log under its key. A revoked key stops working within seconds.

## 3. Test for free

Test in your own studio. Add a card in the panel to start game servers; the first 2,000 vCPU-hours each month are free.

Two limits apply to an agent key:

- **Time limit.** Game servers it starts stop on their own after 1 hour, the panel's default. A key made through the API with no `time_limit` gets 2 hours.
- **Monthly spending cap.** $50 by default, as soon as it may start game servers.

You set both when you make the key. See [limits](https://studio.survivalservers.com/docs/guides/ai-agents/#limits).

## Run our MCP server locally

The local server is `survivalservers mcp`, a command of the command-line tool, started by your editor over standard input and output. It can read the zip the agent names and upload it from your disk. Your key stays on your computer and goes only to the API.

```json
{
  "mcpServers": {
    "survival-servers": {
      "command": "survivalservers",
      "args": ["mcp"],
      "env": { "GS_API_URL": "https://studio.survivalservers.com" }
    }
  }
}
```

Set `GS_API_KEY` in the environment you start your tool from. Never put the key in this file: tools and repositories share it.

`survivalservers mcp` reads these settings from its environment:

| Setting | Default |
|---|---|
| `GS_API_KEY` | none |
| `GS_API_URL` | `https://studio.survivalservers.com` |
| `GS_READ_ONLY` | off |
| `GS_APPS` | every app |
| `GS_MAX_TTL` | `2h` |

- `GS_API_KEY` is the API key. `survivalservers mcp` refuses a key passed as an argument, where other programs can read it.
- `GS_API_URL` is the API address. Without it, it is `https://studio.survivalservers.com`.
- `GS_READ_ONLY=1` (or `true`) leaves every tool that changes something out of the tool list. The 28 reading tools remain.
- `GS_APPS=breakwire-windows` limits the tools to these apps (slugs or IDs, comma-separated). A game server, persistent server or build reached by its own ID is checked against it too.
- `GS_MAX_TTL` stops each game server the agent starts after this long, and is its `ttl` when the agent sets none. A longer `ttl` is refused. `0` turns the limit off. It takes `30m`, `2h`, `1d` or plain seconds.

Never paste a key into the chat.

## What it can do

Each copy has 51 tools. The local server uploads from your disk with `upload_windows_build` and `upload_save`.

- **Docs:** `setup_guide` and `search_docs`
- **Account:** `get_account` and `list_regions`
- **Apps and builds:** `list_apps`, `create_app`, `upload_windows_build` (local), `begin_windows_upload` and `complete_windows_upload` (hosted), `create_linux_build`, `get_registry_login` and `wait_for_build`
- **Versions:** `create_version`, `list_versions`, `wait_for_version` and `set_branch`
- **Game servers:** `start_game_server`, `wait_for_game_server`, `list_game_servers` and `stop_game_server`
- **After a failure:** `diagnose_game_server`, `read_game_server_log`, `list_game_server_files` and `read_game_server_file`
- **Persistent servers:** `create_persistent_server`, `wait_for_persistent_server`, `list_persistent_servers`, `start_persistent_server`, `stop_persistent_server`, `back_up_persistent_server`, `list_backups`, `plan_rollback`, `roll_back`, `undo_bulk_action`, `download_backup`, `upload_save` (local) and `update_persistent_server`
- **Commands and settings:** `get_game_profile`, `set_game_profile`, `send_command`, `list_players`, `get_server_settings`, `set_server_settings`, `plan_bulk_action`, `run_bulk_action`, `get_bulk_action` and `cancel_bulk_action`
- **Pools:** `set_pool`, `list_pools` and `remove_pool`
- **Cost:** `get_usage`, `get_prices` and `estimate_cost`

How the main tools behave:

- `start_game_server` waits until players can join and returns the address. It waits 120 seconds by default (`wait_seconds`, at most 900).
- `create_version` checks the size, the ports and the arguments that name them, and the ready check. It also checks the stop method for the app's system, and that the executable is in the zip. It says what to change before anything is sent. `dry_run` shows the request without making the version.
- `diagnose_game_server` returns everything in one call: the state and exit, the timeline, the last 80 console lines, the other logs and the kept files.
- Game servers and persistent servers the agent starts carry the label `started_by: mcp`. `list_game_servers` with `started_here` lists only those.
- `plan_bulk_action` works out a command, restart or rollout on many persistent servers or game servers and changes nothing. It returns who it reaches, who it skips and a `plan_id`. `run_bulk_action` runs that plan, and is refused if it no longer reaches the same targets.
- Tools that stop, delete or overwrite something are marked destructive for your client.

No tool stops several game servers at once, so a wrong filter cannot stop all your live game servers. API keys, team members, webhooks and billing settings have no tools.

A connected tool or an agent key can't use `create_app`, `set_branch`, `set_pool`, `remove_pool`, `download_backup` or `upload_save`. It also can't move a persistent server to another version or branch, roll out a version, roll back more than one persistent server, or start a server on a version it names. The API refuses these with `403 agent_not_allowed`. `plan_rollback` works with **View only**. `update_persistent_server`, and `roll_back` or `undo_bulk_action` on one persistent server, need **Change persistent servers**. A plain API key with the scopes above can use every tool.

The prompts `setup_unreal`, `setup_unity`, `setup_godot` and `setup_custom_engine` take an optional `os` (`windows` by default). Each walks the agent through the setup and ends with one test game server. Every written docs page is also a `docs://` resource.

## Start from a template

Each template is a game with a dedicated server for Windows and Linux. Open it in your tool and paste its prompt.

Both prompts use `survivalservers`, which reads your key from `GS_API_KEY`. Set it before you start your tool, with a key that has `apps:read`, `apps:write`, `builds:write` and `game_servers:write`.

### Godot

[Godot server template](https://studio.survivalservers.com/templates/godot-multiplayer/)

```text wrap
Start this Godot project as a Windows dedicated server in Europe West with deploy.sh, and give me the address. Then tell me how to stop it.
```

### Unreal

[Unreal server template](https://studio.survivalservers.com/templates/unreal-dedicated-server/)

```text wrap
Package this Unreal project’s dedicated server for Windows with build-server.bat, upload it with survivalservers, start a game server in Europe West, and give me the address. Then tell me how to stop it.
```

## Example prompts

- "Set up this Unreal project's dedicated server for Linux and start one test game server near me."
- "Upload the zip in Build/WindowsServer and make it version 2.3.1 of breakwire-windows."
- "Game server gs_... crashed. Find out why."
- "Which game servers are still running? Stop the ones you started."
- "Back up the EU persistent server, then move it to version 1.8.2 at its next restart."
- "Version 1.8.2 broke saves at 14:00. Show me a rollback of every persistent server on it to before then, try one first, and wait for my yes."
- "What would 200 matches a day of 2 vCPUs for 30 minutes cost a month?"

Game servers and persistent servers are billed while they run. Our rules tell the agent to stop its test game servers when you are done. Ask it what is still running before you close the session.

## Next steps

- What a connected tool may do, its time limit and spending cap, and how to disconnect it: [AI agents](https://studio.survivalservers.com/docs/guides/ai-agents/).
- Every step from your build to a game server: [quick start](https://studio.survivalservers.com/docs/get-started/quick-start/).
