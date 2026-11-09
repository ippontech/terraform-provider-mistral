# Releasing

A release is cut once a week by [semantic-release](https://github.com/semantic-release/semantic-release), on Monday morning, from the commits merged into `main` since the previous one: the version comes from their Conventional Commits types (*Versions* below), the notes from their subjects. Nobody tags or writes notes by hand. GoReleaser then builds, signs and attaches the archives, after the approval of a reviewer (*Approving a release*), and the Terraform Registry and the OpenTofu Registry ingest the GitHub release.

Nothing is released while the repository is private: the signing job skips, since the registries ingest public repositories only.

## How a release is cut

Three workflows share the work:

- `testacc.yml` runs the acceptance tests against the live API, with the keys of the `acctest` environment, on every push to `main` (except the push of the release commit, told apart by its `chore(release):` subject and its content, `CHANGELOG.md` alone). They run after the merge because a pull request from a fork cannot read those keys. A push runs them and stops there: it releases nothing. The job runs only once the repository variable `MISTRAL_ACCTEST` is `true`, which is set when the test organization exists (*Repository setup*).
- `semantic-release.yml` runs every Monday at 07:23 UTC (08:23 in Paris in winter, 09:23 in summer), or on demand (`Actions > Semantic Release > Run workflow`, for a fix that cannot wait for the next Monday). It first checks that a commit since the last tag would cut a version (the `pending` job runs semantic-release's own commit analyzer with the `.releaserc` rules, through `.github/semantic-release/next-release.mjs`), so a week of `chore` or `ci` merges runs no live test; then it runs the acceptance tests again from the head of `main`, and only if they pass (or if `MISTRAL_ACCTEST` keeps them off: the alpha versions ship without live tests, *Versions*) semantic-release reads every commit since the last tag, chooses the version, and pushes the `chore(release): <version>` commit with `CHANGELOG.md`, the `v<version>` tag and the GitHub release with the notes, as a draft (`draftRelease` in `.releaserc`): the signing job publishes it once the archives are attached. It authenticates as the `tf-provider-mistral-semrel` GitHub App (*GitHub App*), whose pushes bypass the `main` and `version tags` rulesets and, unlike the job's own token, start the workflows that listen to them.
- `goreleaser-release.yml` starts on the tag. It checks that the tagged commit is on `main`, lists the `go.mod` changes since the previous release on its summary page, then waits for a reviewer of the `release` environment (*Approving a release*). Once approved, it builds the archives, signs the checksums with the key of *Signing key*, attaches everything to the draft release semantic-release created, keeping its notes (`release.mode: keep-existing` in `.goreleaser.yml`), and publishes it last: the Terraform Registry reads a release when it is published, and finds the assets there. Until then the version is a draft, which only maintainers see. If a version does not show up at https://registry.terraform.io/providers/ippontech/mistral/latest some minutes after the signing job turned green, the *Resync* button on the provider's settings page in the Registry re-creates its webhook. The OpenTofu Registry scans the releases on its own schedule.

A second scheduled run, on Monday at 12:23 UTC, is a retry: it cuts a version only if the day still has none, and stops at once while the morning run is still testing or releasing, rather than run the live suite a second time for the release that run cuts. It catches a morning run that GitHub dropped (it schedules nothing at the top of the hour for that reason), one cancelled while it waited behind a push run, and one whose tests ran while a pull request merged: semantic-release then sees `main` ahead of the commit it tested, publishes nothing and exits green. Those commits ship at the retry, at a manual run, or the next Monday.

A red acceptance run publishes nothing: fix the cause, or, if the API itself was failing, re-run the failed jobs of that run, or start a manual run. `Actions > Acceptance Tests > Run workflow` still runs the tests alone, without releasing.

The push and manual runs of `testacc.yml` and the `acceptance-tests` job of `semantic-release.yml` share one `concurrency` group (`acctest`, no cancellation), so a single live test run exists at a time: a release run that finds a push run testing waits for it, and when several commits land while a run is testing, only the newest waiting run starts. The group is on that job alone, so a release run that tests nothing (nothing to release, or the retry after a morning release) cancels no waiting push run. A running test is never cancelled to make room: the objects it had created would stay in the test organization, since nothing deletes them afterwards.

GitHub disables a scheduled workflow after 60 days without activity on the repository; Renovate's pull requests keep it alive, and the Actions UI says so if it ever happens.

## Approving a release

The signing job waits in `Actions > GoReleaser Release`, on the run of the new tag, behind **Review deployments**, for a reviewer of the `release` environment. The approval releases the GPG key, so it is the last human check before the modules of the release ship signed to every user. At that point the GitHub release already exists as a draft, with its notes and no asset, and `CHANGELOG.md` already holds its entry. Each tag has its own run, so several can wait at once without one cancelling another. Before approving:

1. Open the run's summary. The `dependency-changes` job lists the `go.mod` lines that changed since the last release that shipped signed binaries (`tools/previous-release.sh`: a version whose signing was refused does not count, so its changes are listed again, and the summary names it). `go.mod` lists every module compiled into the provider, indirect ones included. "None" means the release compiles the same modules as the previous one.
2. For each changed module, check that the new version is a published release of that module (a tag or release notes on its repository), and look for anything unexpected: a module nobody added on purpose, a version published days ago without notes, a repository that changed owner. A maintainer merged each of these updates, since Renovate merges nothing on its own: this is a second look, not the first.
3. Approve. If in doubt, reject: nothing is built, signed or published, and the GitHub release stays a draft that the registries never see. The tag stays, since the tag ruleset forbids deleting it: delete the draft, so that nobody publishes it by hand, and say in the notes of the next version why the number was skipped (semantic-release numbers from the tag, and the next release's notes start after it). Never re-sign or replace a published version: release a new one.

A signing run that fails for a transient reason (a module proxy error, a GitHub outage) can be re-run as is: it builds the same tag again.

`ippontech/terraform-provider-anthropic` removed this approval: with a release a day, the approvals queued up and the numbering got holes, and the review at merge time already checks the same modules. With a release a week it costs one approval on Monday; drop it the same way if it gets in the way, by unticking **Required reviewers** on the `release` environment and saying so here.

## Versions

semantic-release applies these rules (`.releaserc`, `releaseRules`), to the commits since the last `v*` tag on `main`, the highest bump winning:

| Commits since the last tag | Next version in 0.x | After 1.0.0 |
|---|---|---|
| a breaking change (`feat!:`, `fix!:`, a `BREAKING CHANGE:` footer) | minor | major |
| a `feat` | minor | minor |
| only `fix`, `perf`, `build`, `refactor`, `docs`, `revert` | patch | patch |
| only `chore`, `ci`, `test`, `style` | no release | no release |

- A `!` after the type or a `BREAKING CHANGE:` footer cuts a minor in 0.x, whatever the type (a `chore(deps)` aside, below), through the `breaking` rule of `.releaserc`; the release notes list the change under *Breaking changes*, so the subject names what breaks, and the body says what users must change.
- A commit scoped `deps` never cuts more than its type gives: `chore(deps)` cuts nothing and `fix(deps)` a patch, even with a `BREAKING CHANGE:` line in its body (Renovate's pull request descriptions carry upstream release notes, and a squash merge makes the description the body of the commit). One exception in 0.x: a `fix(deps)` whose body carries such a line cuts a minor, since the `breaking` rule matches it too; the maintainer who merges a Renovate pull request edits its description first if it has one. After 1.0.0 the `deps` rules alone decide, as in `ippontech/terraform-provider-anthropic`.
- The parser reads `BREAKING CHANGE:` and `BREAKING-CHANGE:` at the start of a line case-insensitively: a `Breaking change: none` line in a pull request description cuts a minor. Write such a line nowhere.
- A `revert:` commit cuts a patch: it undoes a shipped change. Retitle a pull request opened with GitHub's Revert button `revert: <subject>`: the title check refuses `Revert "..."`, because a squash merge replaces git's `This reverts commit <sha>.` line with the description, and semantic-release would then see neither a type nor a revert (a `Revert "..."` commit that git itself made, in a rebase merge, is still read as one).
- The release notes and `CHANGELOG.md` group the commits by type (`.releaserc`, `release-notes-generator`): breaking changes, features, bug fixes, performance, reverts, build, refactoring, documentation; `chore`, `ci`, `test` and `style` are left out. Each entry links to its commit and to the issues it closes.
- A `chore(release):` commit is semantic-release's own: `testacc.yml` skips its push, and the next analysis starts after its tag.

**The provider stays in 0.x until 1.0.0 is decided.** While in 0.x:

- **Breaking changes** publish a minor version, as above.
- **Names first.** Get the resource, attribute and import ID names right before 1.0.0. After it, a rename costs a major version and a `moved` block for every user.
- **Condition for 1.0.0.** Every published resource and data source has passed an acceptance test against the live API (its registry entry says `acceptanceTested`). This includes the Admin API ones, so it needs Enterprise access from Mistral.
- **Cutting 1.0.0.** semantic-release has no "stable" switch: change the `release` of the `breaking` rule in `.releaserc` from `minor` to `major` in a pull request, then merge the change that declares the provider stable as `feat!:` with a `BREAKING CHANGE:` footer that says so. Change the rule, never remove it: the analyzer consults its default rules only when no custom rule matches, and the type rules (`refactor`, `docs`, ...) would match a `refactor!:` and cut a patch. From then on the table's last column applies without any change to the configuration.

The first release, `v0.1.0`, was tagged by hand on 2026-11-08, an alpha: Hector Basset decided on 2026-10-30 to publish before any acceptance test against the live API. The index of the documentation says so (`templates/index.md.tmpl`), and so does the page of each type. Version numbers keep no pre-release suffix such as `-alpha.1`: Terraform and OpenTofu select a pre-release version only through an exact version constraint, so a `~>` or `>=` constraint would never install it, and `.releaserc` lists `main` as its only branch, so semantic-release cuts no pre-release either.

## Release security

Two jobs hold a release credential: the signing job of `goreleaser-release.yml`, with the GPG key, and the release job of `semantic-release.yml`, with the GitHub App that bypasses the rulesets of `main` and of the version tags. Write access to the repository must not be enough to use either: a collaborator, or a stolen collaborator token, can push a workflow file on a new branch, and that workflow receives every repository secret. The rules, from the release hardening of `ippontech/terraform-provider-anthropic`:

1. The secrets live in protected environments, never in repository or organization secrets: `GPG_PRIVATE_KEY` and `GPG_PASSPHRASE` in `release`, whose deployment policy admits the `v*` tags alone; `SEMREL_APP_ID` and `SEMREL_APP_PRIVATE_KEY` in `semantic-release`, whose policy admits `main` alone; the Mistral keys in `acctest`, `main` alone. A workflow pushed on any other ref cannot read them.
2. Rulesets protect `main` and the version tags (`.github/rulesets/`). On `main`: linear history, a pull request with the CI checks, no force push; the semantic-release App is the only bypass actor, for its release commit. On `v*` tags: only the App and repository admins create one (the admins for a tag by hand, such as `v0.1.0`), and nobody updates or deletes one.
3. The signing job refuses a tag whose commit is not on `main`, and stops before publishing anything if the imported key is not `08C47C742E19A68E`, the key of this provider (*Signing key*, and *Check the signing key* in the workflow): the version tag cannot be deleted, but with no asset attached yet, the failed jobs run again on the same tag once the secret is fixed. GoReleaser signs with that exact primary key (`!` after the fingerprint in `.goreleaser.yml`), never with a signing subkey the registries may not know. Rotating the key means registering the new one in both registries first, then changing the fingerprint of that step in a pull request before the next release: a run reads the workflow as its tag holds it, so a rerun cannot pick up a new fingerprint.
4. No cache is restored or saved in a job that holds a secret (`cache: false` on `actions/setup-go`, no cache on `actions/setup-node`). A tag run can restore the caches of `main`, so a cache written by any job on `main` could otherwise run code there. The semantic-release job installs its toolchain from the committed lockfile with `npm ci --ignore-scripts` (`.github/semantic-release`, `.npmrc`): every package is checked against its integrity hash, and no package lifecycle script runs.
5. Every checkout uses `persist-credentials: false`, and the modules are verified against `go.sum` before the build. The App token is minted after the install, with the three permissions semantic-release uses (contents, issues, pull requests), and handed to the semantic-release step alone.
6. A human reviews every bump of the release tooling: GoReleaser and the GPG import action (the signing job), the semantic-release packages and their lockfile, the Node setup and the App token action (the release job). Renovate labels these pull requests `release-tooling` and never merges them automatically.

A pull request cannot cut a release: `semantic-release.yml` runs on a schedule and on demand only, from `main`, and the `pending` job reads the repository with the job's read-only token.

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

## GitHub App

semantic-release pushes the release commit and the tag to `main`, which the `main` ruleset forbids to everyone, and creates the GitHub release. It does so as a GitHub App of its own, `tf-provider-mistral-semrel`, for two reasons: a push made with the job's `GITHUB_TOKEN` starts no workflow, so `goreleaser-release.yml` would never run on the tag; and an App is the only kind of actor a ruleset can let bypass it that is neither a person nor a role.

The App belongs to the `ippontech` organization and is installed on this repository only. Its private key, with the App ID, is in Ippon's Vaultwarden, organization **Cloud & DevOps**, folder **Terraform Mistral provider**, next to the GPG key. The credentials of `ippontech/terraform-provider-anthropic` (`tf-provider-anthropic-semrel`) are not shared: a leaked key then lets its holder release one provider, not both.

### Setting it up

These steps need an owner of the `ippontech` GitHub organization, and are done once (*Repository setup* records the dates).

1. **Create the App.** On github.com, *Organization `ippontech` > Settings > Developer settings > GitHub Apps > New GitHub App* (https://github.com/organizations/ippontech/settings/apps/new):

   | Field | Value |
   |---|---|
   | GitHub App name | `tf-provider-mistral-semrel` |
   | Homepage URL | `https://github.com/ippontech/terraform-provider-mistral` (required, unused) |
   | Webhook | untick **Active** |
   | Repository permissions | **Contents**: Read and write; **Issues**: Read and write; **Pull requests**: Read and write; **Metadata**: Read-only (set by GitHub) |
   | Organization and account permissions | none |
   | Where can this GitHub App be installed? | **Only on this account** |

   `semantic-release.yml` requests exactly these three write permissions when it mints the token (`permission-*` inputs of `actions/create-github-app-token`): a permission added to the App later does not reach the token, and if the App lacks one of the three, the token step fails. Click **Create GitHub App** and note the **App ID** shown at the top of its settings page.

2. **Generate a private key.** On the App's settings page, *Private keys > Generate a private key*: a `.pem` file downloads. Store it and the App ID in Vaultwarden (folder above), then delete the download once the secret is set.

3. **Install the App on the repository.** On the App's settings page, *Install App > ippontech > Only select repositories > `terraform-provider-mistral` > Install*.

4. **Create the `semantic-release` environment** (*Settings > Environments > New environment*), with **Deployment branches and tags** set to *Selected branches and tags* and one branch rule, `main`; no required reviewer (a reviewer there would hold the release commit, not the signing, which `release` already guards). Then its two secrets, from a file rather than the clipboard for the key, so that no whitespace is altered:

   ```sh
   gh secret set SEMREL_APP_ID --env semantic-release --repo ippontech/terraform-provider-mistral
   gh secret set SEMREL_APP_PRIVATE_KEY --env semantic-release --repo ippontech/terraform-provider-mistral < tf-provider-mistral-semrel.*.private-key.pem
   ```

   The first command prompts for the value (the numeric App ID); never pass either with `--body`, which would leave it in the shell history. Check that no secret of the same name exists at repository or organization level.

5. **Let the App bypass the two rulesets**, on *Settings > Rules > Rulesets*: open `main`, then *Bypass list > Add bypass > Apps*, pick `tf-provider-mistral-semrel`, mode **Always**, save; the same on `version tags`, whose list already holds the *Repository admin* role. Or through the API, with the App ID in place of `<APP_ID>` (`actor_type` `Integration` is a GitHub App), for the ruleset IDs that `gh api repos/ippontech/terraform-provider-mistral/rulesets --jq '.[] | "\(.id) \(.name)"'` prints:

   ```sh
   gh api --method PUT repos/ippontech/terraform-provider-mistral/rulesets/<MAIN_RULESET_ID> \
     --input <(gh api repos/ippontech/terraform-provider-mistral/rulesets/<MAIN_RULESET_ID> \
       | jq '{name, target, enforcement, conditions, rules, bypass_actors: [{actor_id: <APP_ID>, actor_type: "Integration", bypass_mode: "always"}]}')
   ```

   ```sh
   gh api --method PUT repos/ippontech/terraform-provider-mistral/rulesets/<TAGS_RULESET_ID> \
     --input <(gh api repos/ippontech/terraform-provider-mistral/rulesets/<TAGS_RULESET_ID> \
       | jq '{name, target, enforcement, conditions, rules, bypass_actors: (.bypass_actors + [{actor_id: <APP_ID>, actor_type: "Integration", bypass_mode: "always"}])}')
   ```

   Then add the same `bypass_actors` entry to `.github/rulesets/main.json` and `.github/rulesets/version-tags.json` in a pull request, so that the files stay what the live rulesets hold.

6. **Check.** `Actions > Semantic Release > Run workflow` on `main`. The `pending` job says on its summary page which bump the commits since the last tag would cut; if `none`, the run stops there and proves nothing more: merge a `fix:` or wait for one. Otherwise the release job must push the `chore(release):` commit and the tag, and `goreleaser-release.yml` must start on the tag and wait for its approval. A `Resource not accessible by integration` error from the token step means a permission is missing on the App; a rejected push means the App is not in the bypass list of the ruleset the error names.

### Rotating the App key

When the private key may have leaked, or the maintainer who holds it leaves: on the App's settings page, generate a new private key, replace `SEMREL_APP_PRIVATE_KEY` in the `semantic-release` environment (step 4), check with a manual run (step 6), then delete the old key on the same page and note the rotation in Vaultwarden. The App ID does not change. If the App itself must go, delete it on its settings page: its installation, and so its bypass entries, go with it, and the next `semantic-release.yml` run fails at the token step until a new App is set up.

## Repository setup

These steps need an owner of the GitHub organization and of the registry namespaces. Each one records when it was done.

1. **Move the repository.** Transfer it to the `ippontech` organization and make it public. GitHub redirects the old URLs. Done on 2026-10-29.
2. **Apply the rulesets**, done on 2026-10-29 through the API (`POST /repos/ippontech/terraform-provider-mistral/rulesets` with each file), or in *Settings > Rules > Rulesets > Import a ruleset*:
   - `.github/rulesets/main.json`: linear history, pull request required, the CI checks required, no force push. It requires no approval: GitHub does not let the author of a pull request approve it, and with one maintainer the review is the merge itself. Raise `required_approving_review_count` to 1, with `require_last_push_approval`, once a second maintainer reviews.
   - `.github/rulesets/version-tags.json`: only the *Repository admin* role (`RepositoryRole` 5 in the bypass list) creates a version tag, and nobody updates or deletes one.
   - The semantic-release App joins both bypass lists in *GitHub App*, step 5. To do.
3. **Create the `release` environment** with its two secrets. Restrict it to the `v*` tags and require a reviewer, who checks the module changes before each release (*Approving a release*). The environment exists since 2026-10-29, restricted to `v*` with Hector Basset as reviewer; its two secrets hold the key of *Signing key* since 2026-11-03.
4. **Prepare the acceptance tests.** Create the `acctest` environment, restricted to `main`, with the secrets `MISTRAL_API_KEY` (a standard key of a workspace of a dedicated test organization) and, once Mistral grants Admin API access, `MISTRAL_ADMIN_API_KEY`, and the variables of the objects the tests cannot create: `MISTRAL_ACC_WORKSPACE_ID` (the workspace of `MISTRAL_API_KEY`), `MISTRAL_ACC_ADMIN_USER_ID` (an organization admin) and `MISTRAL_ACC_MEMBER_USER_ID` (another member of that workspace). A test whose variable is missing skips. Then set the repository variable `MISTRAL_ACCTEST` to `true`: until then `testacc.yml` skips its job, on a push and before a release alike.
5. **Create the `spec-drift` label**, which `spec-drift.yml` puts on the issue it opens.
6. **Publish on the Terraform Registry.** Sign in to https://registry.terraform.io with GitHub, choose *Publish > Provider*, then the `ippontech` organization and `terraform-provider-mistral`. Add the public key of *Signing key* under *Settings > GPG Keys* of the `ippontech` namespace, done on 2026-11-03. The Registry requires RSA or DSA keys, and rejects the default ECC type. The Registry installs a webhook and ingests every later release by itself.
7. **Publish on the OpenTofu Registry.** Open a *Submit new Provider* issue at https://github.com/opentofu/registry/issues/new/choose for `ippontech/terraform-provider-mistral`. Do not open a pull request or use the API: the registry only accepts the issue forms.
   - **Signing key.** Submitted for `ippontech/mistral` on 2026-11-03 (opentofu/registry#6292), see *Signing key*.
8. **Set up the semantic-release App**, the six steps of *GitHub App*. To do. Until they are done, `semantic-release.yml` fails at its token step every Monday, and nothing is released: a release by hand then follows the old procedure, a `v*` tag pushed by a repository admin on a commit of `main`, with notes written by hand on the GitHub release that GoReleaser creates.

## Checking a release

- **Local snapshot:** `goreleaser release --snapshot --clean --skip=sign,publish` builds the 13 archives, the `SHA256SUMS` file and the registry manifest locally.
- **Pull requests:** `goreleaser check`, then the same snapshot build, without signing or publishing, run on every pull request and in the merge queue (`goreleaser-check.yml`, required check `goreleaser-snapshot`).
- **Previous release:** `tools/previous-release.sh` prints the last release with signed binaries reachable from a commit, the baseline of the `go.mod` summary of the release job; `tools/previous-release_test.sh` tests it on every pull request, with a fake `gh`.
- **Next version:** after `npm ci --ignore-scripts` in `.github/semantic-release`, `node .github/semantic-release/next-release.mjs` prints the last release tag and the bump the commits since it would cut (`major`, `minor`, `patch` or `none`), with the analysis of each commit on stderr: the same code and configuration as the `pending` job. The notes themselves exist only once the release is cut; they follow the format of `CHANGELOG.md`.
