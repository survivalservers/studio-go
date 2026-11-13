# Upload builds

> Upload a large Windows build in resumable parts, or bring a Linux image, from your PC or your CI.

A build is your server's files: a zip for a Windows app, or an image for a Linux app. You upload it once. We check it and copy it ahead of time to the regions its versions target. For a new zip build, machines fetch only the chunks that changed since your last one. Chunks are kept for each studio on its own, never shared with another studio.

## A Windows zip, in parts

A zip goes straight to storage in 64 MiB parts. A dropped connection costs only the part that was being sent.

1. **Create the build** with the zip's name, size in bytes and SHA-256:

   ```bash
   curl -X POST "https://studio.survivalservers.com/v0/apps/breakwire-windows/builds" \
     -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
     -d '{"os": "windows", "source": {"type": "upload", "filename": "BreakwireServer-Win64-2.3.0.zip", "bytes": 7340032000, "sha256": "9f2c...b07d"}}'
   ```

   The reply is `201` with `status: awaiting_upload`, the part size in `upload.part_bytes` (`67108864`) and one link per part in `upload.parts`. The links work until `upload.expires_at`, one hour later.

2. **Send each part** with `PUT` to its link. Part `n` is the `part_bytes` bytes that start at `(n - 1) × part_bytes`; the last part is shorter. Send each part with its exact length, since storage refuses a streamed upload. Parts can go in parallel, and a failed part can be sent again. Keep the `ETag` header each part answers with. When the links expire, read the build with `GET /v0/builds/{id}`: while it waits for its upload, every read carries fresh links.

3. **Complete the upload** with every part's number and `ETag`, each part once:

   ```bash
   curl -X POST "https://studio.survivalservers.com/v0/builds/bld_.../complete" \
     -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
     -d '{"parts": [{"number": 1, "etag": "\"a54f0c...\""}, {"number": 2, "etag": "\"7d01e2...\""}]}'
   ```

   The reply is `202` with `status: processing`. A missing part or an `ETag` storage does not know answers `422 validation_failed`, and the build keeps waiting for its parts.

