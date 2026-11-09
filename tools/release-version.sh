#!/usr/bin/env bash
# The version of the next release, read from the Conventional Commits since the
# last published release. A maintainer still decides each release and creates
# its tag (RELEASE.md, Cutting a release): this script computes, checks,
# reminds, and tags only on that confirmation.
#
#   release-version.sh next         prints the version to tag next, or fails
#                                   when there is nothing to release
#   release-version.sh check <tag>  fails unless <tag>, on HEAD, is at least the
#                                   version the commits call for, and above
#                                   every other release tag
#   release-version.sh pending      prints, in Markdown, what main holds that no
#                                   release has published, and the next version
#   release-version.sh tag          tags HEAD, an up-to-date and clean main,
#                                   with the next version and pushes the tag,
#                                   once the maintainer types that version (or
#                                   sets RELEASE_CONFIRM to it)
#
# The rules are those of RELEASE.md (Versions), read from the subjects only: a
# breaking change (a `!` after the type) raises the major, a feat the minor, a
# fix, perf, build, refactor, docs or revert commit, or GitHub's `Revert "..."`
# subject, the patch, and chore, ci, test and style nothing; a 0.x version
# stays 0.x, so a breaking change raises its minor, and 1.0.0 is a tag a
# maintainer chooses, which check accepts since it is above the minimum. The
# patterns are anchored at the start of the subject: a revert quoting a feat
# is a revert. Bodies are not read, so the changelogs Renovate quotes in its
# commits never count (svu, which reads types anywhere in a subject, was
# dropped for that reason).
#
# The last published release is the latest GitHub release that is neither a
# draft nor a pre-release, read with gh: a tag whose release was refused or
# never ran publishes nothing, so its commits are still pending. Without gh,
# the last release tag stands for it, with a warning. RELEASE_VERSION_PREVIOUS
# overrides both (the tests use it). The first release is v0.1.0. A pre-release
# tag (v0.2.0-rc.1) is checked by its core version.
set -euo pipefail
# A failure inside a command substitution stops the script on bash 4.4 and
# later; every substitution also checks its status, for the bash 3.2 of macOS.
shopt -s inherit_errexit 2>/dev/null || true

# release_pattern matches the release tags, as the tag filter of
# goreleaser-release.yml does; pre-releases are excluded where it is used.
readonly release_pattern='v[0-9]*.[0-9]*.[0-9]*'

# The subjects of each kind of change (RELEASE.md, Versions).
readonly breaking_subjects='^[a-z]+(\([^)]*\))?!: '
readonly minor_subjects='^feat(\([^)]*\))?: '
readonly patch_subjects='^((fix|perf|build|refactor|docs|revert)(\([^)]*\))?: |Revert ")'

# core strips the pre-release and the build metadata of a version.
core() {
	local v=${1%%+*}
	echo "${v%%-*}"
}

# newer reports whether version $1 is above version $2 (cores only).
newer() {
	local a b
	a=$(core "$1") b=$(core "$2")
	[[ "${a}" != "${b}" && "$(printf '%s\n%s\n' "${a#v}" "${b#v}" | sort -V | tail -n 1)" == "${a#v}" ]]
}

# published prints the tag of the last published release, or nothing.
published() {
	local tag
	if [[ -n "${RELEASE_VERSION_PREVIOUS+set}" ]]; then
		echo "${RELEASE_VERSION_PREVIOUS}"
		return
	fi
	if command -v gh >/dev/null 2>&1 &&
		tag=$(gh release list --exclude-drafts --exclude-pre-releases --limit 1 --json tagName --jq '.[0].tagName // ""' 2>/dev/null); then
		echo "${tag}"
		return
	fi
	echo "release-version: gh cannot list the releases; the last release tag stands for the last published release" >&2
	git describe --tags --abbrev=0 --match "${release_pattern}" --exclude '*-*' HEAD 2>/dev/null || true
}

# highest prints the highest release tag, pre-releases and the tag $1 excluded,
# or nothing: a refused tag holds its number for good (version-tags ruleset).
highest() {
	git tag -l "${release_pattern}" | grep -v -e '-' -e "^${1:-}\$" | sort -V | tail -n 1 || true
}

# raise prints the version $1 raised for a change of kind $2 (major, minor or
# patch); a major change of a 0.x version raises its minor.
raise() {
	local major minor patch
	IFS=. read -r major minor patch <<<"$(core "$1")"
	major=${major#v}
	case "$2" in
	major) if [[ ${major} -eq 0 ]]; then echo "v0.$((minor + 1)).0"; else echo "v$((major + 1)).0.0"; fi ;;
	minor) echo "v${major}.$((minor + 1)).0" ;;
	patch) echo "v${major}.${minor}.$((patch + 1))" ;;
	esac
}

# minimum prints the version the commits from the release tag $1 to HEAD call
# for, or $1 itself when they call for none. With no release yet (an empty
# $1), the first version is v0.1.0.
minimum() {
	local subjects
	if [[ -z "$1" ]]; then
		echo v0.1.0
		return
	fi
	if ! git merge-base --is-ancestor "$1" HEAD 2>/dev/null; then
		echo "release-version: the last published release, $1, is not an ancestor of HEAD" >&2
		return 1
	fi
	subjects=$(git log --format=%s "$1..HEAD") || return 1
	if grep -qE "${breaking_subjects}" <<<"${subjects}"; then
		raise "$1" major
	elif grep -qE "${minor_subjects}" <<<"${subjects}"; then
		raise "$1" minor
	elif grep -qE "${patch_subjects}" <<<"${subjects}"; then
		raise "$1" patch
	else
		echo "$1"
	fi
}

# above prints the version $1, or the patch after the release tag $2 when $2
# already holds $1 or more.
above() {
	if [[ -n "$2" ]] && ! newer "$1" "$2"; then
		raise "$2" patch
	else
		echo "$1"
	fi
}

mode=${1:-}
case "${mode}" in
next)
	previous=$(published) || exit 1
	min=$(minimum "${previous}") || exit 1
	if [[ -n "${previous}" && "${min}" == "${previous}" ]]; then
		echo "release-version: only chore, ci, test or style commits since ${previous}: nothing to release" >&2
		exit 1
	fi
	above "${min}" "$(highest "")"
	;;
