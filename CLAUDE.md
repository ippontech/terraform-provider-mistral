# CLAUDE.md

Terraform and OpenTofu provider for Mistral AI, in Go, on the Terraform Plugin Framework only (protocol 6). It manages objects of a workspace with a standard API key, and the organization through the Admin API with an Admin API key. This file holds the rules for the whole repository; each service directory has its own `CLAUDE.md` for what is specific to its API, which Claude Code loads only there.

## Architecture

| Path | Role |
|---|---|
| `main.go` | Serves `registry.terraform.io/ippontech/mistral`. |
| `internal/provider` | Provider schema and `Configure`, and `registry.go`: every resource and data source factory, with its audience, its stability at Mistral and its live coverage. |
| `internal/providerdata` | What `Configure` hands to the types: the resolved configuration and the API client. No import of `internal/provider`. |
| `internal/client` | The hand-written HTTP client of the Mistral API: transport, authentication, retries, pagination, errors, and one file of typed calls per API area. No Terraform import. |
| `internal/services/<area>` | Resources and data sources of one API area, their schemas and state mapping, their tests and their fake. No import of `internal/provider`. |
| `internal/sdktest` | The fake Mistral API for unit tests (test helper, never in the binary). |
| `internal/providertest` | Provider factories and configuration pointed at the fake (test helper). |
| `internal/acctest` | Helpers for acceptance tests against the live API (test helper). |
| `tools/` | A Go module of its own: tfplugindocs, the complexity ratchet, the coverage gate, `specdrift` (fingerprints of the operations the provider calls). |
| `templates/`, `examples/` | Sources of `docs/`, which `make generate` rebuilds. Never edit `docs/` by hand. |
| `tests/` | Terraform native tests; `tests/versions.tf` is their root module. |

The client is written by hand: the published OpenAPI specification (`https://docs.mistral.ai/openapi.yaml`) does not load in oapi-codegen v2.8.1 (14 invalid `#/$defs/` references), and it diverges from the official SDKs on some paths. It has no licence, so it is not committed here.

## Adding a resource or data source

