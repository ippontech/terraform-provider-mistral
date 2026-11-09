# Releasing

A maintainer releases by pushing a version tag on a commit of `main`. The tag starts `goreleaser-release.yml`, which checks that the commit is on `main`, then builds, signs and publishes the GitHub release with its notes. The Terraform Registry and the OpenTofu Registry ingest that release.

Nothing is released while the repository is private: the release job skips, since the registries ingest public repositories only.

## Cutting a release

A release is a decision of a maintainer, who creates its tag: no workflow, App or bot can create a version tag. Everything around that decision is automated, as GoReleaser does for its own releases (a task that computes the next version, then asks for a confirmation before it tags):

- the **Unreleased changes** issue, which `release-reminder.yml` keeps up to date on every push to `main` and every release tag, lists the commits that no release has published, grouped as the notes will be, and the version to tag next; it is how a fix waiting on `main` gets noticed. The workflow can write issues, and nothing else, and updates only the issue its own bot opened;
- the version comes from the Conventional Commits since the last published release (*Choosing the version* below);
- the release job refuses, before the approval, a tag below that version or not above every other release tag (*Release security*).

To release:

1. Read the **Unreleased changes** issue, or `git log --oneline <last-tag>..origin/main`, and decide that `main` is worth releasing.
2. On an up-to-date, clean checkout of `main`, run `make release-tag`. It prints the version and the commit, waits for you to type that version, then tags the commit and pushes the tag. `make release-version` prints the version alone. To tag another version, such as 1.0.0, tag it by hand: `git tag -a v1.0.0 -m v1.0.0 <sha> && git push origin v1.0.0`. The tag ruleset lets repository admins only create a version tag, and nobody move or delete one.
3. Approve the release job (*Approving a release* below), then check the GitHub release: its archives, `SHA256SUMS`, the signature and the notes.