[`survivalservers builds upload`](https://studio.survivalservers.com/docs/guides/command-line/), the samples' quick-start script and the MCP server's `upload_windows_build` do all three steps for you.

## What happens next

While the build is `processing`, the API checks its size and SHA-256, unpacks the zip and checks every name in it. It virus scans the programs and scripts and lists the programs in `executables`, for the version's `launch.executable`. Then the build is `ready` and the `build.ready` [webhook](https://studio.survivalservers.com/docs/guides/webhooks/) fires. A build that cannot be used ends `failed`, with `failure.code` and a `failure.message` that says what to fix:

| `failure.code` | Meaning |
|---|---|
| `checksum_mismatch` | Size or SHA-256 differs from the create call |
| `bad_archive` | Not a readable zip, or a name Windows refuses |
| `too_large` | Over a size or file-count limit |
| `scan_flagged` | The virus scan flagged a file |
| `image_not_found` | Linux: no such image, or its registry did not answer |
| `registry_denied` | Linux: the registry refused our login |

- `bad_archive` also covers a link or special file in the zip, and a file and a folder with the same name. So do two names that differ only in case, and an empty zip.
- `scan_flagged` names the file and what the scan found. No machine gets the build. [Talk to us](https://studio.survivalservers.com/#talk) if you think it is wrong: staff can review it and release it.
- `image_not_found` also covers an image with no `linux/amd64` variant, and a registry that did not answer for 10 minutes. The message says why: its host name could not be looked up, it refused, no answer came in time, or its certificate is one we cannot trust.
- A program over 1 GiB is not scanned. The build still becomes `ready`, and `scan.status` stays `pending`.
- Each file keeps its date from the zip, so a game that checks its own files' dates sees the dates you packed.

## A Linux image

Create the build from the image's full name, with a version tag or a digest rather than `latest`:

```bash
curl -X POST "https://studio.survivalservers.com/v0/apps/breakwire-linux/builds" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d '{"os": "linux", "source": {"type": "image", "image": "registry.kingsmoor.example/breakwire-server:2.3.0"}}'
```

The reply is `202` with `status: processing`. We copy the image into our registry and pin it by digest (`image_digest`), so an outage of your registry never stops a start. The image needs a `linux/amd64` variant. Images are not virus scanned: `scan.status` stays `pending`.

For a private registry, save its login once with `POST /v0/registry-credentials` (`registry`, `username`, `password`) and name the `rcr_...` ID as `source.registry_credential`. The password is never returned. Deleting a login leaves builds already copied working.

Every ready Linux build is checked hourly, and whenever a machine cannot fetch its image. When the image is gone from our registry, the build turns `failed` with `image_not_found` and `build.failed` fires with `data.sequence` 2. Make a new build from the image, then a version from that build.

## Push to our registry

You can push an image to us instead of letting us copy it. Ask for a push login:

```bash
curl -X POST "https://studio.survivalservers.com/v0/registry/token" \
  -H "Authorization: Bearer $API_KEY" -H "Idempotency-Key: $(uuidgen)"
```

The reply has `registry`, `repository`, `username`, `password` and `expires_at`. The login works for one hour, and longer while a push is uploading with it: each layer or manifest it is uploading keeps it working until 10 minutes after that upload ends, up to 6 hours after the login was made. It pushes only under `repository`, your studio's own space. Log Docker in with it, push under `repository`, then create the build with the pushed image's name. An image name under our registry outside your `repository` is refused with `422`. Where pushing is not set up, the call answers `503 unavailable`. [Linux images](https://studio.survivalservers.com/docs/game-server/linux-images/) has the full commands.
`survivalservers registry login` makes the call and logs Docker in.

## From your CI

Any build pipeline (CI) that runs a shell can upload with the three calls above, `curl` and `jq`. This GitHub Actions step keeps the API key in the repository's secrets:

```yaml
- name: Upload the server build
  env:
    API_KEY: ${{ secrets.GAME_SERVERS_API_KEY }}
    ZIP: build/BreakwireServer-Win64.zip
  run: |
    api=https://studio.survivalservers.com/v0
    auth="Authorization: Bearer $API_KEY"
    run="$GITHUB_RUN_ID-$GITHUB_RUN_ATTEMPT"
    build=$(curl -sS --fail -X POST "$api/apps/breakwire-windows/builds" -H "$auth" \
      -H "Idempotency-Key: $run-build" -H "Content-Type: application/json" \
      -d "$(jq -n --arg f "$(basename "$ZIP")" --argjson b "$(wc -c < "$ZIP" | tr -d ' ')" \
        --arg s "$(sha256sum "$ZIP" | cut -d' ' -f1)" \
        '{os: "windows", source: {type: "upload", filename: $f, bytes: $b, sha256: $s}}')")
    size=$(jq -r .upload.part_bytes <<<"$build")
    parts='[]'
    while read -r n url; do
      etag=$(dd if="$ZIP" bs="$size" skip=$((n - 1)) count=1 status=none |
        curl -sS --fail -X PUT --data-binary @- -H 'Content-Type:' -H 'Expect:' -D - -o /dev/null "$url" |
        tr -d '\r' | sed -n 's/^[Ee][Tt][Aa][Gg]: *//p')
      parts=$(jq -c --argjson n "$n" --arg e "$etag" '. + [{number: $n, etag: $e}]' <<<"$parts")
    done < <(jq -r '.upload.parts[] | "\(.number) \(.url)"' <<<"$build")
    curl -sS --fail -X POST "$api/builds/$(jq -r .id <<<"$build")/complete" -H "$auth" \
      -H "Idempotency-Key: $run-complete" -H "Content-Type: application/json" \
      -d "{\"parts\": $parts}"
```

The upload calls need `builds:write`; waiting for the build to be ready (`GET /v0/builds/{id}`) also needs `apps:read`. Give CI its own API key, limited to the app it ships, and never put a key in a game client. To make the version and point a branch at it in the same job, send `POST /v0/apps/{app}/versions` with the build's ID once the build is `ready`, then `PUT /v0/apps/{app}/branches/{branch}` with the new version's name.
`survivalservers deploy` does all of it in one step.

## Limits

| Limit | Default |
|---|---|
| Zip size (`zip_build_gb`) | 30 GB |
| Image size, compressed (`image_build_gb`) | 40 GB |
| Versions per app (`versions_per_app`) | 200 |
| Stored builds, all builds together (`build_storage_gb`) | 500 GB |
| Unpacked zip | 256 GiB and 500,000 files |
| Upload part | 64 MiB, at most 10,000 parts |
| New builds per studio | 1 a second, bursts of 10 |

- The first four are your studio's limits, shown under `limits` in `GET /v0/studio` and raised [on request](https://studio.survivalservers.com/docs/help/get-help/). A zip over the limit is refused at create with `413 too_large`.
- Delete builds you no longer need with `DELETE /v0/builds/{id}`. A build that a version still uses answers `409 in_use`.
- A deleted Linux build's copy of its image leaves our registry at once, unless another build of the app uses the same image. An image you pushed to our registry is removed 30 days after the push, whether or not you delete the build. A deleted zip build's files are kept 30 days after the delete, then removed from our store within a day, its chunks with them unless another of your builds uses them. Chunks a failed build left behind are removed within two days.
- Machines remove a deleted version's files, and the chunks no other version of yours uses, within a few minutes of the delete, once no game server runs it. A machine that was offline removes them when it reconnects.
