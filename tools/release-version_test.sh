#!/usr/bin/env bash
# Tests of tools/release-version.sh, against throwaway repositories: each case
# commits Conventional Commits, tags some of them, says which tag is the last
# published release, and checks what the script computes; gh is replaced by a
# fake.
set -uo pipefail

script=$(realpath "$(dirname "$0")/release-version.sh")
status=0

# The cases must not depend on the user's or the system's git configuration
# (commit signing above all), nor on a GitHub login.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.com GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.com

# The fake gh prints FAKE_GH_RELEASE as the tag of the last published release,
# or fails when FAKE_GH_FAIL is set.
fakes=$(mktemp -d)
cat >"${fakes}/gh" <<'EOF'
#!/usr/bin/env bash
[[ -n "${FAKE_GH_FAIL:-}" ]] && exit 1
echo "${FAKE_GH_RELEASE:-}"
EOF
chmod +x "${fakes}/gh"
export PATH="${fakes}:${PATH}"

# repo creates an empty repository and moves into it.
repo() {
	cd "$(mktemp -d)" && git init -q -b main
}

# commit records an empty commit with the message $1.
commit() {
	git commit -q --allow-empty -m "$1"
}

# published sets the tag of the last published release ("" for none).
published() {
	export RELEASE_VERSION_PREVIOUS=$1
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

# expect_in <name> <text> <want...> fails unless text holds every want.
expect_in() {
	local name=$1 text=$2 want
	shift 2
	for want in "$@"; do
		if [[ "${text}" != *"${want}"* ]]; then
			echo "FAIL: ${name}: no '${want}' in:"
			echo "${text}"
			status=1
		fi
	done
}

# No release yet: the first version is v0.1.0.
repo
published ""
commit 'chore: start'
commit 'feat(agents): add the agent resource'
expect 'first release' v0.1.0 "${script}" next
git tag v0.1.0
expect 'first tag' 'v0.1.0: at least v0.1.0, the lowest version the commits since the first commit and the existing tags allow' "${script}" check v0.1.0

# From v0.1.0: a fix raises the patch, a feat the minor, and a breaking change
# the minor too while the version is 0.x.
published v0.1.0
commit 'fix(agents): keep the alias on a failed read'
expect 'a fix' v0.1.1 "${script}" next
commit 'feat(libraries): add the library resource'
expect 'a feat' v0.2.0 "${script}" next
commit 'feat(agents)!: drop an attribute'
expect 'a breaking change in 0.x' v0.2.0 "${script}" next

# check: the tag points at HEAD, and is at least the version the commits call
# for; a maintainer may choose more, 1.0.0 above all.
expect 'a tag elsewhere' fail "${script}" check v0.1.0
git tag v0.1.1
expect 'a tag below the commits' fail "${script}" check v0.1.1
git tag v0.3.0
expect 'a higher tag' 'v0.3.0: at least v0.2.0, the lowest version the commits since v0.1.0 and the existing tags allow' "${script}" check v0.3.0
git tag v1.0.0
expect '1.0.0 chosen by hand' 'v1.0.0: at least v0.3.1, the lowest version the commits since v0.1.0 and the existing tags allow' "${script}" check v1.0.0

# A body never counts: Renovate quotes upstream changelogs in its commits.
repo
commit 'feat: start'
git tag v0.1.0
published v0.1.0
git commit -q --allow-empty -m 'chore(deps): update a tool' -m 'feat(cask): an upstream feature
fix!: an upstream breaking fix
BREAKING CHANGE: upstream'
expect 'a body' fail "${script}" next

# After 1.0.0, a breaking change raises the major.
repo
commit 'feat: start'
git tag v1.2.3
published v1.2.3
commit 'feat(agents)!: drop an attribute'
expect 'a breaking change after 1.0.0' v2.0.0 "${script}" next

# A tag whose release was refused publishes nothing: its commits stay
# pending, and the next version goes above it, even after a ci commit only.
repo
commit 'feat: start'
git tag v0.1.0
published v0.1.0
commit 'feat(agents): add the agent resource'
git tag v0.2.0
commit 'ci: change the fingerprint of the signing key'
expect 'after a refused tag' v0.2.1 "${script}" next
expect 'the refused number' fail "${script}" check v0.2.0
git tag v0.2.1
expect 'above a refused tag' 'v0.2.1: at least v0.2.1, the lowest version the commits since v0.1.0 and the existing tags allow' "${script}" check v0.2.1
expect_in 'pending after a refused tag' "$("${script}" pending)" 'since v0.1.0' '**Next version: `v0.2.2`**' '- feat(agents): add the agent resource'

# A pre-release counts by its core version, and is never the previous release.
repo
commit 'feat: start'
git tag v0.1.0
published v0.1.0
commit 'feat(agents): add the agent resource'
git tag v0.2.0-rc.1
expect 'a pre-release' 'v0.2.0-rc.1: at least v0.2.0, the lowest version the commits since v0.1.0 and the existing tags allow' "${script}" check v0.2.0-rc.1
commit 'fix(agents): keep the alias on a failed read'
commit 'feat(libraries): add the library resource'
expect 'a pre-release is not the previous release' v0.2.0 "${script}" next

# Only chore, ci, test or style since the last release: nothing to release.
repo
commit 'feat: start'
git tag v0.1.0
published v0.1.0
commit 'chore(deps): update a tool'
commit 'ci: pin an action'
commit 'test: cover a case'
commit 'style: format'
expect 'nothing to release' fail "${script}" next
git tag v0.1.1
expect 'a tag with nothing to release' fail "${script}" check v0.1.1
expect_in 'pending with nothing to release' "$("${script}" pending)" '**Nothing to release**'

# perf, build, refactor, docs and reverts release a patch (RELEASE.md,
# Versions). A GitHub revert subject quotes the reverted one: a reverted feat
# is a revert, not a feat.
for subject in 'perf: speed up' 'build: bump Go' 'refactor: move code' 'docs(agents): fix a typo' 'revert: undo a fix' 'Revert "feat(agents): add the agent resource"'; do
	repo
	commit 'feat: start'
	git tag v0.1.0
	published v0.1.0
	commit "${subject}"
	expect "${subject}" v0.1.1 "${script}" next
done

# pending lists what no release has published, grouped as the notes are.
repo
commit 'feat: start'
git tag v0.1.0
published v0.1.0
commit 'fix(client): retry on 429'
commit 'ci: pin an action'
pending=$("${script}" pending)
expect_in pending "${pending}" 'since v0.1.0' '**Next version: `v0.1.1`**' '### Fixes' '- fix(client): retry on 429' '### Other changes' '- ci: pin an action'
if [[ "${pending}" == *'### Features'* ]]; then
	echo "FAIL: pending: a Features group with no feat"
	status=1
fi

# The last published release comes from gh; without gh, from the last
# release tag, with a warning.
unset RELEASE_VERSION_PREVIOUS
repo
commit 'feat: start'
git tag v0.1.0
commit 'feat(agents): add the agent resource'
git tag v0.2.0
commit 'fix(agents): keep the alias on a failed read'
expect 'gh says v0.1.0 is the last release' v0.2.1 env FAKE_GH_RELEASE=v0.1.0 "${script}" next
expect_in 'gh is the source of pending' "$(FAKE_GH_RELEASE=v0.1.0 "${script}" pending)" 'since v0.1.0'
expect 'without gh' v0.2.1 env FAKE_GH_FAIL=1 "${script}" next
expect_in 'without gh, a warning' "$(FAKE_GH_FAIL=1 "${script}" next 2>&1 >/dev/null)" 'the last release tag stands for the last published release'
expect_in 'without gh, pending' "$(FAKE_GH_FAIL=1 "${script}" pending 2>/dev/null)" 'since v0.2.0'
expect 'previous from gh' v0.1.0 env FAKE_GH_RELEASE=v0.1.0 "${script}" previous
# check runs on the tagged commit: without gh, the previous release is the
# last release tag before it, never the tag under check.
git tag v0.2.1
expect 'without gh, check' 'v0.2.1: at least v0.2.1, the lowest version the commits since v0.2.0 and the existing tags allow' env FAKE_GH_FAIL=1 "${script}" check v0.2.1
expect 'without gh, previous before a tag' v0.2.0 env FAKE_GH_FAIL=1 "${script}" previous HEAD^

# tag: from an up-to-date, clean main, once the version is confirmed.
published v0.1.0
remote=$(mktemp -d)
git init -q --bare "${remote}"
repo
commit 'feat: start'
git tag v0.1.0
commit 'feat(agents): add the agent resource'
git remote add origin "${remote}"
git push -q origin main v0.1.0
expect 'tag, not confirmed' fail env RELEASE_CONFIRM=v0.3.0 "${script}" tag
if git ls-remote --tags origin | grep -q 'v0.2.0'; then
	echo "FAIL: tag, not confirmed: v0.2.0 pushed"
	status=1
fi
touch dirty
expect 'tag, dirty worktree' fail env RELEASE_CONFIRM=v0.2.0 "${script}" tag
rm dirty
commit 'fix: not pushed'
expect 'tag, HEAD is not origin/main' fail env RELEASE_CONFIRM=v0.2.0 "${script}" tag
git reset -q --hard origin/main
expect_in 'tag, confirmed' "$(RELEASE_CONFIRM=v0.2.0 "${script}" tag 2>&1)" 'Release v0.2.0, after v0.1.0'
if ! git ls-remote --tags origin | grep -q 'refs/tags/v0.2.0'; then
	echo "FAIL: tag, confirmed: v0.2.0 not pushed"
	status=1
fi

[[ ${status} -eq 0 ]] && echo "release-version: all cases pass"
exit "${status}"
