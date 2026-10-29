#!/usr/bin/env bash
# Checks messages against Conventional Commits, the format the release notes
# group commits by (.goreleaser.yml, changelog) and the maintainer reads to
# choose the next version (RELEASE.md).
#
#   tools/conventional-commits.sh title             # TITLE: a PR title
#   tools/conventional-commits.sh message <file>    # a commit message file (the commit-msg hook)
#   tools/conventional-commits.sh range <base>..<head>   # every commit of a pull request
#
# A squash merge turns the PR title and description into the commit on main;
# a rebase merge puts every commit of the branch there. Both are checked, with
# the same rules:
#
# - the subject is "<type>[(scope)][!]: <subject>", or a "Revert "..."" subject;
# - in a range, no merge commit: main has a linear history, and a branch
#   catches up with a rebase.
set -euo pipefail

# The types of CONTRIBUTING.md. `feature`, an alias of some presets, is left
# out: no group of the release notes matches it.
readonly types='feat|fix|perf|revert|docs|style|chore|refactor|test|build|ci'
readonly subject_pattern="^(${types})(\([^()]+\))?(!)?: [^[:space:]]"

failures=0

fail() {
	echo "::error title=Conventional Commits::$1"
	failures=$((failures + 1))
}

# check <label> <subject>
check() {
	local label=$1 subject=$2
	if [[ "${subject}" =~ ^Revert\ \".+\"$ ]]; then
		return
	fi
	if [[ ! "${subject}" =~ ${subject_pattern} ]]; then
		fail "${label}: '${subject}' is not '<type>[(scope)][!]: <subject>' with type one of ${types//|/, } (CONTRIBUTING.md)."
	fi
}

case "${1:-}" in
title)
	check "PR title" "${TITLE:?TITLE is not set}"
	;;
message)
	file=${2:?usage: $0 message <file>}
	# git strips the comment lines of an editor message after the hook runs.
	message=$(grep -v '^#' "${file}" || true)
	check "Commit message" "$(head -n 1 <<<"${message}")"
	;;
range)
	range=${2:?usage: $0 range <base>..<head>}
	while read -r sha parents; do
		if [[ "${parents}" == *" "* ]]; then
			fail "Commit ${sha:0:12} is a merge commit: rebase the branch instead of merging into it."
			continue
		fi
		check "Commit ${sha:0:12}" "$(git log -1 --format=%s "${sha}")"
	done < <(git rev-list --reverse --format='%H %P' --no-commit-header "${range}")
	;;
*)
	echo "usage: $0 title | message <file> | range <base>..<head>" >&2
	exit 2
	;;
esac

if ((failures > 0)); then
	exit 1
fi
echo "Conventional Commits: OK."
