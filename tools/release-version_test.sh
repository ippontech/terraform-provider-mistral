#!/usr/bin/env bash
# Tests of tools/release-version.sh, against throwaway repositories: each
# case commits Conventional Commits, tags some of them, and checks what the
# script computes. svu must be on the PATH, as for the script.
set -uo pipefail

script=$(realpath "$(dirname "$0")/release-version.sh")
status=0

# The cases must not depend on the user's or the system's git configuration
# (commit signing above all).
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.com GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.com

# repo creates an empty repository and moves into it.
repo() {
	cd "$(mktemp -d)" && git init -q -b main
}

# commit records an empty commit with the message $1.
commit() {
	git commit -q --allow-empty -m "$1"
}

# expect <name> <want> <command...> runs the command and compares its standard
# output with want; a want of "fail" expects a non-zero status instead.
expect() {
	local name=$1 want=$2 got rc
	shift 2
	got=$("$@" 2>/dev/null)
	rc=$?
	if [[ "${want}" == fail ]]; then
		if [[ ${rc} -eq 0 ]]; then
			echo "FAIL: ${name}: succeeded with '${got}', want a failure"
			status=1
		fi
	elif [[ ${rc} -ne 0 || "${got}" != "${want}" ]]; then
		echo "FAIL: ${name}: got '${got}' (status ${rc}), want '${want}'"
		status=1
	fi
}

# No release yet: the first feat makes v0.1.0.
repo
commit 'chore: start'
commit 'feat(agents): add the agent resource'
expect 'first release' v0.1.0 "${script}" next
git tag v0.1.0
expect 'first tag' 'v0.1.0 is the version the commits since the first commit call for' "${script}" check v0.1.0

# From v0.1.0: a fix raises the patch, a feat the minor, and a breaking change
# the minor too while the version is 0.x.
commit 'fix(agents): keep the alias on a failed read'
expect 'a fix' v0.1.1 "${script}" next
commit 'feat(libraries): add the library resource'
expect 'a feat' v0.2.0 "${script}" next
commit 'feat(agents)!: drop an attribute'
expect 'a breaking change in 0.x' v0.2.0 "${script}" next

# check: the tag must point at HEAD and be the computed version.
expect 'a tag elsewhere' fail "${script}" check v0.1.0
git tag v0.3.0
expect 'a wrong tag' fail "${script}" check v0.3.0

# A pre-release counts by its core version, and is never the previous release.
repo
commit 'feat: start'
git tag v0.1.0
commit 'feat(agents): add the agent resource'
git tag v0.2.0-rc.1
expect 'a pre-release' 'v0.2.0-rc.1 is the version the commits since v0.1.0 call for' "${script}" check v0.2.0-rc.1
commit 'fix(agents): keep the alias on a failed read'
commit 'feat(libraries): add the library resource'
expect 'a pre-release is not the previous release' v0.2.0 "${script}" next

# Only chore, ci, test or style since the last release: nothing to release,
# and no tag passes.
repo
commit 'feat: start'
git tag v0.1.0
commit 'chore(deps): update a tool'
commit 'ci: pin an action'
expect 'nothing to release' v0.1.0 "${script}" next
git tag v0.1.1
expect 'a tag with nothing to release' fail "${script}" check v0.1.1

# perf, build, refactor and docs release a patch (RELEASE.md, Versions),
# which svu alone would not.
for type in perf build refactor 'docs(agents)'; do
	repo
	commit 'feat: start'
	git tag v0.1.0
	commit "${type}: change something"
	expect "a ${type} commit" v0.1.1 "${script}" next
done

# pending lists what no release has published, grouped by type.
repo
commit 'feat: start'
git tag v0.1.0
commit 'fix(client): retry on 429'
commit 'ci: pin an action'
pending=$("${script}" pending)
for want in 'since v0.1.0' '**Next version: `v0.1.1`**' '### Fixes' '- fix(client): retry on 429' '### Other changes' '- ci: pin an action'; do
	if [[ "${pending}" != *"${want}"* ]]; then
		echo "FAIL: pending: no '${want}' in:"
		echo "${pending}"
		status=1
	fi
done
if [[ "${pending}" == *'### Features'* ]]; then
	echo "FAIL: pending: a Features group with no feat"
	status=1
fi

[[ ${status} -eq 0 ]] && echo "release-version: all cases pass"
exit "${status}"
