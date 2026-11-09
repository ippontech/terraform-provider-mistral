#!/usr/bin/env bash
# The last release that shipped signed binaries, for the go.mod summary of the
# release job (goreleaser-release.yml, RELEASE.md, Approving a release):
#
#   previous-release.sh [<commit>]   prints the tag of the newest release tag
#                                    reachable from <commit> (HEAD by default)
#                                    whose GitHub release carries the signed
#                                    checksums, or nothing when none does
#
# semantic-release tags every version and creates its GitHub release before
# GoReleaser attaches the archives, so a tag, or a release, is not proof that
# users received a version: the signing job may have been refused, or the
# release deleted (RELEASE.md, Approving a release). The baseline is the newest
# earlier tag whose release holds a `_SHA256SUMS.sig` asset, read with gh; a
# version tagged but never signed is skipped, so its module changes are listed
# again rather than shipping unseen, and a failed API call counts as unsigned,
# which widens the comparison and never narrows it. Pre-release tags are left
# out. A release still a draft, its signing refused or pending, is not served
# by the releases/tags endpoint (a 404), so it counts as unsigned too. The
# release job calls it with the parent of the tagged commit, so that the tag
# under release is never its own baseline. Without gh, the newest release tag
# stands for the last signed release, with a warning.
set -euo pipefail
# A failure inside a command substitution stops the script on bash 4.4 and
# later; every substitution also checks its status, for the bash 3.2 of macOS.
shopt -s inherit_errexit 2>/dev/null || true

# release_pattern matches the release tags, as the tag filter of
# goreleaser-release.yml does; the grep below drops the pre-releases.
readonly release_pattern='v[0-9]*.[0-9]*.[0-9]*'

commit=${1:-HEAD}
tags=$(git tag --merged "${commit}" --list "${release_pattern}" | grep -v -- - | sort -rV) || true
if [[ -z "${tags}" ]]; then
	exit 0
fi
if ! command -v gh >/dev/null 2>&1; then
	echo "previous-release: gh is not installed; the newest release tag stands for the last release with signed binaries" >&2
	head -n 1 <<<"${tags}"
	exit 0
fi
# The twenty newest tags: a longer run of unsigned versions is a broken release
# process, not a baseline to look for.
while read -r tag; do
	# {owner}/{repo}: gh fills them from the remote of the current repository.
	assets=$(gh api "repos/{owner}/{repo}/releases/tags/${tag}" --jq '.assets[].name' 2>/dev/null) || continue
	if grep -q '_SHA256SUMS\.sig$' <<<"${assets}"; then
		echo "${tag}"
		exit 0
	fi
done < <(head -n 20 <<<"${tags}")
