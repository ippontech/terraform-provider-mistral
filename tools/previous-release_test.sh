#!/usr/bin/env bash
# Tests of tools/previous-release.sh, against throwaway repositories: each case
# tags some commits, says which tags have a release with signed checksums, and
# checks which one the script prints; gh is replaced by a fake.
set -uo pipefail

script=$(realpath "$(dirname "$0")/previous-release.sh")
status=0

# The cases must not depend on the user's or the system's git configuration
# (commit signing above all), nor on a GitHub login.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.com GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.com

# The fake gh answers `gh api repos/{owner}/{repo}/releases/tags/<tag> --jq ...`
# from two lists of tags: FAKE_GH_SIGNED, whose releases carry the signed
# checksums, and FAKE_GH_UNSIGNED, whose releases carry archives only; any
# other tag has no release (a failure, as a 404 is). FAKE_GH_FAIL fails every
# call, as an API outage does.
fakes=$(mktemp -d)
cat >"${fakes}/gh" <<'EOF_GH'
#!/usr/bin/env bash
[[ -n "${FAKE_GH_FAIL:-}" ]] && exit 1
tag=${2##*/}
for signed in ${FAKE_GH_SIGNED:-}; do
	if [[ "${signed}" == "${tag}" ]]; then
		printf '%s\n' "terraform-provider-mistral_${tag#v}_linux_amd64.zip" "terraform-provider-mistral_${tag#v}_SHA256SUMS" "terraform-provider-mistral_${tag#v}_SHA256SUMS.sig"
		exit 0
	fi
done
for unsigned in ${FAKE_GH_UNSIGNED:-}; do
	if [[ "${unsigned}" == "${tag}" ]]; then
		printf '%s\n' "terraform-provider-mistral_${tag#v}_linux_amd64.zip"
		exit 0
	fi
done
exit 1
EOF_GH
chmod +x "${fakes}/gh"
export PATH="${fakes}:${PATH}"

# repo creates a repository with one commit per argument and moves into it; an
# argument `tag=<name>` tags the commit before it instead.
repo() {
	cd "$(mktemp -d)" && git init -q -b main
	local arg
	for arg in "$@"; do
		if [[ "${arg}" == tag=* ]]; then
			git tag "${arg#tag=}"
		else
			git commit -q --allow-empty -m "${arg}"
		fi
	done
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

# The newest tag whose release carries the signed checksums, a tag without
# binaries (its signing was refused) and a pre-release skipped on the way.
repo 'feat: start' tag=v0.1.0 'feat: a' tag=v0.2.0 'fix: b' tag=v0.2.1 'feat: c' tag=v0.3.0-rc.1 'fix: d'
expect 'the newest signed release' v0.2.1 env FAKE_GH_SIGNED='v0.1.0 v0.2.1' "${script}"
expect 'a refused release is skipped' v0.2.0 env FAKE_GH_SIGNED='v0.1.0 v0.2.0' FAKE_GH_UNSIGNED=v0.2.1 "${script}"
expect 'a deleted release is skipped' v0.1.0 env FAKE_GH_SIGNED=v0.1.0 "${script}"
expect 'no signed release' '' env FAKE_GH_UNSIGNED='v0.1.0 v0.2.0 v0.2.1' "${script}"
expect 'an API outage counts as unsigned' '' env FAKE_GH_FAIL=1 "${script}"

# From the parent of a tagged commit, as the release job calls it: the tag
# under release is never its own baseline.
repo 'feat: start' tag=v0.1.0 'feat: a' tag=v0.2.0
expect 'the tag under release' v0.1.0 env FAKE_GH_SIGNED='v0.1.0 v0.2.0' "${script}" HEAD^
expect 'only the tags reachable from the commit' v0.1.0 env FAKE_GH_SIGNED='v0.1.0 v0.2.0' "${script}" v0.1.0

# No release tag at all: nothing, and no failure (the first release).
repo 'feat: start'
expect 'no tag' '' env FAKE_GH_SIGNED= "${script}"

# Without gh, the newest release tag, with a warning. The script runs with a
# PATH that holds the commands it needs and no gh, real or fake.
nogh=$(mktemp -d)
for command in bash git grep sort head; do
	ln -s "$(command -v "${command}")" "${nogh}/${command}"
done
repo 'feat: start' tag=v0.1.0 'feat: a' tag=v0.2.0 'fix: b'
expect 'without gh' v0.2.0 env PATH="${nogh}" "${script}"
warning=$(PATH="${nogh}" "${script}" 2>&1 >/dev/null)
if [[ "${warning}" != *'gh is not installed'* ]]; then
	echo "FAIL: without gh, a warning: got '${warning}'"
	status=1
fi

[[ ${status} -eq 0 ]] && echo "previous-release: all cases pass"
exit "${status}"
