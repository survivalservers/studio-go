# Go client for the game server API

A small Go client for the public game server API, shared by the command-line tool and the MCP server. Apache-2.0,
standard library only.

- JSON in and out, an `Idempotency-Key` on every `POST` and `PATCH` (random, or yours), and API errors (problem
  details) as a `*Problem` whose message says what to do next.
- A call the API did not do is tried again, 3 times in all: on `429 rate_limited`, and on `503 unavailable` with the
  same `Idempotency-Key`, waiting `Retry-After` up to 30 seconds. Never on `503 capacity_unavailable`, which is yours to
  answer (more regions, or later). `SetRetry` changes both numbers.
- `UploadZip`: a Windows build uploaded in parts straight to storage, each with its exact length, three at once; links
  that run out are replaced by reading the build again; a repeated upload with the same idempotency key finds the
  first build, and with `ResumeDir` a re-run of the same zip carries on with the parts left.
- `Wait` with backoff, and the settled states of builds, versions, game servers and persistent servers.
- `CheckVersion`: a version body checked before it is sent (sizes for the tier, ports and templates, the ready
  check, the stop method for the system, the executable in the zip).
- `DownloadLink` (JSON or redirect) and `Stream` for followed logs.
- `Skill`: the one rules file for AI tools (`skill/survival-servers/SKILL.md`), which `survivalservers ai setup` installs and the
  MCP server's instructions carry. Change the rules there.

```bash
go test ./...
```
