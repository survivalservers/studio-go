# Changes

What changed in each release of the Go API client, newest first.

## 0.1.3

- Printing or logging a client no longer shows its API key. Earlier versions (Python 0.1.0, Go v0.1.0 to v0.1.2)
  included the key when the client was printed. If your logs may hold a printed client, make a new key in the
  dashboard and revoke the old one.
- `UploadChunks`: a folder with a dot in its name (ArenaServer-1.4.3) keeps its whole name as the build's name.
- The next step for `not_allowed` and for `example_studio`.
- The AI tools' rules file (`Skill`) has curl send the key on its standard input, never on its command line.

## 0.1.2

- `UploadChunks`: a Windows build (a zip or a folder) sent as only the chunks the studio does not hold yet, so a new
  build of the same game sends little. An API that does not take chunks gets a zip whole. `UploadProgress.Chunks`
  says when the counts are chunks.
- The `chunks` package: a build's files cut into the build store's own chunks.
- `PartLinks` and `SendParts`: a save zip over 5 GiB, up to 32 GiB, uploaded in parts, three at once, a link that ran
  out replaced by reading the upload again. The API answers in parts only where it has them on.
- "Ask me first": `Call.Approval` sends the Approval header for a call made again after a member said yes, and the
  `approval_required` problem's `Approval` names the request a member answers in the panel.
- The next step for `test_daily_limit`.

## 0.1.1

- `VersionRemoved`, and `VersionSettled` also ends when every region is failed or removed.

## 0.1.0

- The first release.
