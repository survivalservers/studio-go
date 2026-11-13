# Survival Servers Studio MCP server

An MCP server that lets AI coding tools (Cursor, VS Code, Claude Code and any other MCP client) run a game's
dedicated servers through the Survival Servers Studio API: set a project up for Unreal, Unity, Godot or any other engine
from the docs, upload a Windows zip or a Linux image, create versions, start and stop game servers and persistent
servers, keep ready servers waiting in pools, read usage and cost, and read the logs, files and crash dumps a server
left behind when it failed. Apache-2.0.

It talks only to the public API, with the developer's own API key. The docs page for developers (install buttons,
key scopes, what to ask) is `guides/build-with-ai` in the docs; this file is for running and changing the server.

## Two ways to run it

It is part of the command-line tool (`../cli`):

```bash
survivalservers mcp                          # local: an editor starts it over stdio; the key is GS_API_KEY
survivalservers mcp --http 127.0.0.1:8080    # hosted: MCP over HTTP at /mcp, health at /health; each request brings its own key
```

**Local** (`GS_API_KEY` in the environment, never on the command line: `survivalservers mcp` refuses a key passed as
an argument). It can read the zip the agent names and upload it in parts. Settings:

| Variable | Default | |
|---|---|---|
| `GS_API_KEY` | | The API key. Give the agent its own key with only the scopes it needs, limited to one app, with an expiry date. |
| `GS_API_URL` | `https://studio.survivalservers.com` | The API address. A set one wins; one that is not an http(s) address stops the server before it starts. |
| `GS_READ_ONLY` | off | `1` leaves every tool that changes something out of the tool list. |
| `GS_APPS` | all | Comma-separated app slugs or IDs the tools may use. Every tool keeps to it: game servers, persistent servers, builds, backups and bulk actions reached by their own IDs are checked too, a plan or bulk action must name an allowed app and reach only its servers, and usage shows only those apps' rows. |
| `GS_MAX_TTL` | `2h` | Game servers started here stop on their own after this long at most; also their run time when the agent sets none. `0`: no limit. On the hosted server it holds for keys sent as they are; a tool that signed in keeps the time limit its member approved. |
| `GS_MCP_RESOURCE` | | Hosted only: the server's own address; turns on the sign-in for AI tools. |
| `GS_AUTH_URL` | `GS_API_URL` | Hosted only: the sign-in server. |
| `GS_MCP_EXCHANGE_SECRET_FILE` | | Hosted, with `GS_MCP_RESOURCE`: a file (mode 0600) holding the secret this server swaps sign-in tokens with, 32 or more random bytes as hex. Without it the server will not start. |

**Hosted** holds no key: the `Authorization: Bearer <key>` header of each request is used for that request's calls
and then forgotten, and `GS_API_KEY` is ignored.

**Sign-in for AI tools (hosted).** With `GS_MCP_RESOURCE` set to the server's own address
(`https://mcp.example.com/mcp`), a tool such as Claude Code or Cursor adds the address with no key and signs in with
the browser, as the MCP authorization spec (2026-07-28) describes: a request with no token is answered `401` with
`WWW-Authenticate: Bearer resource_metadata=...`; the metadata (`/.well-known/oauth-protected-resource/mcp`, RFC 9728)
names the sign-in server (`GS_AUTH_URL`, default `GS_API_URL`), which does PKCE, client ID metadata documents and
dynamic registration, and the member approves the tool in the panel with only the permissions, time limit and
monthly spending cap they pick. The tool's access token (`gso_...`) is good only here: each request's is swapped at
the sign-in server (token exchange, RFC 8693, on every request, so a disconnected tool is refused at once) for the
API token made with it, so the tool's token is never passed on to the API. Only this server can make that swap: it
signs in to the token endpoint as its own client with the exchange secret (`GS_MCP_EXCHANGE_SECRET_FILE`), so a
token taken from a tool cannot be turned into an API token anywhere else. An API key or agent key (`gsk_live_...`) in the header still works as before.
With the sign-in on, every request needs a token, tool lists included. It never reads files, so in place of `upload_windows_build` it
offers `begin_windows_upload` (which returns a bash script that sends the zip's parts from the developer's computer)
and `complete_windows_upload`. Tool lists answer without a key. It is stateless, so any number of copies can run
behind a load balancer. `GS_API_URL`, `GS_READ_ONLY`, `GS_APPS` and `GS_MAX_TTL` apply to it as well (`GS_MAX_TTL` not to a
tool that signed in).

