#!/usr/bin/env bash
# Tests of tools/validate-examples.sh, offline: the examples use only
# terraform_data, of Terraform's built-in provider, so terraform validates
# them without init and without the provider of this repository.
set -uo pipefail

if ! command -v terraform >/dev/null; then
	echo "validate-examples_test.sh: terraform not found" >&2
	exit 2
fi

script=$(realpath "$(dirname "$0")/validate-examples.sh")
status=0
work=$(mktemp -d)
trap 'rm -rf "${work}"' EXIT

# example <dir> writes a resource directory with both import examples, whose
# resource reads a file of a subdirectory with file("${path.module}/..."), as
# the asset of a skill would.
example() {
	local dir=$1
	mkdir -p "${dir}/assets"
	echo 'print(1)' >"${dir}/assets/extract.py"
	# Single quotes: ${path.module} is Terraform's, not the shell's.
	printf '%s\n' 'resource "terraform_data" "skill" {' '  input = file("${path.module}/assets/extract.py")' '}' >"${dir}/resource.tf"
	printf '%s\n' 'import {' '  to = terraform_data.skill' '  id = "one"' '}' >"${dir}/import-by-string-id.tf"
	printf '%s\n' 'import {' '  to = terraform_data.skill' '  id = "two"' '}' >"${dir}/import-by-identity.tf"
}

# expect <valid|invalid> <case> <dir>
expect() {
	local want=$1 label=$2 dir=$3 got out
	if out=$("${script}" "${dir}" 2>&1); then got=valid; else got=invalid; fi
	if [[ "${got}" != "${want}" ]]; then
		echo "FAIL: ${label}: got ${got}, want ${want}"
		echo "${out}"
		status=1
	fi
}

# Each import is validated in a copy of the directory without the other, and
# the copy holds the file the resource reads.
example "${work}/reads-a-file"
expect valid 'an example that reads a file next to it' "${work}/reads-a-file"

# validate evaluates file(): a missing file fails, so the case above checks
# the copy.
example "${work}/missing-file"
rm -r "${work}/missing-file/assets"
expect invalid 'an example that reads a missing file' "${work}/missing-file"

# An error in one import example fails, though the other is valid.
example "${work}/bad-import"
printf '%s\n' 'import {' '  to   = terraform_data.skill' '  name = "two"' '}' >"${work}/bad-import/import-by-identity.tf"
expect invalid 'an import example with an unsupported argument' "${work}/bad-import"

if ((status == 0)); then
	echo "validate-examples.sh: all cases pass."
fi
exit "${status}"
