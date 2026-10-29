default: fmt tidy-check test install generate

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

generate:
	cd tools; go generate -tags generate ./...

# COVERPROFILE=<file> also writes a coverage profile.
test:
	go test -v -race -shuffle=on -cover$(if $(COVERPROFILE), -coverprofile=$(COVERPROFILE)) -timeout=300s -parallel=10 ./...

testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

.PHONY: build install fmt tidy-check generate test testacc