1. Write `internal/services/<area>/<name>_resource.go` (or `_data_source.go`), in `package <area>`.
2. Register its factory in `internal/provider/registry.go`, with on the same line its `Audience` (`providerdata.Workspace` or `providerdata.Admin`), its `Stability` (`beta` when the specification tags its endpoints `beta.`) and its `Live` coverage (`fakeOnly` until an acceptance test has passed against the live API).
3. Add an example: `examples/resources/<name>/resource.tf` (or `examples/data-sources/<name>/data-source.tf`) holds only the blocks the Registry page shows; the `terraform {}` block goes in a sibling `versions.tf` with `required_version = ">= 1.11"` and the provider constraint `version = "~> 0.1"` (tflint). Every `output` has a `description` (tflint). A resource also gets `import-by-string-id.tf` and `import-by-identity.tf`, which the template includes; `make terraform-validate` (`tools/validate-examples.sh`) validates each one in a copy of the directory without the other, since Terraform refuses two imports of one address.
4. Add `templates/resources/<name>.md.tmpl` (or `templates/data-sources/`) with its `subcategory` from *Naming and grouping*, the first sentence saying what the object is and which credential it needs, and the callouts of its registration: the not-verified banner (`~> **Not verified against the live API.**`) for a `fakeOnly` type, the preview note (`-> **Public preview at Mistral.**`) for a `beta` one. `internal/provider/docs_test.go` fails on a page that contradicts the registry, and on a template or example that no registered type owns.
5. Write the tests of *Testing*, then run `make` (format, tidy, lint, complexity, tests, docs) and `pre-commit run -a`. A route a fake starts serving needs its fingerprint: `make spec-drift SPECDRIFT_FLAGS=-update`, then commit `tools/specdrift/fingerprints.txt` (CI checks the routes offline; `spec-drift.yml` compares the fingerprints with Mistral's specification every week).
6. Note in `internal/services/<area>/CLAUDE.md` what the next contributor would otherwise rediscover: API quirks, documentation pages, decisions and their reasons.

## Naming and grouping

Stable information goes in a type's name, changing information in its docs: a name changes only with a migration for every user.

- **Type names.** The object's name as the Mistral documentation calls it, prefixed `mistral_`, singular: `mistral_agent`, `mistral_library`. A list data source takes the plural (`mistral_models`), never `_list`.
- **Workspace and organization.** A workspace object keeps the bare name. An organization object takes `organization_` when the bare name would be ambiguous (`mistral_organization_spend_limit`, since a workspace spend limit exists in the Admin Panel).
- **Products.** A product prefix comes from the product's name in the documentation, not from its path segment, since Mistral renames paths (`/v1/rag/managed_indexes` in the specification, `/v1/context/search-indexes` in the SDK): `mistral_search_index`.
- **No `admin_` prefix.** The credential goes in the registry's `Audience`, the subcategory and the page, never in the name.
- **Write-only arguments** end in `_wo`, with a `_wo_version` trigger (HashiCorp's naming guide).
- **`_settings`** names a singleton that adjusts an object it neither creates nor deletes.
- **Lookups.** A singular data source looks up by `id`, or by a natural unique key when users do not know the ID (`email` for `mistral_user`), always the same attribute for a type. A reference to another object is `<object>_id`.
- **Composite import IDs** join their parts with `:` (`<workspace_id>:<user_id>`), as `ippontech/anthropic` does, and every resource also declares a resource identity.
- **Subcategories.** `Agents`, `Libraries`, `Connectors`, `Prompts and Skills`, `Service Accounts`, `Models`, then `Admin API: Workspaces and API Keys`, `Admin API: Users and Groups`, `Admin API: Billing`. Do not use `Beta`: almost everything is in preview at Mistral, and each page says so instead.
- **Data sources.** Add one only when a resource, of this provider or another, consumes what it returns: an ID or a version. No report, usage, history or audit feed, and no data source that only mirrors a resource.

## Credentials and the API

| Audience | Argument | Environment variable | Endpoints |
|---|---|---|---|
| `providerdata.Workspace` | `api_key` | `MISTRAL_API_KEY` | everything outside `/v1/admin`, in the workspace of the key |
| `providerdata.Admin` | `admin_api_key` | `MISTRAL_ADMIN_API_KEY` | `/v1/admin/*`, Enterprise plans only, access granted by Mistral on request |

- A standard key is scoped to its workspace: one aliased provider block per workspace.
- `Configure` requires no credential; a type asks for its audience's credential in its own `Configure` (`ProviderData.Credential`), so a missing key fails only the types that need it.
- A provider argument unknown at plan time is an error.
- The specification declares `Authorization: Bearer` for the Admin API key, the authentication guide `x-api-key`. The client isolates this choice in one place until a live call settles it.
- Several Admin API objects have no read by ID (workspaces, API keys, invites) and workspace members have no read route at all. Their `Read` goes through one `find` function per type that lists with the narrowest filter and compares the ID; replace that function, nothing else, when Mistral adds the route.
- No endpoint documents an idempotency key.

## Code and comments

The code is read more often than it is written, by people and by agents that have nothing but the repository.

- **Names** say what a value is (`findWorkspace`, `defaultToolChoice`), never how it was obtained (`resp2`, `tmp`, `helper`).
- **Doc comments** on every type and function, exported or not: what it does and, when it is not obvious, why. A test file opens with what it covers when its name does not say it.
- **A comment next to every choice the code cannot explain by itself**: a server-side behaviour, an API quirk, an order of calls, a deliberate omission. It cites the documentation page when there is one, and says when the behaviour is our reading of the specification rather than documented.
- **No comment that repeats the code.** Code that needs a paragraph to follow is restructured first, and commented only if it stays hard to read.
- **Area knowledge** that only the next contributor of an area needs goes in that area's `CLAUDE.md`, once, not in comments repeated across files.

## Provider coding conventions

- **Retries.** Reads, deletes and the upserts the API documents as idempotent (`PUT` of an alias or a share, `PATCH /v1/admin/workspaces/{id}/users`) are retried on 429, 5xx and connection errors. Every other write, a create first, is retried only on 429, or on a connection error before the request was sent: after a 5xx the object may exist, and a replay would create a duplicate. Never add a retry loop around a client call.
- **404.** `Read` removes the resource from the state without a diagnostic; `Delete` succeeds. Any other error is a diagnostic. Before removing, `Read` sets the resource identity from the state: during an import, terraform-plugin-framework v1.19.0 fails a `Read` that removes a resource without one ("Missing Resource Identity After Read") instead of reporting the object as missing. Each resource has a fake test that imports an ID the fake does not hold.
- **No silent adoption.** A create that finds the object already there fails and points to an `import` block. Unlike PR #332 of ippontech/terraform-provider-anthropic, it never takes over an existing object.
- **State after status changes.** An object stays in the state for as long as the API returns it, whatever its status: an accepted invitation stays (PR #330 of ippontech/terraform-provider-anthropic). The one exception is a workspace archived outside Terraform: `Read` removes it with a warning, since an archived workspace serves nothing.
- **Versioned objects.** An agent update creates a version; prompt and skill versions cannot be deleted. Fold versions into the parent resource with a computed `version`; never write a `_version` resource whose `Delete` does nothing.
- **Secrets.** A secret the user supplies is write-only (`<name>_wo` and `<name>_wo_version`). A secret the API returns once (an API key) is a computed, sensitive attribute: say on the page that the state holds it and that import cannot recover it.
- **Requires replace.** An attribute the API cannot update forces a replacement (`stringplanmodifier.RequiresReplace`). When a replacement would lose data a user cares about (a workspace), document it on the page.
- **Dates** are RFC 3339 strings.

## Testing

Unit tests need no credential and no network. Acceptance tests (`TestAcc` prefix, `resource.Test`) need `TF_ACC=1` and real keys, and create real objects.

**The standard for each resource, against the fake API** (`resource.UnitTest`, `internal/sdktest`, `internal/providertest`):

- a full lifecycle, including emptying an optional field and removing a map key, with `plancheck` on the expected action of each step and an empty plan after each apply;
- a delete behind Terraform's back (remove the object from the fake between two steps) that plans a create;
- a 5xx on create that produces exactly one call;
- the exact shape of the requests (method, path, query, body, credential);
- import, with `ImportStateVerify`;
- exact typed values in `statecheck`, not `TestCheckResourceAttrSet`.

For each data source: exact values, an empty list, two pages or more through the real `Read`, and the not-found error. For each bug fix: a test that fails without the fix. Tests call `t.Parallel()`; `make test` runs with `-race -shuffle=on`.

**The helpers.**

- **`internal/client`**: a typed call builds a `call` (method, path from `escapePath`, query, JSON `body` or multipart `form`, `audience`, and `idempotent: true` for a documented upsert) and passes it to `do`, which authenticates, retries by the policy above and decodes. Lists go through `walkCursor`, `walkPages` (first page 0 or 1, total or -1) or `walkOffset`, with `collect` or `find`. Errors are `*client.Error`; test them with `client.IsNotFound`, `IsConflict` or `StatusCode`. Client tests are internal (`package client`) and build their client with `newTestClient(srv)`, which carries the production rules with millisecond delays.
- **`internal/sdktest`**: `NewServer(t)` with `Handle`/`Respond` routes in `net/http.ServeMux` syntax (`"GET /v1/agents/{agent_id}"`), `FailNext`, `FailNextWith`, `ResetNext`, `SetMaxRequests`, and the assertions `AssertCalled`, `AssertCredential` (`sdktest.APIKey` or `AdminKey`), `AssertQueries`, `AssertJSONBodies` (compared with `JSONDiff`: an absent key, null and `""` differ). Answers: `WriteJSON`, `WriteError` (`{"detail": "..."}`), `WriteValidationError` (422). State: `NewCollection(key)` (`Create`, `Put`, `Get`, `Patch`, `Delete`, `List`, `Len`), `NewUUID`, and the page helpers `PageOf`, `OffsetOf`, `CursorOf`, `QueryInt`. Multipart: `ReadParts`, `Request.Parts`.
- **`internal/providertest`**: `ProviderFactories(srv)` (the provider pointed at the fake, with millisecond retries), `ProviderConfig()` (both dummy keys, so the environment never fills them) and `ExpectAction(addr, action)` (the planned action before the apply, an empty plan after it).
- **`internal/acctest`**: `PreCheck(t)`, `PreCheckAdmin(t)` (skips without an Admin API key), `SkipUnlessAcc(t)`, `ProtoV6ProviderFactories`, `RandomName(prefix)`.
- **Configure**: every resource and data source calls `providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)` (or `Admin`) and returns when it gives nil.

Fake-API tests live in `package <area>_test`, in files named `<name>_fake_test.go`, with one `fake_test.go` per service for its routes and handlers.

**What a fake holds**, and nothing more:

1. the transport contract: paths, methods, headers, credential, bodies, status codes, pagination;
2. the minimal state the Terraform lifecycle needs;
3. a server-side behaviour only when the provider depends on it, with the documentation page in a comment.

Everything else is a canned response set up by the test that needs it. A fake encodes our reading of the documentation: when an acceptance run contradicts it, fix the fake first.

**Acceptance tests** run against a dedicated Mistral organization, after every merge to `main` and every week, never automatically on a pull request from a fork. Until Mistral grants Enterprise access, the Admin API types stay `fakeOnly` and their pages say they were not verified against the live API.

**Native tests**: a `tests/<name>.tftest.hcl` with `mock_provider` runs offline on every pull request and must never need a credential; a test file without `mock_provider` calls the live API and runs in the acceptance workflow only. Never mix the two in one file.

**Coverage gate** (`.coverage.json`): 75 % of the statements a pull request changes, 80 % in total and 80 % in every package, three fixed thresholds with no ratchet. The exclusions are the test helpers, `main.go` and `tools/`. Code that cannot be tested is a reason to restructure it, not to exclude it; the returns after `req.Plan.Get` and `req.State.Get` are the accepted exception.

## Commands

```sh
make            # fmt, tidy-check, lint, complexity, test, install, generate
make test       # unit tests (-race -shuffle=on)
make cover      # unit tests, then the coverage gate against origin/main
make lint       # golangci-lint, root and tools/ modules
make complexity # cognitive complexity ratchet against origin/main
make generate   # terraform fmt of the examples, then docs/
make terraform-validate terraform-test-mock   # offline Terraform checks
make testacc    # TF_ACC=1: live API, creates real objects
make spec-drift # the operations the provider calls against the live specification
```

Tool versions are in `mise.toml`; CI pins the same ones.

## Working rules

- One issue per pull request, one pull request per coherent change. The title and every commit follow Conventional Commits (`feat(agents): ...`): CI checks both, and the commit-msg hook runs the same script (`tools/conventional-commits.sh`). Merges are squash or rebase only, and a branch catches up with a rebase: there are no merge commits.
- Pull requests are stacked while their base is not merged: each targets the branch it builds on.
- A sub-agent commits locally and never pushes, never comments on GitHub, never reads `.env`, and never calls the live API. The session that briefed it reviews and pushes.
- Never run `make testacc` or an acceptance test unless you were asked to: it creates real objects in a real organization.
- Never put a real key in a file, a test or a commit; gitleaks runs on every commit and every pull request. A deliberate fake in a test takes `gitleaks:allow` at the end of its line.
