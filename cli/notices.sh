#!/usr/bin/env bash
# Writes the third-party notices for a program built from this folder: Go's own licence (its standard library and
# runtime are in every Go program) and the licence of each module built into it, read from the program itself
# (go version -m) and the module cache. A module of ours (one replaced by a folder beside this one, or one we publish
# under github.com/survivalservers/) is left out. A module with no licence file stops the script: nothing ships
# without its notice.
#
#   ./notices.sh PROGRAM OUT_FILE   write the notices for PROGRAM (any system's build) to OUT_FILE
#   ./notices.sh --list PROGRAM     print the modules it would cover, "path version" one per line; PROGRAM - reads
#                                   go version -m's output from standard input
# Needs Go, and the modules in its cache or a network to fetch them.
set -euo pipefail
die() { echo "notices.sh: $*" >&2; exit 1; }
here=$(cd "$(dirname "$0")" && pwd)

# go version -m prints "dep PATH VERSION SUM" for each module, and under one that is replaced "=> PATH VERSION SUM"
# (another module) or "=> ../folder" (a folder of ours, no version).
modules() {
	awk -F'\t' '
		$2 == "dep" { if (m != "") print m; m = ($3 ~ /^github\.com\/survivalservers\//) ? "" : $3 " " $4; next }
		$2 == "=>" { m = ($4 == "" || $4 == "(devel)" || $3 ~ /^[.\/]/) ? "" : $3 " " $4; next }
		END { if (m != "") print m }'
}

if [ "${1:-}" = --list ]; then
	prog=${2:?usage: ./notices.sh --list PROGRAM}
	if [ "$prog" = - ]; then modules; else go version -m "$prog" | modules; fi
	exit 0
fi
prog=${1:?usage: ./notices.sh PROGRAM OUT_FILE}
out=${2:?usage: ./notices.sh PROGRAM OUT_FILE}
info=$(go version -m "$prog") || die "$prog is not a Go program"
out=$(cd "$(dirname "$out")" && pwd)/$(basename "$out")
cd "$here" # go mod download reads this module's go.sum
gover=$(head -n1 <<<"$info" | awk '{print $NF}')
golicense=$(go env GOROOT)/LICENSE
[ -s "$golicense" ] || die "no LICENSE in Go's own folder ($golicense)"

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
{
	printf 'Third-party notices for %s\n\n' "$(basename "$prog")"
	printf 'This program is built with Go and the modules below. Each one'"'"'s licence follows in full.\n'
	printf '\n==== Go %s (its standard library and runtime)\n\n' "$gover"
	cat "$golicense"
	while read -r path version; do
		dir=$(go mod download -json "$path@$version" | sed -n 's/^[[:space:]]*"Dir": "\(.*\)",\{0,1\}$/\1/p')
		[ -n "$dir" ] && [ -d "$dir" ] || die "cannot find $path $version in the module cache"
		# Licence files by their usual names; never a source file such as copyright_test.go or license.go.
		mapfile -t files < <(find "$dir" -maxdepth 1 -type f \( -iname 'LICENSE*' -o -iname 'LICENCE*' -o \
			-iname 'COPYING*' -o -iname 'NOTICE*' -o -iname 'COPYRIGHT' -o -iname 'COPYRIGHT.*' -o -iname 'PATENTS*' \) \
			! -iname '*.go' | sort)
		[ "${#files[@]}" -gt 0 ] || die "$path $version has no licence file: it cannot ship without its notice"
		for f in "${files[@]}"; do
			printf '\n==== %s %s (%s)\n\n' "$path" "$version" "$(basename "$f")"
			cat "$f"
		done
	done < <(modules <<<"$info")
} >"$tmp"
mv "$tmp" "$out"
trap - EXIT
echo "notices.sh: $(grep -c '^==== ' "$out") notices for $(basename "$prog") in $out"
