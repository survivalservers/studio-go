# AI agents

> Give Claude Code, Cursor or another AI tool only the permissions you pick, a time limit and a spending cap, and see everything it does.

An AI agent is an AI tool that works in your studio for you, such as Claude Code or Cursor. It calls the API with an agent key. You choose what it may do, how long the game servers it starts may run, and how much it may spend in a month. Everything it does is marked "By AI" in your audit log, with the person it acted for.

No AI tool such as Claude Code or Cursor has used an agent key here yet. Not run on real machines yet: an agent's bulk actions and persistent server changes.

## Agent keys

Make one on the API keys page: click New key and choose Agent key. You pick what it may do and its limits, below, and you can limit it to a list of addresses. It acts for you, and its actions are marked "By AI" with your name. The key is shown once.

Give the key to the agent in its environment, such as `GS_API_KEY`, and never in the chat. The agent sends it in each call's `Authorization: Bearer` header.

## What it may do

| Choice | Default |
|---|---|
| Apps | All apps |
| View only | On |
| Start and stop game servers | Off |
| Upload builds | Off |
| Change persistent servers | Off |
| Bulk actions | Off |

- **Apps.** The apps it may see and change.
- **View only.** It sees game servers, persistent servers, apps, logs, backups and usage, and previews restores and rollbacks. It changes nothing.
- **Start and stop game servers.** One game server at a time, never many at once.
- **Upload builds.** It uploads builds, makes new versions and sets their game profiles. It starts nothing and moves no server onto a version.
- **Change persistent servers.** It creates, starts, stops, updates, backs up and restores persistent servers, changes their game settings, and rolls back one persistent server or undoes that.
- **Bulk actions.** Commands on one game server or persistent server or on many, restarts, maintenance windows and applying shared settings. It can also stop many game servers at once.

An agent can never do more than your own role allows. Apps, branches, pools, saved actions, API keys, members, billing, webhooks and studio settings stay with people. No agent can change them.

An agent also can't download or upload a backup, change a persistent server's version or branch, roll out a version, or roll back more than one persistent server. It starts servers on a branch, never on a version it names. The API answers these calls with `403 agent_not_allowed`, whatever the switches, and so does any call its switches don't allow.

## Limits

- **Time limit.** Game servers and persistent servers the agent starts stop on their own after this long. The panel offers 30 minutes to 24 hours, or no limit. The default is 1 hour for an agent key. If the agent asks for a longer run, it gets your limit.
- **Monthly spending cap.** How much the game servers and persistent servers the agent starts may cost in a calendar month before it can't start more (UTC). It counts your prices before free vCPU-hours. It's $50 by default once any switch other than Upload builds is on. At the cap, the agent can't start more until the 1st, and the API answers it with `403 agent_spending_cap`. Game servers that are already running keep running until their time limit.
- **Ends.** The agent is disconnected on its own after 7, 30 or 90 days, or a year. 30 days is recommended.

At $0.035 per vCPU-hour (up to 2 GB per vCPU), $50 runs a 2-vCPU, 4 GB game server for about 714 hours.

## See and disconnect agents

The AI agents page lists each connected tool and agent key. Each row shows who connected it, what it may do on which apps, its time limit, its end date and when it was last used. Below that are the latest actions by AI, each with the person the agent acted for.

Disconnect stops every token the agent holds at once. Game servers it started keep running until their time limit. Revoking an agent key on the API keys page disconnects its agent too.

Members see and disconnect the agents they connected. Owners and admins see and disconnect every agent in the studio. When a member leaves the studio, or their role can no longer do what one of their agents was given, that agent is disconnected.

## In the audit log

Every change an agent makes is in the audit log, marked By AI, with the agent's name and the person it acted for. Game server starts and stops by agents are recorded too. Choose By AI on the audit log page, or call `GET /v0/audit-log?by_ai=true`.
