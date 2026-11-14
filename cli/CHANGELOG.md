# Changes

What changed in each release of `survivalservers`, the command-line tool, newest first.

## 0.1.2

- `survivalservers builds upload` sends a Windows build as only the chunks your studio does not hold yet, so a new
  build of the same game sends little. An API that does not take chunks gets the zip whole, in parts, as before.
- The AI tool inside (`survivalservers mcp`, now 0.1.1) uploads Windows builds the same way. Its game server and
  persistent server stops, game commands and bulk actions take an `approval`: when the API answers approval_required
  ("Ask me first"), the agent makes the same call again with the request's ID once a member said yes in the panel.
- `survivalservers ai setup` adds the local AI server to Claude Code again. In 0.1.0 Claude Code refused it with
  "Invalid environment variable format".
- `survivalservers ai setup` names GS_API_KEY in Codex's table (`env_vars = ["GS_API_KEY"]`): Codex passes a server
  only a short list of variables from its environment, so the local server never saw the key. A table written by 0.1.0
  needs that line added by hand.
- Waiting for a version that the machines removed from every region now stops and says so ("ready in no region: the
  machines removed it"), instead of saying it failed in every region.
- The guides inside the AI tool (`survivalservers mcp`) match the current docs.
- `survivalservers version` in a copy installed with `go install` prints the version it was released as, the same as
  the downloads.

There is no 0.1.1 of the command-line tool: the release named v0.1.1 carries only a Python package.

## 0.1.0 (2026-11-13)

- The first release, with the AI tool (`survivalservers mcp`) and `survivalservers dev` inside.
