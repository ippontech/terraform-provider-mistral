#!/usr/bin/env bash
# Tests of tools/conventional-commits.sh, run by the conventional-commits
# workflow before it checks anything: each case states a message and whether
# the check must accept it.
set -uo pipefail

script="$(dirname "$0")/conventional-commits.sh"
status=0

# expect <accept|reject> <title> [body]
expect() {
	local want=$1 title=$2 body=${3:-} got
	if TITLE=${title} BODY=${body} "${script}" title >/dev/null 2>&1; then got=accept; else got=reject; fi
	if [[ "${got}" != "${want}" ]]; then
		echo "FAIL: '${title}' / '${body}': got ${got}, want ${want}"
		status=1
	fi
}

expect accept 'feat(agents): add the agent resource'
expect accept 'fix: keep the alias on a failed read'
expect accept 'ci(release): pin GoReleaser'
expect accept 'Revert "feat(agents): add the agent resource"'
expect accept 'feat: drop an attribute' 'Breaking in 0.x: tools is now a list.'
expect reject 'Add the agent resource'
expect reject 'feature: add the agent resource'
expect reject 'feat:add the agent resource'
expect reject 'feat(): add the agent resource'
expect reject 'feat!: drop an attribute'
expect reject 'feat(agents)!: drop an attribute'
expect reject 'feat: drop an attribute' $'Some text.\n\nBREAKING CHANGE: tools is a list.'
expect reject 'fixup! feat: add the agent resource'

# The message mode, as the commit-msg hook calls it, ignores comment lines.
tmp=$(mktemp)
trap 'rm -f "${tmp}"' EXIT
printf '%s\n' 'docs: explain the import ID' '' '# Please enter the commit message' >"${tmp}"
if ! "${script}" message "${tmp}" >/dev/null 2>&1; then
	echo "FAIL: message mode rejected a valid message"
	status=1
fi
printf '%s\n' 'wip' >"${tmp}"
if "${script}" message "${tmp}" >/dev/null 2>&1; then
	echo "FAIL: message mode accepted 'wip'"
	status=1
fi

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
