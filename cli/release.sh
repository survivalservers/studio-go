#!/usr/bin/env bash
# Release builds of the program for every system, for the download link:
#   ./release.sh 0.1.0        -> dist/<name>_0.1.0_<os>_<arch>.tar.gz (.zip for Windows) and dist/SHA256SUMS
#                                (ops/publish/checksums.sh: every file in dist, checked with sha256sum -c)
# Static programs (no C), paths and build IDs stripped, the version stamped in, file times fixed to SOURCE_DATE_EPOCH
# (else the last commit's time), entries in name order with fixed modes (pack.sh), so the same source and
# SOURCE_DATE_EPOCH always give the same files, in any folder or repository and on any machine: the public run's files
# must match the scanned rehearsal's, hash for hash. Not code-signed: see README.md.
set -euo pipefail
cd "$(dirname "$0")"
version="${1:?usage: ./release.sh VERSION}"
# A release version (X.Y.Z) is CHANGELOG.md's newest, the one cli.go's Version holds (its test keeps them equal), so the
# released program and a copy installed with go install from the module's cli/vX.Y.Z tag print the same. A version
# with a suffix (a check build such as 0.0.0-check) is not a release and is built as asked.
newest=$(sed -n 's/^## \([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\)\([^0-9.].*\)\{0,1\}$/\1/p' CHANGELOG.md | head -n 1)
if [[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ && $version != "$newest" ]]; then
	echo "release.sh: $version is not a version to release: CHANGELOG.md's newest version is $newest." >&2
	echo "Add the release's section to CHANGELOG.md and set cli.go's Version to it in the same change, or build $newest." >&2
	exit 1
fi
name=survivalservers
pkg=./cmd/survivalservers
stamp=github.com/survivalservers/studio-go/cli.Version
targets="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"
epoch="${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct 2>/dev/null || echo 0)}"
out="$PWD/dist"
rm -rf "$out" && mkdir -p "$out"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
for t in $targets; do
	os=${t%/*}
	arch=${t#*/}
	base="${name}_${version}_${os}_${arch}"
	dir="$work/$base"
	mkdir -p "$dir"
	program=$name
	if [ "$os" = windows ]; then program="$name.exe"; fi
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -buildvcs=false \
		-ldflags "-s -w -buildid= -X $stamp=$version" -o "$dir/$program" "$pkg"
	cp LICENSE README.md "$dir/"
	./notices.sh "$dir/$program" "$dir/THIRD_PARTY_NOTICES.txt" >/dev/null # Go's and each built-in module's licence
	# Name order, fixed modes and times (pack.sh): the same bytes on every machine, whatever its folder order or umask.
	if [ "$os" = windows ]; then
		./pack.sh "$dir" "$out/$base.zip" "$epoch"
	else
		./pack.sh "$dir" "$out/$base.tar.gz" "$epoch"
	fi
done
# Every file in dist gets its line, and the check must pass: release files ship with checksums.
../../ops/publish/checksums.sh "$out" >/dev/null
cat "$out/SHA256SUMS"