The release notes group the commits by type (`.goreleaser.yml`, `changelog`): breaking changes (`!` after the type), features, fixes, performance, reverts (GitHub's `Revert "..."` subjects and `revert:`), then the rest; `chore`, `ci`, `test` and `style` are left out, and so are their reverts. Every pull request checks the format (`conventional-commits.yml`), so a commit cannot land outside these groups. A tag with a pre-release suffix (`v1.0.0-rc.1`) publishes a GitHub pre-release. The notes start at the last published release (*Choosing the version*), which the release job hands to GoReleaser (`GORELEASER_PREVIOUS_TAG`): the notes of a stable release include what its pre-releases shipped, and those of a release after a refused tag include the commits of that tag.

## Choosing the version

`tools/release-version.sh` computes the version from the subjects of the commits since the last published release, with the rules of *Versions* below; `tools/release-version_test.sh` tests it on every pull request.

- **The last published release** is the latest GitHub release that is neither a draft nor a pre-release, which the script reads with `gh`. A tag whose release was refused, or stopped before publishing, publishes nothing: its commits stay pending, and the next version goes above it. Without `gh`, the last release tag stands for the last published release, with a warning.
- **The subjects only.** Each rule matches the start of a subject: GitHub's `Revert "feat(...): ..."` is a revert, not a feature. Bodies are not read, so the upstream changelogs that Renovate quotes in its commits never count, and a breaking change is marked with `!` in the subject.
- **The first release** is `v0.1.0`. A pre-release tag (`v0.2.0-rc.1`) is checked by its core version, and is never the last published release.
- **The modes.** `next` (`make release-version`) prints the version, or fails when there is nothing to release; `tag` (`make release-tag`) tags and pushes it once confirmed; `check` runs in the release job, and `previous` gives it the last published release, where the `go.mod` summary and the notes start; `pending` writes the body of the **Unreleased changes** issue.

If the release job refuses a tag, nothing was approved, signed or published, but the tag stays: tag the version the error gives. The refused tag holds its number for good, and the next versions go above it.

## Approving a release

The release job waits in `Actions > GoReleaser Release`, on the run of the new tag, behind **Review deployments**, for a reviewer of the `release` environment. The approval releases the GPG key, so it is the last human check before the modules of the release ship signed to every user. Before approving:

1. Open the run's summary. The `dependency-changes` job lists the `go.mod` lines that changed since the last published release, or every module for the first release. `go.mod` lists every module compiled into the provider, indirect ones included. "None" means the release compiles the same modules as the previous one.
2. For each changed module, check that the new version is a published release of that module (a tag or release notes on its repository), and look for anything unexpected: a module nobody added on purpose, a version published days ago without notes, a repository that changed owner.
3. Approve. If in doubt, reject: nothing is built, signed or published, and no GitHub release is created. The tag stays, since the tag ruleset forbids deleting it, and the next release takes the next version number. The notes of that next release start at the last published release, so they include the commits of the rejected tag; edit them to say why the number was skipped. Never re-sign or replace a published version: release a new one.

## Versions

| Commits since the last tag | Next version in 0.x | After 1.0.0 |
|---|---|---|
| a breaking change (`feat!:`, `fix!:`) | minor | major |
| a `feat` | minor | minor |
| only `fix`, `perf`, `build`, `refactor`, `docs`, `revert` and GitHub's `Revert "..."` | patch | patch |
| only `chore`, `ci`, `test`, `style` | no release | no release |

**The provider stays in 0.x until 1.0.0 is decided.** While in 0.x:

- **Breaking changes** publish a minor version. The commit marks them with `!`, which puts them under *Breaking changes* in the notes, and its body says what users must change; the notes show the subject only, so the subject names what breaks.
- **Names first.** Get the resource, attribute and import ID names right before 1.0.0. After it, a rename costs a major version and a `moved` block for every user.
- **Condition for 1.0.0.** Every published resource and data source has passed an acceptance test against the live API (its registry entry says `acceptanceTested`). This includes the Admin API ones, so it needs Enterprise access from Mistral.

The first release is `v0.1.0`, an alpha: Hector Basset decided on 2026-10-30 to publish before any acceptance test against the live API. The index of the documentation says so (`templates/index.md.tmpl`), and so does the page of each type. Version numbers keep no pre-release suffix such as `-alpha.1`: Terraform and OpenTofu select a pre-release version only through an exact version constraint, so a `~>` or `>=` constraint would never install it.

## Release security

The release job holds the signing key. It follows the rules for privileged jobs of the `ippontech/terraform-provider-anthropic` release hardening, without its release automation, which needed a GitHub App able to push tags:

1. The secrets live in the protected `release` environment, not in repository secrets: `GPG_PRIVATE_KEY` and `GPG_PASSPHRASE`, for GoReleaser.
2. A ruleset protects the version tags (`.github/rulesets/version-tags.json`): only repository admins create them, and nobody updates or deletes them.
3. Before the approval, the release job refuses a tag below the version the commits since the last published release call for, or not above every other release tag (*Choosing the version*), so that no approver signs a wrong number. A higher number, such as 1.0.0, is a maintainer's choice and passes. After it, the job refuses a tag whose commit is not on `main`, and stops before publishing anything if the imported key is not `08C47C742E19A68E`, the key of this provider (*Signing key*, and *Check the signing key* in the workflow): the version tag cannot be deleted, but with no GitHub release created yet, the failed jobs run again on the same tag once the secret is fixed. GoReleaser signs with that exact primary key (`!` after the fingerprint in `.goreleaser.yml`), never with a signing subkey the registries may not know. Rotating the key means registering the new one in both registries first, then changing the fingerprint of that step in a pull request before the next tag: a run reads the workflow as its tag holds it, so a rerun cannot pick up a new fingerprint.
4. No cache is restored or saved in the job that signs (`cache: false`). A tag run can restore the caches of `main`, so a cache written by any job on `main` could otherwise run code there. A pull request's cache is not the risk: only that pull request can restore it.
5. Every checkout uses `persist-credentials: false`, and the modules are verified against `go.sum` before the build.
6. A human reviews every bump of the release tooling (GoReleaser, the GPG import action). Renovate labels these pull requests `release-tooling` and never merges them automatically.

## Signing key

The provider signs its releases with a key of its own, `08C47C742E19A68E` (fingerprint `B746617CC1BA2F43396E200008C47C742E19A68E`, uid `Ippon Technologies (terraform-provider-mistral) <cloud.fr.coreteam@ippon.fr>`), generated on 2026-11-03. `ippontech/anthropic` signs with another key, `C4C6311F36624DB7`. The two keys can isolate the providers on the OpenTofu Registry only, where a key can be scoped to one provider, and they do once neither key is also registered there for the whole namespace (below). On the Terraform Registry every key of the `ippontech` namespace validates every provider of the namespace (below), so a leak of either key lets its holder sign releases of both providers there: audit the releases of both, whichever key leaked. The private key, its passphrase and its revocation certificate are in Ippon's Vaultwarden, organization **Cloud & DevOps**, folder **Terraform Mistral provider**.

The key is RSA 4096 without expiration, as the key of `ippontech/anthropic`: the Terraform Registry serves the copy it received and offers no way to replace or delete it, so an expiration cannot be extended there, and GnuPG refuses to sign with an expired key, which would break the release job at that date for no benefit.

The key is known to both registries since 2026-11-03:

- the Terraform Registry holds it for the `ippontech` namespace (*Settings > GPG Keys*), where a key has no per-provider scope: every key of the namespace validates every provider of the namespace, and a key added there is permanent;
- the OpenTofu Registry holds it for `ippontech/mistral` only (opentofu/registry#6292, submitted with the *Submit new Provider Signing Key* issue form, which validates a provider name without the `terraform-provider-` prefix).

The OpenTofu Registry serves for a provider the keys registered for it together with the keys registered for its whole namespace, and `tofu init` accepts a release that any of them signed. `C4C6311F36624DB7` is registered there twice: for `ippontech/anthropic`, and for the whole `ippontech` namespace (`keys/i/ippontech/provider-1793373469.asc`, opentofu/registry#6197). As long as that namespace copy stays, the key of `ippontech/anthropic` also validates the releases of this provider on the OpenTofu Registry, so a leak of it calls for auditing the releases of both providers there too. Withdrawing that copy completes the separation. The registry has issue forms to submit a key or to remove a version, none to withdraw a key: ask its maintainers in an issue.

**Rotating the key**, when its private part may have leaked or when the maintainer who holds it leaves, adds a new key next to the old one; the old one stays in the registries, so the versions it signed stay verifiable:

1. Generate a new key as above (`gpg --full-generate-key`: RSA and RSA, 4096, no expiration, the same uid) and store its private part, passphrase and revocation certificate (`gpg --gen-revoke`) in Vaultwarden, in the same folder, without deleting the old entry: it stays the record of which key signed the versions published so far.
2. Register the public key (`gpg --armor --export <KEY_ID>`) in both registries, as above.
3. Replace both secrets of the `release` environment. `GPG_PRIVATE_KEY` comes from a file rather than the clipboard, so that no whitespace is altered: `gpg --armor --export-secret-keys <KEY_ID> > private.asc`, then `gh secret set GPG_PRIVATE_KEY --env release --repo ippontech/terraform-provider-mistral < private.asc`, and delete the file. `GPG_PASSPHRASE` comes from the prompt of `gh secret set GPG_PASSPHRASE --env release --repo ippontech/terraform-provider-mistral`, never from `--body`, which would leave it in the shell history. A passphrase left at the old key's value fails the GPG import of the next release.
4. Change the fingerprint of *Check the signing key* in `goreleaser-release.yml` and the key IDs of this file in a pull request, merged before the next tag.
5. If the private key leaked, publish its revocation certificate (`gpg --import <certificate>` then `gpg --send-keys <OLD_KEY_ID>`). The registries ignore revocations, so the versions already published keep installing; the revocation only protects uses of the key outside the registries.

## Before the first release

These steps need an owner of the GitHub organization and of the registry namespaces.

1. **Move the repository.** Transfer it to the `ippontech` organization and make it public. GitHub redirects the old URLs. Done on 2026-10-29.
2. **Apply the rulesets**, done on 2026-10-29 through the API (`POST /repos/ippontech/terraform-provider-mistral/rulesets` with each file), or in *Settings > Rules > Rulesets > Import a ruleset*:
   - `.github/rulesets/main.json`: linear history, pull request required, the CI checks required, no force push. It requires no approval: GitHub does not let the author of a pull request approve it, and with one maintainer the review is the merge itself. Raise `required_approving_review_count` to 1, with `require_last_push_approval`, once a second maintainer reviews.
   - `.github/rulesets/version-tags.json`: only the *Repository admin* role (`RepositoryRole` 5 in the bypass list) creates a version tag, and nobody updates or deletes one.
3. **Create the `release` environment** with its two secrets. Restrict it to the `v*` tags and require a reviewer, who checks the module changes before each release (*Approving a release*). The environment exists since 2026-10-29, restricted to `v*` with Hector Basset as reviewer; its two secrets hold the key of *Signing key* since 2026-11-03.
4. **Prepare the acceptance tests.** Create the `acctest` environment, restricted to `main`, with the secrets `MISTRAL_API_KEY` (a standard key of a workspace of a dedicated test organization) and, once Mistral grants Admin API access, `MISTRAL_ADMIN_API_KEY`, and the variables of the objects the tests cannot create: `MISTRAL_ACC_WORKSPACE_ID` (the workspace of `MISTRAL_API_KEY`), `MISTRAL_ACC_ADMIN_USER_ID` (an organization admin) and `MISTRAL_ACC_MEMBER_USER_ID` (another member of that workspace). A test whose variable is missing skips. Then set the repository variable `MISTRAL_ACCTEST` to `true`: until then `testacc.yml` skips its job.
5. **Create the `spec-drift` label**, which `spec-drift.yml` puts on the issue it opens.
6. **Publish on the Terraform Registry.** Sign in to https://registry.terraform.io with GitHub, choose *Publish > Provider*, then the `ippontech` organization and `terraform-provider-mistral`. Add the public key of *Signing key* under *Settings > GPG Keys* of the `ippontech` namespace, done on 2026-11-03. The Registry requires RSA or DSA keys, and rejects the default ECC type. The Registry installs a webhook and ingests every later release by itself.
7. **Publish on the OpenTofu Registry.** Open a *Submit new Provider* issue at https://github.com/opentofu/registry/issues/new/choose for `ippontech/terraform-provider-mistral`. Do not open a pull request or use the API: the registry only accepts the issue forms.
   - **Signing key.** Submitted for `ippontech/mistral` on 2026-11-03 (opentofu/registry#6292), see *Signing key*. OpenTofu checks the key's expiry at submission; the Terraform Registry does not.

## Checking a release

- **Local snapshot:** `goreleaser release --snapshot --clean --skip=sign,publish` builds the 13 archives, the `SHA256SUMS` file and the registry manifest locally.
- **Pull requests:** `goreleaser check`, then the same snapshot build, without signing or publishing, run on every pull request and in the merge queue (`goreleaser-check.yml`, required check `goreleaser-snapshot`).
- **Release notes:** a snapshot does not write them. To preview them, tag the commit in a throwaway clone and run `goreleaser release --clean --skip=publish,sign,validate` there: the notes are in `dist/CHANGELOG.md`.
