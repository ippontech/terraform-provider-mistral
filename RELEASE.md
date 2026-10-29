# Releasing

A maintainer releases by pushing a version tag on a commit of `main`. The tag starts `goreleaser-release.yml`, which checks that the commit is on `main`, then builds, signs and publishes the GitHub release with its notes. The Terraform Registry and the OpenTofu Registry ingest that release.

Nothing is released while the repository is private: the release job skips, since the registries ingest public repositories only.

## Cutting a release

1. Read the commits since the last tag (`git log --oneline <last-tag>..origin/main`) and choose the version from their types (*Versions* below).
2. Tag the commit of `main` and push the tag: `git tag -a v0.4.0 -m v0.4.0 <sha> && git push origin v0.4.0`. The tag ruleset lets repository admins only create a version tag, and nobody move or delete one.
3. Approve the release job (*Approving a release* below), then check the GitHub release: its archives, `SHA256SUMS`, the signature and the notes.

The release notes group the commits by type (`.goreleaser.yml`, `changelog`): breaking changes (`!` after the type), features, fixes, performance, then the rest; `chore`, `ci`, `test` and `style` are left out. Every pull request checks the format (`conventional-commits.yml`), so a commit cannot land outside these groups.

## Approving a release

The release job waits in `Actions > GoReleaser Release`, on the run of the new tag, behind **Review deployments**, for a reviewer of the `release` environment. The approval releases the GPG key, so it is the last human check before the modules of the release ship signed to every user. Before approving:

1. Open the run's summary. The `dependency-changes` job lists the `go.mod` lines that changed since the previous release tag, or every module for the first release. `go.mod` lists every module compiled into the provider, indirect ones included. "None" means the release compiles the same modules as the previous one.
2. For each changed module, check that the new version is a published release of that module (a tag or release notes on its repository), and look for anything unexpected: a module nobody added on purpose, a version published days ago without notes, a repository that changed owner.
3. Approve. If in doubt, reject: nothing is built, signed or published, and no GitHub release is created. The tag stays, since the tag ruleset forbids deleting it, and the next release takes the next version number. GoReleaser starts the notes of that next release at the rejected tag: add the commits of the rejected one to them by hand, and say why the number was skipped. Never re-sign or replace a published version: release a new one.

## Versions

| Commits since the last tag | Next version in 0.x | After 1.0.0 |
|---|---|---|
| a breaking change (`feat!:`, `fix!:`) | minor | major |
| a `feat` | minor | minor |
| only `fix`, `perf`, `build`, `refactor`, `docs` | patch | patch |
| only `chore`, `ci`, `test`, `style` | no release | no release |

**The provider stays in 0.x until 1.0.0 is decided.** While in 0.x:

- **Breaking changes** publish a minor version. The commit marks them with `!`, which puts them under *Breaking changes* in the notes, and its body says what users must change; the notes show the subject only, so the subject names what breaks.
- **Names first.** Get the resource, attribute and import ID names right before 1.0.0. After it, a rename costs a major version and a `moved` block for every user.
- **Condition for 1.0.0.** Every published resource and data source has passed an acceptance test against the live API (its registry entry says `acceptanceTested`). This includes the Admin API ones, so it needs Enterprise access from Mistral.

The first release is `v0.1.0`.

## Release security

The release job holds the signing key. It follows the rules for privileged jobs of the `ippontech/terraform-provider-anthropic` release hardening, without its release automation, which needed a GitHub App able to push tags:

1. The secrets live in the protected `release` environment, not in repository secrets: `GPG_PRIVATE_KEY` and `GPG_PASSPHRASE`, for GoReleaser.
2. A ruleset protects the version tags (`.github/rulesets/version-tags.json`): only repository admins create them, and nobody updates or deletes them.
3. The release job refuses a tag whose commit is not on `main`.
4. No cache is restored or saved in the job that signs (`cache: false`). A tag run can restore the caches of `main`, so a cache written by any job on `main` could otherwise run code there. A pull request's cache is not the risk: only that pull request can restore it.
5. Every checkout uses `persist-credentials: false`, and the modules are verified against `go.sum` before the build.
6. A human reviews every bump of the release tooling (GoReleaser, the GPG import action). Renovate labels these pull requests `release-tooling` and never merges them automatically.

