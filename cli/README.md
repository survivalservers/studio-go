# survivalservers

The command-line tool for the Survival Servers Studio API: upload builds, create versions, start and stop game servers,
follow their logs, and run persistent servers and ready servers, from a studio's own computer or its CI. One program
for Windows, macOS and Linux with no dependencies. Apache-2.0.

The docs page for studios is `guides/command-line` in the docs; `survivalservers help` lists every command.

```bash
export GS_API_KEY=gsk_live_...            # or --key-file; a key on the command line is refused
survivalservers apps create brawl-server --os windows --continent north-america
survivalservers deploy brawl-server --zip build/BrawlServer.zip --settings version.json --name 1.4.2 --branch live
survivalservers game-servers start brawl-server --wait
survivalservers logs gs_... --follow
survivalservers apps icon brawl-server art/icon.png     # PNG, JPEG or WebP up to 1 MiB; also `apps banner`, `apps color`
survivalservers ai setup                                # our MCP server and rules in the AI tools on this computer
survivalservers dev -- ./BrawlServer                    # your game server on this computer, as our machines run it
```

- `--json` on every command prints the API's own JSON.
- Exit codes: 0 done, 1 refused or failed (with the API's reason and what to do next), 2 a wrong command line,
  3 a wait ran out before the object settled.
- `--idempotency-key K` gives the command's first write the key `K` and later writes `K-2`, `K-3`, so re-running a
  CI step repeats nothing; a zip upload that already finished is found again instead of made twice.
- `versions create` and `deploy` check the settings before sending them (the same checks as the MCP server's).
- `ai setup` finds Claude Code, Codex, Cursor and VS Code, shows what it will add and asks first (`--yes` skips the
  question). `--project` writes one repository's files instead. It needs no API key and writes none: each tool signs
  in with a browser. It adds only our own entry to each file and leaves an entry that is already there alone.
  `GS_MCP_URL` names another MCP address.
- `mcp` is the MCP server itself, the program an AI tool starts (`survivalservers mcp`, the key in `GS_API_KEY`), or
  with `--http ADDR` the hosted server (`../mcp`).
- `dev` runs a game server on this computer with the same `GS_*` variables, allocation file and local API as our
  machines (`../localapi/dev`).

## Development

```bash
go test ./...                 # against a fake API and fake storage; nothing leaves this computer
go build ./cmd/survivalservers
```

It shares its API client with the MCP server (`../client`, module `github.com/survivalservers/studio-go`), and
builds in the MCP server (`../mcp`) and the local API (`../localapi`), each wired with a `replace` until published.

## Release builds

```bash
./release.sh 0.1.0     # dist/survivalservers_0.1.0_<os>_<arch>.tar.gz (.zip for Windows) and dist/SHA256SUMS
```

Linux, macOS and Windows, each on x86-64 and ARM: static programs with the version stamped in, built so the same
commit always gives byte-identical archives. They are not code-signed. On macOS a downloaded copy is allowed once
under System Settings, Privacy and Security (or `xattr -d com.apple.quarantine survivalservers`); on Windows, SmartScreen's
"More info", then "Run anyway". Check a download against `SHA256SUMS`.