check)
	tag=${2:?usage: release-version.sh check <tag>}
	if [[ "$(git rev-parse "${tag}^{commit}")" != "$(git rev-parse HEAD)" ]]; then
		echo "release-version: ${tag} does not point at HEAD" >&2
		exit 2
	fi
	previous=$(published) || exit 1
	min=$(minimum "${previous}") || exit 1
	if [[ -n "${previous}" && "${min}" == "${previous}" ]]; then
		echo "release-version: ${tag}, but only chore, ci, test or style commits since ${previous}: nothing to release" >&2
		exit 1
	fi
	high=$(highest "${tag}")
	want=$(above "${min}" "${high}")
	if newer "${want}" "${tag}"; then
		echo "release-version: ${tag} is below ${want}, the lowest version the commits since ${previous:-the first commit} and the existing tags allow" >&2
		exit 1
	fi
	echo "${tag}: at least ${want}, the lowest version the commits since ${previous:-the first commit} and the existing tags allow"
	;;
pending)
	previous=$(published) || exit 1
	min=$(minimum "${previous}") || exit 1
	range=${previous:+${previous}..}HEAD
	echo "Changes on \`main\` that no release has published yet, since ${previous:-the first commit}. Updated by \`release-reminder.yml\` on every push to \`main\` and every release tag."
	echo
	if [[ -n "${previous}" && "${min}" == "${previous}" ]]; then
		echo "**Nothing to release**: only chore, ci, test or style commits since ${previous}."
	else
		echo "**Next version: \`$(above "${min}" "$(highest "")")\`**. To release it, run \`make release-tag\` (\`RELEASE.md\`, *Cutting a release*)."
	fi
	for group in "Features:^feat(\\([^)]*\\))?!?: " "Fixes:^fix(\\([^)]*\\))?!?: " "Other changes:"; do
		title=${group%%:*} pattern=${group#*:}
		if [[ -n "${pattern}" ]]; then
			lines=$(git log --format='%s (%h)' "${range}" | grep -E "${pattern}" || true)
		else
			lines=$(git log --format='%s (%h)' "${range}" | grep -v -E '^(feat|fix)(\([^)]*\))?!?: ' || true)
		fi
		[[ -z "${lines}" ]] && continue
		echo
		echo "### ${title}"
		echo
		sed 's/^/- /' <<<"${lines}"
	done
	;;
tag)
	git fetch -q origin main --tags
	if [[ -n "$(git status --porcelain)" ]]; then
		echo "release-version: the worktree has changes; tag from a clean checkout of main" >&2
		exit 1
	fi
	if [[ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]]; then
		echo "release-version: HEAD is not origin/main; check out an up-to-date main" >&2
		exit 1
	fi
	previous=$(published) || exit 1
	min=$(minimum "${previous}") || exit 1
	if [[ -n "${previous}" && "${min}" == "${previous}" ]]; then
		echo "release-version: only chore, ci, test or style commits since ${previous}: nothing to release" >&2
		exit 1
	fi
	version=$(above "${min}" "$(highest "")")
	echo "Release ${version}, after ${previous:-no release}, on $(git log -1 --format='%h %s')."
	if [[ -n "${RELEASE_CONFIRM:-}" ]]; then
		answer=${RELEASE_CONFIRM}
	else
		read -r -p "Type ${version} to tag and push it: " answer
	fi
	if [[ "${answer}" != "${version}" ]]; then
		echo "release-version: not confirmed (${answer:-nothing} is not ${version}); nothing tagged" >&2
		exit 1
	fi
	git tag -a "${version}" -m "${version}" HEAD
	git push origin "${version}"
	;;
*)
	echo "usage: release-version.sh next | check <tag> | pending | tag" >&2
	exit 2
	;;
esac