## Before the first release

These steps need an owner of the GitHub organization and of the registry namespaces.

1. **Move the repository.** Transfer it to the `ippontech` organization and make it public. GitHub redirects the old URLs. Done on 2026-10-29.
2. **Apply the rulesets**, done on 2026-10-29 through the API (`POST /repos/ippontech/terraform-provider-mistral/rulesets` with each file), or in *Settings > Rules > Rulesets > Import a ruleset*:
   - `.github/rulesets/main.json`: linear history, pull request required, the CI checks required, no force push. It requires no approval: GitHub does not let the author of a pull request approve it, and with one maintainer the review is the merge itself. Raise `required_approving_review_count` to 1, with `require_last_push_approval`, once a second maintainer reviews.
   - `.github/rulesets/version-tags.json`: only the *Repository admin* role (`RepositoryRole` 5 in the bypass list) creates a version tag, and nobody updates or deletes one.
3. **Create the `release` environment** with its two secrets. Restrict it to the `v*` tags and require a reviewer, who checks the module changes before each release (*Approving a release*). The environment exists since 2026-10-29, restricted to `v*` with Hector Basset as reviewer; its two secrets are still to add.
4. **Prepare the acceptance tests.** Create the `acctest` environment, restricted to `main`, with the secrets `MISTRAL_API_KEY` (a standard key of a workspace of a dedicated test organization) and, once Mistral grants Admin API access, `MISTRAL_ADMIN_API_KEY`, and the variables of the objects the tests cannot create: `MISTRAL_ACC_WORKSPACE_ID` (the workspace of `MISTRAL_API_KEY`), `MISTRAL_ACC_ADMIN_USER_ID` (an organization admin) and `MISTRAL_ACC_MEMBER_USER_ID` (another member of that workspace). A test whose variable is missing skips. Then set the repository variable `MISTRAL_ACCTEST` to `true`: until then `testacc.yml` skips its job.
5. **Create the `spec-drift` label**, which `spec-drift.yml` puts on the issue it opens.
6. **Publish on the Terraform Registry.** Sign in to https://registry.terraform.io with GitHub, choose *Publish > Provider*, then the `ippontech` organization and `terraform-provider-mistral`. The `ippontech` namespace already has a signing key (the one used for `ippontech/anthropic`). The Registry requires RSA or DSA keys, and rejects the default ECC type. The Registry installs a webhook and ingests every later release by itself.
7. **Publish on the OpenTofu Registry.** Open a *Submit new Provider* issue at https://github.com/opentofu/registry/issues/new/choose for `ippontech/terraform-provider-mistral`. Do not open a pull request or use the API: the registry only accepts the issue forms.
   - **Signing key.** As of 2026-10-29, OpenTofu has no signing key for the `ippontech` namespace, and installs the provider without checking its signature. The `RELEASE.md` of `ippontech/terraform-provider-anthropic` (*Register the public key in the OpenTofu Registry*, ippontech/terraform-provider-anthropic#491) submits the namespace key with no provider name, which covers every provider of `ippontech`, this one included: nothing to submit here once it is done. OpenTofu checks the key's expiry at submission; the Terraform Registry does not.

## Checking a release

- **Local snapshot:** `goreleaser release --snapshot --clean --skip=sign,publish` builds the 13 archives, the `SHA256SUMS` file and the registry manifest locally.
- **Pull requests:** `goreleaser check` and a snapshot build run on every pull request (`goreleaser-check.yml`).
- **Release notes:** a snapshot does not write them. To preview them, tag the commit in a throwaway clone and run `goreleaser release --clean --skip=publish,sign,validate` there: the notes are in `dist/CHANGELOG.md`.
