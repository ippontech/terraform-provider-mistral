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

# read_subject <file> sets subject to the subject git commits from the
# message file the commit-msg hook gets. git runs the hook before it cleans
# the message up as commit.cleanup says (git help commit, --cleanup), so this
# does the same cleanup with git stripspace:
#
# - strip removes the comment lines, those of core.commentChar; whitespace,
#   verbatim and scissors keep them, and the subject skips the blank lines
#   above it in every mode;
# - default is strip when an editor opened, whitespace otherwise: git runs the
#   hook with GIT_EDITOR=: when none opens (-m, -F);
# - after an editor, git cuts the message at its scissors line (with -v, or
#   with commit.cleanup=scissors).
#
# The --cleanup option of git commit does not reach the hook: this follows
# commit.cleanup only. And with core.commentChar=auto, git comments with the
# first of #;@!$%^&|: that starts no line of the message before the editor
# opens (a template, an amended message), which the hook cannot see, while
# git stripspace takes #: after a template whose lines start with #, git
# keeps them, and the subject checked here can differ from the one
# committed. The commit-messages job of CI checks the subjects committed.
read_subject() {
	local file=$1 mode editor=true comment text
	mode=$(git config commit.cleanup || echo default)
	if [[ "${GIT_EDITOR:-}" == ":" ]]; then
		editor=false
	fi
	if [[ "${mode}" == default ]]; then
		mode=whitespace
		if ${editor}; then mode=strip; fi
	fi
	text=$(cat "${file}")
	if ${editor}; then
		comment=$(printf 'x\n' | git stripspace --comment-lines)
		text=$(scissors="${comment%x}------------------------ >8 ------------------------" \
			awk '$0 == ENVIRON["scissors"] { exit } { print }' <<<"${text}")
	fi
	if [[ "${mode}" == strip ]]; then
		text=$(git stripspace --strip-comments <<<"${text}")
	else
		text=$(git stripspace <<<"${text}")
	fi
	subject=${text%%$'\n'*}
}

case "${1:-}" in
title)
	check "PR title" "${TITLE:?TITLE is not set}"
	;;
message)
	read_subject "${2:?usage: $0 message <file>}"
	check "Commit message" "${subject}"
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
