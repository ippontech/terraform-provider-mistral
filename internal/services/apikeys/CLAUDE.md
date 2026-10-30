# internal/services/apikeys

`mistral_api_key`: the workspace API keys an organization admin creates for a user, through the Admin API (`providerdata.Admin`). Every endpoint is tagged `beta.admin.api-keys`.

## API

- Guides: <https://docs.mistral.ai/admin/identity-access/api-keys>, <https://docs.mistral.ai/admin/identity-access/api-key-policy>, <https://docs.mistral.ai/getting-started/quickstarts/admin/manage-workspaces>. Reference: <https://docs.mistral.ai/api/endpoint/beta/admin/api-keys>.
- `POST /v1/admin/api-keys` takes `workspace_uuid` and `user_id` (required), `name` and `expiration` (a date), and answers `APIKeyOUT` with the secret `key`, "Only returned at creation time".
- `DELETE /v1/admin/api-keys/{key_id}` answers 200 with a `detail`. There is no update: "After you create a key, you cannot change its Workspace, Connector access scope, or expiration date."
- No read by ID. `GET /v1/admin/api-keys` pages by `limit` (default 100) and `offset`, with no total and no cursor, and filters with `name`: "name substring or the last 4 characters of the key", case- and accent-insensitive. `APIKeyExtendedOUT` gives `workspace_id`, but not the user the key was created for (`created_by` is the creator).
- Expiration policy: a key asked without a date, or beyond the maximum, is created "with the latest expiration date allowed by the policy". Policies are set in the Admin Panel only. Expired keys stay in the list.

## Decisions

- **`findAPIKey`** walks the list with `name=<name>` when the key has a name, then the whole list (a key imported, or renamed outside Terraform, escapes the filter), and compares `key_id`. The list ends on a page shorter than the limit (`walkOffset`). Replace this function when Mistral adds a read by ID.
- **Expiration in two attributes.** `expiration` keeps the configured date and is never read back; `expiration_date` is computed from the answer. A date shortened by a policy then never plans a replacement.
- **`replaceUnlessImported`** on `user_id`, **`replaceExpiration`** on `expiration`: neither is read back, so both are null after an import. The first apply after an import records the configured values in place (`Update`, which calls nothing); any other change replaces the key, an expiration added to a key created without one included. The import marker is a null `user_id` in the state (`imported`), not a null value: `user_id` is required, so only an import leaves it null, while a key created without an expiration has a null `expiration` too. One more in-place case: an expiration added to a key whose `expiration_date` is already on or before it (a policy date, say) is recorded, since a new key would expire no earlier; a later or null `expiration_date`, one in another format than `YYYY-MM-DD`, or a date unknown at plan time replaces the key. A change of a configured expiration always replaces: moving it later renews the key. `workspace_id` and `name` read back and replace on change.
- **The secret** is a computed, sensitive `key`, set from the create only and kept by `Read`; an import leaves it null. `hidden_key` is the masked value.
- **No `create_before_destroy` by default**: whether two keys of the same name may coexist is not documented; the page tells users they can set it.
- Not mapped: `actions`, `can_delete`, `product`, `last_used` (it changes with every call), `created_by`.

## To check on the first live run

- That the list returns keys created through the API with their `workspace_id`, and whether it includes expired keys.
- Whether the `name` filter is a substring match, as documented.
- Whether an unnamed key reads back as null or `""` (both map to null).
- The status and body of a delete of a missing key (the provider takes a 404 as done).
- The format of `created_at` and `expiration_date`.

## Tests

- `fake_test.go` serves the three routes; `setPolicy(days)` sets an expiration policy, whose start is the fake's `today`. Secrets are `fake-secret-<n>`, in creation order.
- `api_key_resource_fake_test.go` holds the fake tests; `api_key_resource_test.go` the acceptance test, which needs `MISTRAL_ACC_WORKSPACE_ID` and `MISTRAL_ACC_MEMBER_USER_ID`.
- An import by identity plans an update (`user_id`), so that step sets `ExpectNonEmptyPlan` and checks the update.
- `Read` sets the identity before it removes a missing key: during an import, terraform-plugin-framework v1.19.0 fails such a Read with "Missing Resource Identity After Read". `TestAPIKeyResourceImportOfAMissingKey` pins it.
