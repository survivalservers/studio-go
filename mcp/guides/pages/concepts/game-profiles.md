# Game profiles

> Describe how to reach your game, its commands, settings and players once, in plain JSON that the panel, the API and your own tools read.

A game profile describes one version of your game in plain JSON. It says how to reach the game, its commands and settings, how to read its players, and its maps. The panel draws buttons, forms, a player list and a map from it. The API, the command line and AI tools use the same names. Every part is optional, and a version without a profile runs the same.

```json
{
  "connections": {
    "rcon": {"type": "rcon", "port": "rcon", "password_env": "RCON_PASSWORD"}
  },
  "commands": [
    {"id": "save", "label": "Save", "via": "rcon", "send": "save"},
    {"id": "announce", "label": "Announce", "via": "rcon", "send": "broadcast {{text}}",
     "args": [{"name": "text", "label": "Message"}]},
    {"id": "kick", "label": "Kick", "via": "rcon", "send": "kick {{player}} {{reason}}", "confirm": true,
     "args": [{"name": "player", "type": "player"}, {"name": "reason", "optional": true}]}
  ]
}
```

## Set a profile

Send `profile` when you create a version, or replace it later. A profile can change after a version is made, because it changes no bytes of the build:

```bash
curl -X PUT "https://studio.survivalservers.com/v0/apps/brinewatch-linux/versions/1.8.2/profile" \
  -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" \
  -d @server-profile.json
```

Each save adds one to the profile's `revision` and a line to the audit log. `{}` clears the profile. A new version with no `profile` starts from the app's newest profile, so a new build keeps its commands. Unknown keys are refused, so a typing mistake never looks like a saved setting. In the panel, open a version and choose **Edit game profile**.

### Start from a known game

Ready profiles exist for some well-known games, and `GET /v0/profile-templates` lists them. A template's `profile` is ready to save with the call above:

```bash
curl "https://studio.survivalservers.com/v0/profile-templates" -H "Authorization: Bearer $API_KEY"
curl "https://studio.survivalservers.com/v0/profile-templates/TEMPLATE" -H "Authorization: Bearer $API_KEY" | jq .profile > server-profile.json
```

Or with the command-line tool:

```bash
survivalservers profile templates
survivalservers profile set YOUR_APP YOUR_VERSION --from TEMPLATE
```

In the panel, pick one under **Start from a known game** when you edit a profile.

- Each template names the ports and env values your version must have, and any setup the game needs.
- Its `proven` and `not_proven` lists say what has run against the real game and what has not.

## Connections

A connection is a way to reach the running game. Name it what you like and point commands at it with `via`.

| `type` | Reaches the game through | Fields |
|---|---|---|
| `console` | Its standard input | None |
| `rcon` | Source RCON over TCP | `port`, `password_env` |
| `http` | Its own web interface | `port`, `headers`, basic auth |
| `a2s` | A Steam query port, for players | `port` |
| `isle_rcon` | The Isle Evrima's own RCON over TCP | `port`, `password_env` |

Basic auth on `http` uses `user` and `password_env`.

Make an `rcon`, `isle_rcon` or `http` connection's port an [admin port](https://studio.survivalservers.com/docs/game-server/environment-and-allocation/#admin-ports) (`"port_policy": "none"`): the connection works from the machine, and the port is never open to the internet.

The Isle Evrima has an RCON of its own, which Source RCON clients cannot talk to. Over an `isle_rcon` connection, a command is the game's command name and its arguments, comma separated where it takes several: `announce Restart in 10 minutes`, `kick 76561198000000001,AFK`, `ban Name,76561198000000001,Reason,0`, `save`. A command name the game does not list answers `422`. Its players need nothing else: `{"players": {"via": "rcon"}}` with the connection named `rcon`. A version's stop and save commands can use it too (`"type": "isle_rcon"`).

`console` is always there, so a profile names only the others. A typed line reaches a game that reads whole lines from its console. A Windows game that reads single key presses, such as Unturned, does not see typed lines: use RCON or its stop signal.

`port` names one of the version's ports, and `password_env` names one of its env values. Passwords stay in the version's secrets, never in a profile. A profile that names a port or env value the version lacks answers `422`. The machine agent connects from the game's own machine, so an admin port never needs to be open to the internet.

The machine agent reads a `password_env` value from memory and never writes it to a file. Some games, such as Minecraft and Palworld, take their password only from their own config file. That file then holds it, in the game server's folder and its backups.

