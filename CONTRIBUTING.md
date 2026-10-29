# Contributing

Thanks for helping. This guide covers the local setup, the checks every pull request must pass, and how pull requests are organised. `CLAUDE.md` holds the coding conventions, for people and for coding agents alike.

## Local setup

- **Tools**: the versions are pinned in `mise.toml` (Go, golangci-lint, GoReleaser, Terraform, gitleaks, poutine). With [mise](https://mise.jdx.dev), `mise install` gets them all.
- **Hooks**: `pre-commit install` installs the pre-commit and commit-msg hooks: formatting, lint, tidiness, secret scanning, workflow linters and the Conventional Commits check.
- **Build and install**: `make install` puts the binary in `$(go env GOPATH)/bin`.
- **Local binary in Terraform**: `make .dev.tfrc` writes a CLI configuration with a `dev_overrides` block for `registry.terraform.io/ippontech/mistral`. Point Terraform at it with `TF_CLI_CONFIG_FILE=$PWD/.dev.tfrc`.

## Credentials

```sh
export MISTRAL_API_KEY=...        # standard key: the workspace resources and data sources
export MISTRAL_ADMIN_API_KEY=...  # Admin API key: workspaces, API keys, invites, user groups (Enterprise plans)
```

Unit tests need neither. Never commit a key, and never paste one into an issue, a pull request or a test.

## Checks

| Command | What it checks |
|---|---|
| `make lint` | golangci-lint, 0 issues, in the root and `tools/` modules |
| `make complexity` | no production function above a cognitive complexity of 20 |
| `make test` | unit tests against the fake API, with `-race -shuffle=on` |
| `make cover` | the coverage gate: 75 % of changed statements, 80 % in total and per package |
| `make generate` | the docs are up to date (CI fails on a diff) |
| `make terraform-fmt-check terraform-validate terraform-test-mock` | the examples and the offline native tests |
| `make testacc` | acceptance tests against the live API: they create real objects |

CI runs all of them except `make testacc`, plus gitleaks, govulncheck, actionlint, poutine, semgrep, tflint, checkov and `goreleaser check`.

## Pull requests

- **One issue per pull request**, and one pull request per coherent change: a feature with its tests, docs and examples, or a fix with the test that fails without it.
- **Titles and commits** follow [Conventional Commits](https://www.conventionalcommits.org): `<type>[(scope)]: <subject>`, with type `feat`, `fix`, `perf`, `revert`, `docs`, `style`, `chore`, `refactor`, `test`, `build` or `ci`. semantic-release computes the version from them (RELEASE.md). A breaking change in 0.x is a `feat` whose description starts with `Breaking in 0.x:`. The `conventional-commits.yml` workflow checks the title, the description and every commit of the pull request, since a rebase merge puts each commit on `main`; it refuses merge commits.
- **Code** follows *Code and comments* in `CLAUDE.md`: names that say what a value is, a doc comment on every type and function, a comment next to every choice the code cannot explain by itself.
- **Merges** are squash or rebase only: the history of `main` is linear.
- **Stacking.** A pull request that depends on another targets that pull request's branch, so its diff shows only its own changes. Once the base is merged, rebase it onto `main`.
- **Description.** Say what changed, why, how it was tested, and what was not verified against the live API.

## Checklist for a new resource or data source

See *Adding a resource or data source* in `CLAUDE.md`. In short: the type and its registration, an example, a template with its subcategory, fake API tests to the standard of `CLAUDE.md`, an acceptance test, and the service's `CLAUDE.md` updated.

## Dependency updates

Renovate opens the dependency pull requests, after a 3-day delay and without merging them. Go toolchain and indirect module updates are typed `fix(deps)` so that they trigger a release. A human reviews every bump of the release tooling.

## Claude Code

The repository is developed with Claude Code. `CLAUDE.md` is loaded in every session, and `internal/services/<area>/CLAUDE.md` when working in that area. Keep them accurate: an agent follows them as written.
