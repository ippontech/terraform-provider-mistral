# internal/services/spendlimits

`mistral_organization_spend_limit`: the monthly spend limit of the organization of the Admin API key. Both endpoints are tagged `beta.admin.billing`; the type is `Admin`, `beta`, `fakeOnly`.

## API

- Reference: <https://docs.mistral.ai/api/endpoint/beta/admin/billing>. Guides: <https://docs.mistral.ai/admin/admin-api/usage-metrics> (the calls), <https://docs.mistral.ai/admin/billing-usage/usage-limits> (the concept).
- `GET /v1/admin/spend-limit` and `POST /v1/admin/spend-limit` both answer `LimitsOUT`: `limits.completion` (`no_monthly_limit`, `usage_limit`, `usage`, `monthly_limit_reached`, ...) and `limits.currency`. There is no delete.
- `POST` takes `NewUsageLimitIN`: `amount` (integer, required) and `no_monthly_limit` (default false). The guide: `amount` is "a whole number in your Organization's billing currency (500 means 500, not 500 cents)", and `no_monthly_limit: true` removes the cap.
- `usage_limit` is typed `number` in the answer, while `amount` is an `integer` in the request.
- Reaching the limit can suspend the organization's API access until the next month or until an admin raises it; a workspace limit cannot exceed the organization limit (usage-limits guide).

## Decisions

- **Schema.** `amount` (Int64, optional, at least 0) or `no_monthly_limit = true`, exactly one (`ValidateConfig`); `no_monthly_limit` defaults to `false`. `currency` is computed. The usage fields are not mapped: they change all the time and nothing consumes them.
- **Exact amounts.** `usage_limit` is decoded as a `json.Number`. A whole value, `500` or `500.0`, maps to `amount`; a fractional one is an error on the read rather than a rounded value, since the integer `amount` of the `POST` cannot produce it.
- **The amount with `no_monthly_limit`.** The `POST` requires `amount` even when the flag removes the limit. The provider sends the current limit, from `usage_limit` on create and from the state on update, never `0`: if the API ever read the amount despite the flag, `0` would suspend the organization.
- **No silent adoption, for a singleton.** The limit always exists, so the rule reads as "never overwrite a value set outside Terraform": the create reads the limit first and fails with the `import` block (`id = "organization"`) when the organization has a limit different from the plan. No limit, or the same limit, is taken as is: writing it loses nothing.
- **Destroy leaves the limit.** `Delete` calls nothing and warns. The alternative, `no_monthly_limit = true`, would lift a cost cap as a side effect of a destroy, and the limit from before Terraform is unknown, so it cannot be restored. The page says to apply `no_monthly_limit = true` first to remove the limit.
- **No retry on 5xx.** The `POST` sets a value and would be safe to replay, but the API does not document it as idempotent (CLAUDE.md, "Retries").
- **ID and identity** are always `organization`: the Admin API key selects the organization. `Read` never removes the resource (the limit always exists), so the framework's identity check on a removed import does not apply, and there is no ID the API could lack: any other import ID fails in `ImportState`.

## To check on the first live run

- What `usage_limit` holds when `no_monthly_limit` is true (the fake keeps the last amount; the provider ignores it), and whether the `POST` with the flag keeps or replaces that amount.
- Whether `usage_limit` comes back as `500` or `500.0` (both map), and whether a limit set in the Admin Panel can be fractional.
- What a new Enterprise organization has: no limit, or a default limit, which would make the first create fail with the import block.
- The currency code format (`EUR`, `USD`).

## Tests

- `fake_test.go` serves both routes over one limit, starting with no limit in euros; `seed` sets a limit outside Terraform and `limit` reads it back.
- `organization_spend_limit_resource_fake_test.go`: lifecycle with exact bodies, both imports and the destroy that leaves the limit; the refusal to overwrite and the import that follows; equal and absent limits taken as is; drift; a 5xx sent once; read errors; fractional limits; validation.
- `TestAccOrganizationSpendLimitResource` reads the organization's limit first and ends on it, so a live run leaves the organization as it found it.