## Game commands

A game command has an `id`, a `label` and what to send. `send` is a console line or an RCON command; an HTTP call takes `path`, with optional `method` and `body`. `via` names its connection, `console` by default. `{{name}}` is replaced with an argument.

| Argument `type` | What it takes |
|---|---|
| `text` | Any text on one line. This is the default. |
| `number` | A number |
| `choice` | One of its `choices` |
| `player` | A player's name or ID |

An argument with `"optional": true` may be left out. Set `"confirm": true` on anything that throws a player out or loses data, and the panel asks first.

Values are escaped for URLs in `path` and for JSON in `body`. Values never contain a line break, so one value cannot become a second command.

## Run a game command

The panel shows a version's game commands as buttons in the console, and lists them under **Send command** in Bulk actions. From the API, on one persistent server:

```bash
curl -X POST "https://studio.survivalservers.com/v0/persistent-servers/$ID/command" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d '{"command_id": "announce", "args": {"text": "Double XP starts now"}}'
```

From the command line:

```bash
survivalservers command ps_... announce text="Double XP starts now"
survivalservers bulk-actions command --app brinewatch-linux --label mode:pvp --dry-run announce text="Restart at 06:00"
```

The reply's `delivered` says whether the game took it, and `output` holds what the game answered, up to 4 KiB.

To send a line as it is, name a console or RCON connection instead, and you need neither its port nor its password's name:

```json
{"line": "status", "via": "rcon"}
```

