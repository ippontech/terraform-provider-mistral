default: fmt tidy-check lint complexity test install generate

build:
	go build -v ./...

install: build
	go install -v ./...

fmt:
	gofmt -s -w -e .

# Reports what `go mod tidy` would change and exits non-zero, without touching
# go.mod or go.sum.
tidy-check:
	@go mod tidy -diff || \
		(echo; echo "go.mod/go.sum are not tidy. Run 'go mod tidy' and commit the result."; exit 1)
	@cd tools && go mod tidy -diff || \
		(echo; echo "tools/go.mod/go.sum are not tidy. Run 'go mod tidy' in tools/ and commit the result."; exit 1)

# tools/ is a Go module of its own, which a run from the root does not see:
# it is linted second, with the same configuration.
lint:
	golangci-lint run
	cd tools && golangci-lint run --config ../.golangci.yml

# The complexity ratchet of the CI complexity job (tools/complexity): every
# function in the non-test Go files under internal/ and tools/ (testdata
# aside) scores 20 or less, unless it already scored more at the merge base
# with COMPLEXITY_BASE and its score did not rise. Reads the working tree,
# committed or not.
COMPLEXITY_BASE ?= origin/main
complexity:
	@go run -C tools ./complexity -base $(COMPLEXITY_BASE)

generate:
	cd tools; go generate -tags generate ./...

# COVERPROFILE=<file> also writes a coverage profile (the CI test job does).
test:
	go test -v -race -shuffle=on -cover$(if $(COVERPROFILE), -coverprofile=$(COVERPROFILE)) -timeout=300s -parallel=10 ./...

# Unit tests, then the coverage gate of the CI coverage job (.coverage.json,
# tools/coversummary): the diff rule against COVER_BASE (committed changes
# only), the total and the per-package minimum, followed by the coverage of
# each resource and data source file. Fails like the CI job does.
COVER_BASE ?= origin/main
cover: COVERPROFILE := coverage.out
cover: test
	git diff --unified=0 --no-color $(COVER_BASE)...HEAD > coverage-diff.out
	go run -C tools ./coversummary -config ../.coverage.json -diff ../coverage-diff.out ../$(COVERPROFILE)

testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

.dev.tfrc:
	@GOBIN=$$(go env GOBIN); \
	printf 'provider_installation {\n  dev_overrides {\n    "registry.terraform.io/ippontech/mistral" = "%s"\n  }\n  direct {}\n}\n' \
		"$${GOBIN:-$$(go env GOPATH)/bin}" > $@

# Offline Terraform checks, run on every pull request (terraform.yml): no
# credentials, no API call, no registry download (the provider comes from
# dev_overrides, so neither validate nor init installs it).
EXAMPLE_DIRS := $(sort $(dir $(wildcard examples/*/*.tf examples/*/*/*.tf)))
# A test file with a mock_provider block runs on every pull request, so it must
# not need credentials. Live test files (no mock_provider) run in testacc.yml.
MOCK_TESTS := $(notdir $(shell grep -l '^mock_provider' tests/*.tftest.hcl 2>/dev/null))
LIVE_TESTS := $(filter-out $(MOCK_TESTS),$(notdir $(wildcard tests/*.tftest.hcl)))

terraform-fmt-check:
	terraform fmt -check -diff -recursive

# Each example directory against the provider just built: catches an attribute
# renamed or removed in the schema that an example (and so the docs) still uses.
terraform-validate: install .dev.tfrc
	TF_CLI_CONFIG_FILE=$(CURDIR)/.dev.tfrc tools/validate-examples.sh $(EXAMPLE_DIRS)

# tests/ is the root module of the native tests: versions.tf maps the provider
# source the test files resolve against.
terraform-test-mock: install .dev.tfrc
	@if [ -z "$(MOCK_TESTS)" ]; then echo "no mock_provider test file in tests/"; exit 0; fi; \
	TF_CLI_CONFIG_FILE=$(CURDIR)/.dev.tfrc terraform -chdir=tests init -backend=false && \
	TF_CLI_CONFIG_FILE=$(CURDIR)/.dev.tfrc terraform -chdir=tests test $(addprefix -filter=,$(MOCK_TESTS))

terraform-test: install .dev.tfrc
	@if [ -z "$(LIVE_TESTS)" ]; then echo "no live test file in tests/"; exit 0; fi; \
	TF_CLI_CONFIG_FILE=$(CURDIR)/.dev.tfrc terraform -chdir=tests init -backend=false && \
	TF_CLI_CONFIG_FILE=$(CURDIR)/.dev.tfrc terraform -chdir=tests test $(addprefix -filter=,$(LIVE_TESTS))

# Compares the operations the provider calls with the fingerprints of
# tools/specdrift/fingerprints.txt, against the live specification (never
# committed: it has no licence). SPECDRIFT_FLAGS=-update rewrites them.
SPEC_URL ?= https://docs.mistral.ai/openapi.yaml
spec-drift:
	@spec=$$(mktemp); trap 'rm -f "$$spec"' EXIT; \
	curl -sSf --retry 3 -o "$$spec" $(SPEC_URL) && \
	go run -C tools ./specdrift -spec "$$spec" -root .. $(SPECDRIFT_FLAGS)

.PHONY: spec-drift build install fmt tidy-check lint complexity generate test cover testacc terraform-fmt-check terraform-validate terraform-test-mock terraform-test
