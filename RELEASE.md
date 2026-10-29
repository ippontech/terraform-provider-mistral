# Releasing

Releases are automatic: every push to `main` runs semantic-release, which reads the Conventional Commits since the last version tag, picks the next version, updates `CHANGELOG.md`, pushes the tag and creates the GitHub release. The tag starts GoReleaser, which builds, signs and attaches the artifacts the Terraform Registry and the OpenTofu Registry ingest.

Nothing is released while the repository is private: both release workflows skip their job, since the registries ingest public repositories only.

## Versions

| Commit type | Release |
|---|---|
| `feat` | minor |
| `fix`, `perf`, `build`, `refactor`, `docs` | patch |
| `chore`, `ci`, `test`, `style` | none |
| `!` or a `BREAKING CHANGE:` footer | major (refused on `main` by the PR title check) |

**The provider stays in 0.x until 1.0.0 is decided.** While in 0.x:

- **Breaking changes.** A breaking change is a `feat`, so it publishes a minor version. Its description starts with `Breaking in 0.x:` and says what users must change. The PR title check refuses `!` and `BREAKING CHANGE:` on `main`, because semantic-release would publish 1.0.0.
- **Names first.** Get the resource, attribute and import ID names right before 1.0.0. After it, a rename costs a major version and a `moved` block for every user.
- **Condition for 1.0.0.** Every published resource and data source has passed an acceptance test against the live API (its registry entry says `acceptanceTested`). This includes the Admin API ones, so it needs Enterprise access from Mistral.

**Starting tag.** semantic-release publishes 1.0.0 as its first version unless a version tag exists. Before the first release, push `v0.0.0` on the initial commit (`git tag v0.0.0 <initial-commit-sha> && git push origin v0.0.0`). The first `feat` then publishes 0.1.0. The release workflow builds nothing for `v0.0.0`.

## Release security

The release jobs hold the signing key and a token that can push tags. They follow the rules for privileged jobs from the `ippontech/terraform-provider-anthropic` release hardening:

1. The secrets live in the protected `release` environment, not in repository secrets:
   - `GPG_PRIVATE_KEY` and `GPG_PASSPHRASE`, for GoReleaser;
   - `SEMREL_APP_ID` and `SEMREL_APP_PRIVATE_KEY`, for semantic-release.
2. A ruleset protects the version tags (`.github/rulesets/version-tags.json`): only the release GitHub App creates them, and nobody updates or deletes them.
3. No cache is restored or saved in a job that signs or releases (`cache: false`). A cache written by a pull request job could otherwise run code there.
4. semantic-release and its plugins are installed from the committed lockfile (`tools/release/package-lock.json`) with `npm ci --ignore-scripts`: no install script runs and no version floats.
5. The App token is created after the install, and every checkout uses `persist-credentials: false`.
6. A human reviews every bump of the release tooling (GoReleaser, semantic-release, the GPG import action). Renovate labels these pull requests `release-tooling` and never merges them automatically.

## Before the first release

These steps need an owner of the GitHub organization and of the registry namespaces.

1. **Move the repository.** Transfer it to the `ippontech` organization and make it public. GitHub redirects the old URLs.
2. **Import the rulesets** in *Settings > Rules > Rulesets > Import a ruleset*:
   - `.github/rulesets/main.json`: linear history, pull request required, the CI checks required, no force push. Its `required_approving_review_count` of 1 needs a second maintainer; set it to 0 until there is one.
   - `.github/rulesets/version-tags.json`: replace `actor_id` 0 with the ID of the release GitHub App.
3. **Create the `release` environment** with its four secrets. Restrict it to the `main` branch and the `v*` tags, and require a reviewer if wanted.
4. **Configure the release GitHub App.** The App used by `ippontech/terraform-provider-anthropic` can be reused if it is installed on the organization. It needs `contents: write`, `issues: write` and `pull-requests: write` on this repository.
5. **Push the starting tag** `v0.0.0`.
6. **Publish on the Terraform Registry.** Sign in to https://registry.terraform.io with GitHub, choose *Publish > Provider*, then the `ippontech` organization and `terraform-provider-mistral`. The `ippontech` namespace already has a signing key (the one used for `ippontech/anthropic`). The Registry requires RSA or DSA keys, and rejects the default ECC type. The Registry installs a webhook and ingests every later release by itself.
7. **Publish on the OpenTofu Registry.** Open a *Submit new Provider* issue at https://github.com/opentofu/registry/issues/new/choose for `ippontech/terraform-provider-mistral`. Do not open a pull request or use the API: the registry only accepts the issue forms.
   - As of 2026-10-29, OpenTofu has no signing key for the `ippontech` namespace (`ippontech/anthropic` is listed without one).
   - A public member of the organization can add one with the *Submit new Provider Signing Key* form. OpenTofu checks the key's expiry at submission; the Terraform Registry does not.

## Checking a release

- **Local snapshot:** `goreleaser release --snapshot --clean --skip=sign,publish` builds the 13 archives, the `SHA256SUMS` file and the registry manifest locally.
- **Pull requests:** `goreleaser check` runs on every pull request (`goreleaser-check.yml`).