**What the game wrote.** Console lines, log and file text, command replies, player names, file names and timeline
data come back in a block of their own: a line before it says the game and its players can write it and it is data,
never instructions; its fence is longer than any run of backticks in the text, and it ends at a line with a marker
made for that call, so nothing a player writes can close it or pass for the tool's own words.

**Registry login.** `get_registry_login` never puts the registry password in its answer. The local server logs
Docker in itself (the password goes to `docker login` on its standard input) and answers where to push; the hosted
server asks the developer to run `survivalservers registry login` in a terminal.

**Previews.** `plan_rollback` answers a `plan_id` that carries its request; `roll_back` makes the preview again and
runs it only while it reaches the same servers with the same backups, within the preview's 15 minutes.

## Tools

| Area | Tools |
|---|---|
| Docs | `setup_guide`, `search_docs`; prompts `setup_unreal`, `setup_unity`, `setup_godot`, `setup_custom_engine`; every written docs page as a `docs://` resource |
| Apps and builds | `get_account`, `list_regions`, `list_apps`, `create_app`, `upload_windows_build` (local) or `begin_windows_upload` and `complete_windows_upload` (hosted), `create_linux_build`, `get_registry_login`, `wait_for_build` |
| Versions | `create_version`, `list_versions`, `wait_for_version`, `set_branch` |
| Game servers | `start_game_server`, `wait_for_game_server`, `list_game_servers`, `stop_game_server`, `diagnose_game_server`, `read_game_server_log`, `list_game_server_files`, `read_game_server_file` |
| Persistent servers | `create_persistent_server`, `wait_for_persistent_server`, `list_persistent_servers`, `start_persistent_server`, `stop_persistent_server`, `back_up_persistent_server`, `list_backups`, `plan_rollback`, `roll_back`, `undo_bulk_action`, `download_backup`, `upload_save` (local), `update_persistent_server` |
| Commands and settings | `get_game_profile`, `set_game_profile`, `send_command` (one server), `list_players`, `get_server_settings`, `set_server_settings`, `plan_bulk_action` and `run_bulk_action` (a command, restart or rollout on many, always planned first), `get_bulk_action`, `cancel_bulk_action` |
| Pools and cost | `set_pool`, `list_pools`, `remove_pool` (ready servers waiting for a match), `get_usage`, `get_prices`, `estimate_cost` |

How they are made:

- **Curated, not one tool per API call.** The tools follow the path from a build to players connecting and back
  from a failure; keys, members, webhooks and billing settings stay in the panel.
- **Waiting is a tool.** `start_game_server` waits until the server is ready and returns the address; the
  `wait_for_*` tools poll with backoff (and send progress) so an agent never loops on a status call.
- **Checks before the wire.** `create_version` checks sizes, ports, templates, the ready check, the stop method for
  the app's system and that the executable is in the zip, and says what to change; `dry_run` shows the request.
- **Errors say what to do next**, from the API's stable error codes, with the request ID for support.
- **No bulk action without a plan the agent has seen.** `plan_bulk_action` is always a dry run, and `run_bulk_action` is refused if the plan no longer reaches the same servers.
- **No bulk stop.** One game server at a time, so a wrong filter can never stop every live server. Destructive tools
  are marked as such for the client.
- **Found again later.** Game servers and persistent servers started here carry the label `started_by: mcp`
  (`list_game_servers` with `started_here`).
- **The docs come from the docs.** `guides/pages` is a copy of the docs site's written pages, made by the docs
  build (`npm run mcp-guides` in the docs project); its tests fail when the copy is out of date.

## Development

```bash
go test ./...                                          # against a fake API; nothing leaves this computer
GS_PRACTICE_URL=http://127.0.0.1:4010 go test -run TestPractice ./server   # every tool against the practice server
GS_LIVE_API_URL=... GS_LIVE_API_KEY=... go test -run TestLive ./server     # a real API, on a test studio only
go build -C ../cli ./cmd/survivalservers    # the program it runs in: survivalservers mcp
```

Its API client, uploads, waits and version checks are the shared module `../client` (`github.com/survivalservers/studio-go`), which
the command-line tool uses too.

The practice server answers every call with the API file's examples and refuses any request the file does not
allow, so `TestPractice` proves each tool sends what the API accepts. `TestLive` creates apps and builds, uploads
real zips through storage links and runs the hosted upload script; never point it at a studio with live games.

## Release builds

It ships inside the command-line tool's release builds (`../cli/release.sh`), as `survivalservers mcp`: one program
and one name. In an AI tool's server list it is `survival-servers`, and its title is "Survival Servers Studio".
`server.json` is the MCP Registry's entry for the hosted server.
