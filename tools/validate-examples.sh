#!/usr/bin/env bash
# Validates each example directory given as argument against the provider
# just built (make terraform-validate sets TF_CLI_CONFIG_FILE). A resource
# directory may hold two import examples for one address,
# import-by-string-id.tf and import-by-identity.tf, which Terraform refuses
# together: each is validated in a copy of the directory without the other.
# The copy holds every file and subdirectory, not only the .tf files: validate
# evaluates file("${path.module}/...") and fails on a file that is missing.
set -uo pipefail

failed=0

validate() {
	local dir=$1 label=$2 out
	if out=$(terraform -chdir="$dir" validate -no-color 2>&1); then
		echo "valid:   $label"
	else
		echo "INVALID: $label"
		echo "$out"
		failed=1
	fi
}

for dir in "$@"; do
	if [[ -f $dir/import-by-string-id.tf && -f $dir/import-by-identity.tf ]]; then
		for skip in import-by-identity.tf import-by-string-id.tf; do
			tmp=$(mktemp -d)
			find "$dir" -mindepth 1 -maxdepth 1 ! -name "$skip" -exec cp -R {} "$tmp" \;
			validate "$tmp" "$dir (without $skip)"
			rm -rf "$tmp"
		done
	else
		validate "$dir" "$dir"
	fi
done

exit "$failed"
