#!/usr/bin/env bash
# Tests of tools/conventional-commits.sh, run by the conventional-commits
# workflow before it checks anything: each case states a message and whether
# the check must accept it.
set -uo pipefail

script=$(realpath "$(dirname "$0")/conventional-commits.sh")
status=0

# The message mode reads core.commentChar, commit.cleanup and GIT_EDITOR: the
# cases must not depend on the user's or the system's git configuration, nor
# on the editor of the environment. Each case sets what it needs.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
unset GIT_EDITOR

# expect <accept|reject> <title>
expect() {
	local want=$1 title=$2 got
	if TITLE=${title} "${script}" title >/dev/null 2>&1; then got=accept; else got=reject; fi
	if [[ "${got}" != "${want}" ]]; then
		echo "FAIL: '${title}': got ${got}, want ${want}"
		status=1
	fi
}

expect accept 'feat(agents): add the agent resource'
expect accept 'fix: keep the alias on a failed read'
expect accept 'ci(release): pin GoReleaser'
expect accept 'Revert "feat(agents): add the agent resource"'
expect accept 'feat!: drop an attribute'
expect accept 'feat(agents)!: drop an attribute'
expect reject 'Add the agent resource'
expect reject 'feature: add the agent resource'
expect reject 'feat:add the agent resource'
expect reject 'feat(): add the agent resource'
expect reject 'feat!:drop an attribute'
expect reject 'fixup! feat: add the agent resource'

# The message mode, as the commit-msg hook calls it, checks the subject git
# will commit: after an editor, it ignores comment lines, those of
# core.commentChar included, blank lines above the subject and what follows
# the scissors line; it keeps the comment lines where commit.cleanup or the
# absence of an editor (GIT_EDITOR=:) keeps them.
tmp=$(mktemp)
# Outside any repository, so that the configuration of the checkout, such as
# its core.commentChar, does not reach the cases.
outside=$(mktemp -d)
trap 'rm -f "${tmp}"; rm -rf "${outside}"' EXIT

# expect_message <accept|reject> <case> <line>... (the lines of the message
# file; GIT_CONFIG_* and GIT_EDITOR in the environment reach the script)
expect_message() {
	local want=$1 label=$2 got
	shift 2
	printf '%s\n' "$@" >"${tmp}"
	if (cd "${outside}" && GIT_CEILING_DIRECTORIES=$(dirname "${outside}") "${script}" message "${tmp}") >/dev/null 2>&1; then
		got=accept
	else
		got=reject
	fi
	if [[ "${got}" != "${want}" ]]; then
		echo "FAIL: message mode, ${label}: got ${got}, want ${want}"
		status=1
	fi
}

expect_message accept 'comment lines below' 'docs: explain the import ID' '' '# Please enter the commit message'
expect_message reject 'wip' 'wip'
expect_message accept 'a blank line above the subject' '' 'docs: explain the import ID' '' '# Please enter the commit message'
expect_message accept 'a template comment and a blank line above' '# type(scope): subject' '' 'docs: explain the import ID'
expect_message reject 'a template comment above an invalid subject' '# type(scope): subject' 'wip'
GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.commentChar GIT_CONFIG_VALUE_0=';' \
	expect_message accept 'core.commentChar ;' '; type(scope): subject' 'docs: explain the import ID' '' '; Please enter the commit message'
GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.commentChar GIT_CONFIG_VALUE_0=';' \
	expect_message reject 'core.commentChar ;, a # line is the subject' '#42 explain the import ID' 'docs: explain the import ID'

# commit.cleanup, and an editor or not.
readonly cut='# ------------------------ >8 ------------------------'
expect_message reject 'below the scissors line' '' "${cut}" '# Do not modify or remove the line above.' 'feat: below the cut'
GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.commentChar GIT_CONFIG_VALUE_0=';' \
	expect_message reject 'below the scissors line of core.commentChar ;' '' "${cut/#\#/;}" 'feat: below the cut'
GIT_EDITOR=: expect_message reject 'no editor: a # line is the subject' '#42 explain the import ID' '' 'docs: explain the import ID'
GIT_EDITOR=: expect_message accept 'no editor: a valid subject' 'docs: explain the import ID' '' 'The body.'
for mode in whitespace verbatim scissors; do
	GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=commit.cleanup GIT_CONFIG_VALUE_0=${mode} \
		expect_message reject "commit.cleanup=${mode}: a # line is the subject" '#42 explain the import ID' 'docs: explain the import ID'
	GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=commit.cleanup GIT_CONFIG_VALUE_0=${mode} \
		expect_message accept "commit.cleanup=${mode}: a blank line above the subject" '' 'docs: explain the import ID' '' "${cut}" '# Changes to be committed:'
done
GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=commit.cleanup GIT_CONFIG_VALUE_0=strip \
	expect_message accept 'commit.cleanup=strip: a comment above the subject' '# type(scope): subject' 'docs: explain the import ID'

# The range mode, on a scratch repository: a merge commit and a bad subject
# fail, a linear range of valid commits passes.
repo=$(mktemp -d)
trap 'rm -f "${tmp}"; rm -rf "${repo}"' EXIT
script=$(realpath "${script}")
(
	cd "${repo}" || exit 1
	git init -q -b main
	git -c user.name=t -c user.email=t@t commit -q --allow-empty -m 'chore: start'
	git -c user.name=t -c user.email=t@t commit -q --allow-empty -m 'feat: one'
	"${script}" range HEAD~1..HEAD >/dev/null 2>&1 || { echo "FAIL: range rejected a valid commit"; exit 1; }
	git -c user.name=t -c user.email=t@t commit -q --allow-empty -m 'bad subject'
	"${script}" range HEAD~2..HEAD >/dev/null 2>&1 && { echo "FAIL: range accepted 'bad subject'"; exit 1; }
	git checkout -q -b side HEAD~1
	git -c user.name=t -c user.email=t@t commit -q --allow-empty -m 'fix: side'
	git checkout -q main
	git -c user.name=t -c user.email=t@t merge -q --no-ff --no-edit -m 'chore: merge side' side
	"${script}" range HEAD~1..HEAD >/dev/null 2>&1 && { echo "FAIL: range accepted a merge commit"; exit 1; }
	exit 0
) || status=1

if ((status == 0)); then
	echo "conventional-commits.sh: all cases pass."
fi
exit "${status}"
