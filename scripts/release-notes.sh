#!/usr/bin/env bash
# Release notes from Conventional Commits since the previous final tag (J14.4).
#
#   scripts/release-notes.sh TAG              notes on stdout: TAG's CHANGELOG.md section if it has
#                                             one, else generated from commits; then the known
#                                             limitations (docs/release-notes/KNOWN_LIMITATIONS.md)
#   scripts/release-notes.sh --changelog TAG  prepend the generated section to CHANGELOG.md
#                                             (before tagging a final release; edit, then commit)
#
# Release candidates count from the previous final tag too, so a final's notes cover all its rcs.
set -euo pipefail
cd "$(dirname "$0")/.."
mode=notes
[ "${1:-}" = --changelog ] && { mode=changelog; shift; }
tag=${1:?usage: scripts/release-notes.sh [--changelog] TAG}
end=HEAD
day=$(date -u +%F)
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then end=$tag day=$(git log -1 --format=%cs "$tag"); fi
prev=$(git describe --tags --abbrev=0 --match 'v*' --exclude '*-*' "$end^" 2>/dev/null || true)
range=${prev:+$prev..}$end

generate() {
	local log breaking
	log=$(git log --no-merges --format='%h %s' "$range" | grep -vE '^[0-9a-f]+ chore\(release\)' || true)
	breaking=$(git log --no-merges --format='%h' -E --grep='^BREAKING[ -]CHANGE:' "$range")
	echo "## $tag ($day)"
	[ -z "$prev" ] || printf '\nChanges since %s.\n' "$prev"
	group() { # group TITLE LINES
		[ -n "$2" ] || return 0
		printf '\n### %s\n\n' "$1"
		printf '%s\n' "$2" | sed -E 's/^([0-9a-f]+) ([a-z]+)(\(([^)]*)\))?!?: (.*)$/- **\4** \5 (\1)/; s/^- \*\*\*\* /- /; s/^([0-9a-f]+) (.*)$/- \2 (\1)/'
	}
	bang='^[0-9a-f]+ [a-z]+(\([^)]*\))?!: '
	group "Breaking changes (upgrade notes)" "$(printf '%s\n' "$log" | grep -E "$bang" || true
		for h in $breaking; do printf '%s\n' "$log" | grep -E "^$h " | grep -vE "$bang" || true; done)"
	group Features "$(printf '%s\n' "$log" | grep -E '^[0-9a-f]+ feat(\([^)]*\))?: ' || true)"
	group Fixes "$(printf '%s\n' "$log" | grep -E '^[0-9a-f]+ fix(\([^)]*\))?: ' || true)"
	group Performance "$(printf '%s\n' "$log" | grep -E '^[0-9a-f]+ perf(\([^)]*\))?: ' || true)"
	group Documentation "$(printf '%s\n' "$log" | grep -E '^[0-9a-f]+ docs(\([^)]*\))?: ' || true)"
	group Maintenance "$(printf '%s\n' "$log" | grep -vE '^[0-9a-f]+ (feat|fix|perf|docs)(\([^)]*\))?: ' | grep -vE "$bang" | grep . || true)"
}

if [ "$mode" = changelog ]; then
	grep -q "^## $tag " CHANGELOG.md && { echo "CHANGELOG.md already has $tag" >&2; exit 1; }
	section=$(generate)
	S=$section awk '!done && /^## / { print ENVIRON["S"] "\n"; done = 1 } { print } END { if (!done) print "\n" ENVIRON["S"] }' CHANGELOG.md >CHANGELOG.md.tmp
	mv CHANGELOG.md.tmp CHANGELOG.md
	echo "CHANGELOG.md: added $tag (review the wording, add upgrade notes, commit as chore(release): $tag)"
	exit 0
fi

if grep -q "^## $tag " CHANGELOG.md 2>/dev/null; then
	awk -v t="## $tag " 'index($0, t) == 1 { on = 1; print; next } on && /^## / { exit } on' CHANGELOG.md
else
	generate
fi
printf '\n## Known limitations\n\n'
# Only the list items, with links made absolute (relative ones break on the release page).
base=${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY:-KaanEmec/vitamux}/blob/$tag/docs
grep '^- ' docs/release-notes/KNOWN_LIMITATIONS.md | sed "s#](\.\./#]($base/#g"
