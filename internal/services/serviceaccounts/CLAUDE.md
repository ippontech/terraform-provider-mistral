# internal/services/serviceaccounts

`mistral_service_account` and the `mistral_assignable_service_account_roles` data source: the non-human identities of a workspace and the workspace roles they can hold. Every endpoint is tagged `beta.service_accounts` in the specification.

## API

- Reference: <https://docs.mistral.ai/api/endpoint/beta/service_accounts>; guides: <https://docs.mistral.ai/admin/identity-access/service-accounts>, <https://docs.mistral.ai/admin/identity-access/roles-permissions>, <https://docs.mistral.ai/admin/identity-access/workload-identity>.
- **Credential.** The paths sit outside `/v1/admin` and the specification gives them the global `ApiKey` bearer scheme: `providerdata.Workspace`. Every operation says it "requires the Workspace admin (`workspace_admin`) role"; listing the whole organization (`GET /v1/service-accounts` without `workspace_id`) requires `organization_admin`.
- **Workspace.** `POST /v1/service-accounts` requires `workspace_id` in its body, and `GET .../assignable-roles` in its query. A standard key is scoped to its workspace, and no endpoint the provider calls tells the key's workspace (`GET /v1/whoami` and `GET /v1/users/me`, tagged `beta.users`, return it): `workspace_id` is a required argument, documented as the workspace of the key.
- `POST /v1/service-accounts` (201) takes `name`, `workspace_id`, `description` and `role_ids` (at most 14; "omitted or empty lists default to member, which grants no role permissions"). The answer is a `ServiceAccount`: `id`, `name`, `customer_id`, `organization_id`, `workspace_id`, `description`, `created_at`, `updated_at`, `deleted_at`, no roles.
- `PATCH /v1/service-accounts/{id}` takes `description` only (`UpdateServiceAccountRequest`): `name` and `workspace_id` cannot change. The guide says the name "cannot be changed after creation".
- `PUT .../{id}/roles` replaces the roles; `role_ids` is "required and non-empty; omitted or empty lists return 422". `GET .../{id}/roles` and the `PUT` answer `{"items": [{"role_id": ...}]}`. Neither is paginated.
- `GET /v1/service-accounts/assignable-roles?workspace_id=` answers `{"items": [{"uuid", "name", "description", "is_custom_role"}]}`, not paginated.
- `GET /v1/service-accounts` (offset and limit, both required, `q`, `include_deleted`, `order`) is not used.
- **Credentials of an account** (API keys, workload identity credentials) have no endpoint in the specification: the guide creates them in the Admin Console ("Add new authentication"). An API key of a service account is shown once and "scoped to the service account's workspace"; it is listed with the workspace's API keys. "Deleting a service account removes the identity and its credentials."

## Contradictions between the guide and the specification

- **Empty roles.** The guide says an empty `role_ids` on the `PUT` "clears every role"; the specification answers 422. The provider follows the specification: `role_ids` takes 1 to 14 IDs (`SizeBetween`).
- **Roles at creation.** The guide's console flow creates an account with no role and grants roles after; the specification's create takes `role_ids`. The provider sends them in the create.
- **Name.** The guide gives a slug of 2 to 63 characters, lowercased by the API; the specification a string of 1 to 255. The provider validates the guide's slug, lowercase only, since a mixed-case name would come back lowercased and plan a replacement on every run.

## Decisions

- **One call per create.** The roles go in the `POST` body, so no failure can leave an account without its roles. The answer has no roles, and `Create` writes the plan's `role_ids` rather than reading them back: if the API ignored them, the next refresh shows the difference and the next apply sends the `PUT`, where a read-back would fail the apply with an inconsistent result and taint the account.
- **Ordered updates.** `Update` sends the `PATCH` only when `description` changed and the `PUT` only when `role_ids` changed, and writes the state after the `PATCH`: when the `PUT` fails, the state keeps the new description and the next plan sends the roles only. The framework starts the update's state from the prior state, so an error before any write keeps it.
- **Retries.** The `PATCH` is not marked idempotent (the API does not document it as an upsert): it is retried on 429 only. The `PUT` is a full replace, retried like every `PUT`.
- **Role IDs** are sent sorted, so bodies do not depend on the order of the set; an empty list of roles reads as null.
- **Deleted accounts.** `ServiceAccount.deleted_at` and the list's `include_deleted` suggest a soft delete. `Read` removes the resource on a 404 of either call, and when `deleted_at` is set.
- **Name taken.** On a 409, `Create` points to an `import` block. That the API answers 409 is our reading: the specification documents no status but 422.
- **Not mapped:** `customer_id` (undocumented meaning), `deleted_at` (always null for an account in the state).
- **Data source name.** `mistral_assignable_service_account_roles` follows the reference's "List assignable service account roles" and the schema `AssignableServiceAccountRole`. `mistral_service_account_roles` would read as the roles an account holds, and the organization roles of `GET /v1/admin/roles` will be another type. `uuid` is renamed `id` and `is_custom_role` `custom`.

## To check on the first live run

- That the create assigns `role_ids` (the empty plan after the first step of `TestAccServiceAccountResource`), and that the roles answer echoes the IDs as sent.
- That a key of one workspace cannot act on another, and what it answers.
- That `{"description": null}` clears the description (third step of the acceptance test).
- The status of a name taken in the workspace, and whether the name of a deleted account is free again.
- Whether `GET` of a deleted account answers 404 or the account with `deleted_at`.
- Whether `name` of an assignable role is the API name (`workflow_executor`) or the console label; the examples filter on the API name.
- Whether an account made in the Admin Console holds no role or `member`.

## Tests

- `fake_test.go` serves the seven routes over an account collection and a role map per account; it refuses a name taken in the workspace (409) and a `PUT` without a role (422), and lists three assignable roles for `workspaceID`, none for any other workspace. `seed` stores an account made outside Terraform. Its clock moves one minute per write: `at(n)` is the n-th write.
- `service_account_resource_fake_test.go` holds the lifecycle with the exact bodies, the replacements, the deletes outside Terraform and the imports; `_errors_` the 5xx, the partial update, the taken name, the read and delete errors and the validators; `assignable_service_account_roles_data_source_fake_test.go` the data source, and the resource fed by it; `service_account_internal_test.go` the helpers.
- `TestAccServiceAccountResource` needs `MISTRAL_WORKSPACE_ID`, the workspace of `MISTRAL_API_KEY`, and skips without it.