The same `command_id` and `args`, or `line` and `via`, work in [Bulk actions](https://studio.survivalservers.com/docs/guides/bulk-actions/) on many persistent servers or game servers at once. Each one's own version writes the command, so two versions can send it differently. One whose version has no such command is skipped and named.

Not run on real machines yet: the panel's console, game commands in Bulk actions, and game commands on Windows.

## Settings

`config` lists your game's settings, each with a type and the place it lives. The panel draws a form from it, and a change writes only that key. Every other line in the game's file stays as it was, comments and hand edits included. Settings apply to persistent servers; settings on game servers are not available yet.

```json
{
  "config": {
    "files": [{"id": "game", "path": "/data/saves/ServerSettings.ini", "format": "ini"}],
    "settings": [
      {"id": "max_players", "label": "Max players", "type": "int", "default": 40, "min": 2, "max": 100, "popular": true,
       "at": {"file": "game", "section": "Session", "key": "MaxPlayers"}},
      {"id": "xp_rate", "label": "XP rate", "type": "float", "default": 1, "unit": "x", "section": "Rules",
       "at": {"file": "game", "section": "Rules", "key": "XPMultiplier"},
       "live": {"via": "rcon", "send": "setxp {{value}}"}},
      {"id": "server_name", "label": "Server name", "type": "text", "default": "Brinewatch Server",
       "at": {"arg": "-ServerName={{value}}"}}
    ]
  }
}
```

`type` is `bool`, `int`, `float`, `text` or `choice`. A setting lives in one of three places, set in `at`:

| `at` | Where |
|---|---|
| `env` | An environment value |
| `arg` | A launch argument, with `{{value}}` |
| `file` with `key` | A key in one of the game's files |

In a `file` setting, `section` is the ini or toml section. `field` is a field inside a one-line `(A=1,B=2)` value, as Palworld keeps its settings.

| File `format` | Files |
|---|---|
| `ini` | Unreal's `Game.ini` and `Engine.ini`, and other ini files |
| `toml` | TOML. Set `"quote": true` on text settings |
| `properties` | `key=value` files, as Minecraft's `server.properties` |
| `cfg` | Source's `key "value"` files |
| `json` | JSON, with `key` as a path with dots |

On Windows `path` is relative to the top of your build. On Linux it is an absolute path in one of the persistent server's save paths. A save path hides what the image has there, so keep the game's program outside it. For any other format, keep the whole file in [shared settings](https://studio.survivalservers.com/docs/guides/bulk-actions/#shared-settings).

A bool is written `true` or `false` unless you set `true_text` and `false_text`. `default` is the game's own default, shown in the form. It is not written: a setting nobody set stays as the game has it.

A text value can hold `{{server.name}}`, `{{server.id}}`, `{{region}}` and `{{label.<key>}}`, filled in for each persistent server.

### Change a setting

Set a persistent server's own values, in the panel's **Game settings** card or through the API:

```bash
curl -X PATCH "https://studio.survivalservers.com/v0/persistent-servers/$ID/config" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d '{"values": {"max_players": 60, "xp_rate": 2}, "live": true}'
```

Values are written right before each run, because a game rewrites its config when it shuts down. With `"live": true`, a setting with a `live` entry is also sent to the running game now, and `results` says how each one went. `null` takes a value off.

`GET /v0/persistent-servers/{id}/config` returns every setting with its value, where the value came from (`default`, `server` or `config_set`) and whether it waits for a restart (`pending`).

A machine on an older machine agent leaves the game's files as they are. The run starts as usual, and the setting and the persistent server say `not_applied`. A settings change through Bulk actions fails there with the reason `not_applied`. On your own machines, update the machine agent and restart.

To change many persistent servers, put `values` in [shared settings](https://studio.survivalservers.com/docs/guides/bulk-actions/#shared-settings) (a config set in the API). Their value wins over a persistent server's own, so a change to the set reaches every persistent server on it. One whose version has no such setting is skipped by name (`no_setting`).

In October 2026, settings reached Palworld's and Minecraft's files and Valheim's launch arguments on Linux, and each game read them back. Settings have not run on Windows yet.

## Players

`GET /v0/persistent-servers/{id}/players` and `GET /v0/game-servers/{id}/players` ask the running game who is on it. The panel asks every 5 seconds while the game server's page is open. Nothing a list returns is stored, so a position is only ever what it was at that moment.

The machine also counts the players itself the same way, so the Overview, the players charts and `players.count` are real for a game that does not use our SDK:

- It reads every 30 seconds while the game server runs and is not paused. A console line is read every 60 seconds, because each one also shows in the game's console.
- Only the count is kept, never names or positions.
- A game that reports its players on the local API is not read. Its own count wins.
- A count your backend sets with `POST /v0/game-servers/{id}/players` is never replaced by the machine's.
- `players.source` on a game server says where its count came from: `reported`, `api`, `query` or `connection`.
- A paused persistent server still pauses by the game's own count, or by its players' traffic. The machine's count never pauses it.

There are three ways to read players. `players.via` names one of the profile's connections, and its type decides how. These examples assume connections named `query` (`a2s`), `rcon` and `web` (`http`):

```json
{"players": {"via": "query"}}
```

```json
{"players": {"via": "rcon", "send": "ShowPlayers",
             "parse": {"regex": "^(?P<name>[^,]*),(?P<id>\\d+),"}}}
```

```json
{"players": {"via": "web", "path": "/v1/players",
             "parse": {"list": "data.players", "id": "steamId", "name": "name", "x": "pos.x", "y": "pos.y", "ping": "ping"}}}
```

- **A query port** (an `a2s` connection) gives each player's time on, and names when the game shares them. It needs nothing else.
- Some games leave the query port's list empty and keep their player count in their Steam rules instead. `rules_count` names that rule: `{"players": {"via": "query", "rules_count": "PlayerCount_i"}}`. Only the count is read, so the list shows that many players with no names. A rule that is missing, or not a whole number, is an error and never a count of 0.
- **RCON or a console line** needs `send` and a `regex` tried on each line. Its named groups are `id`, `name`, `x`, `y`, `z`, `map`, `team` and `ping`. A line that does not match is left out.
- With `split`, `name` holds every player on one line, split by that text, as Minecraft's `list` answers.
- **An HTTP call** needs `path`, `list` (the path to the list of players in the JSON), and a path for each field you want.

With no `players`, the list is what the game reported on the [local API](https://studio.survivalservers.com/docs/game-server/local-api/), and the machine counts nothing itself. The reply's `source` is `reported`, `query` or `connection`.

In October 2026, Palworld's, Minecraft's and Valheim's lists were read through their profiles with nobody on. A list with real players on it, and map positions, have not been read from a real game yet. A count read from a rule has not run against a real game yet.

A game command with an argument of type `player` shows on each player's row, with that player filled in.

## Maps

`maps` lets the panel draw players on your map. It needs positions in the player list.

```json
{"maps": [{"id": "drowned-coast", "name": "Drowned Coast", "image": "https://cdn.example.com/drowned-coast.png",
           "x": [-40000, 40000], "y": [22500, -22500]}]}
```

`image` is a picture of the whole map on your own site, loaded over HTTPS. `x` is the game's x at the picture's left and right edges, and `y` the game's y at its top and bottom. With several maps, a player's `map` picks the one it shows on.
