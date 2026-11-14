#!/usr/bin/env bash
# Packs one release folder as a download, the same bytes on every machine (release.sh runs it for each one):
#   ./pack.sh DIR OUT EPOCH     OUT ends in .zip (Windows) or .tar.gz
# Every entry is in name order (LC_ALL=C), never the order a machine's file system lists a folder in. Every folder and
# program gets mode 0755 and every other file 0644, whatever the umask. Every time is EPOCH (SOURCE_DATE_EPOCH), in UTC.
# The zip keeps its folder entries (DIR/ first, as zip -r gave them) and no extra attributes (-X: no owner, no extended
# times); the tar.gz has owner 0 and no gzip name or time.
# Only plain files and folders go in. DIR holding anything else (a symbolic link, a device, a pipe, a socket) stops the
# run before anything is changed or written, naming each such entry: zip follows a link and would put the file it
# points to, wherever that is, into a public download.
set -euo pipefail
dir=${1:?usage: ./pack.sh DIR OUT EPOCH}
out=${2:?usage: ./pack.sh DIR OUT EPOCH}
epoch=${3:?usage: ./pack.sh DIR OUT EPOCH}
case $epoch in '' | *[!0-9]*) echo "pack.sh: EPOCH is a whole number of seconds" >&2; exit 2 ;; esac
[ ! -L "$dir" ] || { echo "pack.sh: $dir is a symbolic link, not a folder" >&2; exit 2; }
[ -d "$dir" ] || { echo "pack.sh: no folder $dir" >&2; exit 2; }
case $out in *.zip | *.tar.gz) ;; *) echo "pack.sh: OUT ends in .zip or .tar.gz" >&2; exit 2 ;; esac
out=$(cd "$(dirname "$out")" && pwd)/$(basename "$out")
parent=$(dirname "$dir")
base=$(basename "$dir")
cd "$parent"
other=$(find "$base" ! -type f ! -type d | LC_ALL=C sort)
if [ -n "$other" ]; then
	echo "pack.sh: only plain files and folders go into a download; not packed:" >&2
	sed 's/^/  /' <<<"$other" >&2
	exit 2
fi
find "$base" -type d -exec chmod 0755 {} +
find "$base" -type f -perm /111 -exec chmod 0755 {} +
find "$base" -type f ! -perm /111 -exec chmod 0644 {} +
find "$base" -exec touch -h -d "@$epoch" {} +
rm -f -- "$out"
case $out in
	*.zip) find "$base" | LC_ALL=C sort | TZ=UTC zip -qX -@ "$out" ;;
	*) tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$epoch" -cf - "$base" | gzip -n > "$out" ;;
esac
