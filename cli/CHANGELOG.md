# Changes

What changed in each release of `survivalservers`, the command-line tool, newest first.

## 0.1.2

- `survivalservers ai setup` adds the local AI server to Claude Code again. In 0.1.0 Claude Code refused it with
  "Invalid environment variable format".
- Waiting for a version that the machines removed from every region now stops and says so ("ready in no region: the
  machines removed it"), instead of saying it failed in every region.
- The guides inside the AI tool (`survivalservers mcp`) match the current docs.

There is no 0.1.1 of the command-line tool: the release named v0.1.1 carries only a Python package.

## 0.1.0 (2026-11-13)

- The first release, with the AI tool (`survivalservers mcp`) and `survivalservers dev` inside.
