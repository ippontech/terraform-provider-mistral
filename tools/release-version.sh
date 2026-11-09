#!/usr/bin/env bash
# The version of the next release, read from the Conventional Commits since the
# last release tag by svu (https://github.com/caarlos0/svu), which must be on
# the PATH: mise.toml pins it, and the workflows that run this script install
# the same version, checked against a pinned hash. A maintainer still creates
# the tag by hand (RELEASE.md, Cutting a release): this script only computes
# and checks.
#
#   release-version.sh next         prints the next version, from HEAD
#   release-version.sh check <tag>  fails unless <tag>, on HEAD, is that version
#   release-version.sh pending      prints, in Markdown, what main holds that no
#                                   release has published, and the next version
#
# The rules are those of RELEASE.md (Versions), computed by svu with --v0: a
# breaking change (`!` or BREAKING CHANGE) raises the major, a feat the minor,
# a fix, perf, build, refactor or docs commit the patch, and chore, ci, test
# and style nothing; a 0.x version stays 0.x until a maintainer tags 1.0.0 by
# hand. A pre-release tag
# (v0.2.0-rc.1) counts by its core version, and is never the previous release.
# The first release is v0.1.0.
set -euo pipefail

# release_pattern matches the release tags, as the tag filter of
# goreleaser-release.yml does; pre-releases are excluded where it is used.
readonly release_pattern='v[0-9]*.[0-9]*.[0-9]*'

# patch_types matches the subjects that release a patch besides fix
# (RELEASE.md, Versions); chore, ci, test and style release nothing.
readonly patch_types='^(perf|build|refactor|docs)(\([^)]*\))?!?: '

# previous_release prints the last release tag reachable from the commit $1,
# pre-releases excluded, or nothing when there is none.
previous_release() {
	git describe --tags --abbrev=0 --match "${release_pattern}" --exclude '*-*' "$1" 2>/dev/null || true
}

# next_after prints the next version from the release tag $1 to HEAD. svu
# considers only the tags matching --tag.pattern: the exact previous tag keeps
# it from reading a later tag on HEAD, or a pre-release. With no previous
# release (an empty $1) the first version is v0.1.0, whatever the commits:
# svu fails when no tag matches its pattern.
next_after() {
	if [[ -z "$1" ]]; then
		echo v0.1.0
		return
	fi
	local next
	next=$(svu next --v0 --tag.pattern "$1")
	# svu raises the patch for a fix only; RELEASE.md (Versions) also releases
	# perf, build, refactor and docs commits, which change the binary or the
	# documentation the registries publish with each version.
	if [[ "${next}" == "$1" ]] && git log --format=%s "$1..HEAD" | grep -qE "${patch_types}"; then
		next=$(svu patch --tag.pattern "$1")
	fi
	echo "${next}"
}

# core strips the pre-release and the build metadata of a version.
core() {
	local v=${1%%+*}
	echo "${v%%-*}"
}

mode=${1:-}
case "${mode}" in
next)
	next_after "$(previous_release HEAD)"
	;;
check)
	tag=${2:?usage: release-version.sh check <tag>}
	if [[ "$(git rev-parse "${tag}^{commit}")" != "$(git rev-parse HEAD)" ]]; then
		echo "release-version: ${tag} does not point at HEAD" >&2
		exit 2
	fi
	previous=$(previous_release HEAD^)
	want=$(next_after "${previous}")
	if [[ "$(core "${tag}")" != "${want}" ]]; then
		if [[ -n "${previous}" && "${want}" == "${previous}" ]]; then
			echo "release-version: ${tag}, but no commit since ${previous} calls for a release (only chore, ci, test or style)" >&2
		else
			echo "release-version: ${tag}, but the commits since ${previous:-the first commit} call for ${want}" >&2
		fi
		exit 1
	fi
	echo "${tag} is the version the commits since ${previous:-the first commit} call for"
	;;
pending)
	previous=$(previous_release HEAD)
	next=$(next_after "${previous}")
	range=${previous:+${previous}..}HEAD
	echo "Changes on \`main\` that no release has published yet, since ${previous:-the first commit}. Updated by \`release-reminder.yml\` on every push to \`main\` and every release tag."
	echo
	if [[ -n "${previous}" && "${next}" == "${previous}" ]]; then
		echo "**Nothing to release**: only chore, ci, test or style commits since ${previous}."
	else
		echo "**Next version: \`${next}\`** (\`make release-version\`). To release it, follow *Cutting a release* in \`RELEASE.md\`."
	fi
	for group in "Features:^feat(\\(.*\\))?!?: " "Fixes:^fix(\\(.*\\))?!?: " "Other changes:"; do
		title=${group%%:*} pattern=${group#*:}
		if [[ -n "${pattern}" ]]; then
			lines=$(git log --format='%s (%h)' "${range}" | grep -E "${pattern}" || true)
		else
			lines=$(git log --format='%s (%h)' "${range}" | grep -v -E '^(feat|fix)(\(.*\))?!?: ' || true)
		fi
		[[ -z "${lines}" ]] && continue
		echo
		echo "### ${title}"
		echo
		sed 's/^/- /' <<<"${lines}"
	done
	;;
*)
	echo "usage: release-version.sh next | check <tag> | pending" >&2
	exit 2
	;;
esac
