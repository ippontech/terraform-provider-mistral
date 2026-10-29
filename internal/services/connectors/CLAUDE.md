# internal/services/connectors

`mistral_connector`, `mistral_connector_credential` and the `mistral_connector` data source: the MCP and HTTP connectors of the workspace of the standard key, which a `connector` tool of a `mistral_agent` calls, and the credentials Mistral sends to their servers. Every endpoint is tagged `beta.connectors`.

## API

- Reference: <https://docs.mistral.ai/api/endpoint/beta/connectors>; guides: <https://docs.mistral.ai/studio/connectors>, <https://docs.mistral.ai/studio/connectors/management>, and for the Admin Panel side <https://docs.mistral.ai/admin/identity-access/connectors>.
- `POST /v1/connectors` (201) takes `CreateConnectorRequest` or `CreateHTTPConnectorRequest`, a union on `protocol` (`mcp`, the default, or `http`). `visibility` defaults to `private`. `GET /v1/connectors/{connector_id_or_name}` takes the ID or the name ("Connector names are unique within a Workspace"). `PATCH` and `DELETE /v1/connectors/{connector_id}` take the ID only; the delete answers 200 with a `MessageResponse`.
- Both update schemas (`ConnectorMCPPublicUpdate`, `UpdateHTTPConnectorRequest`) refuse unknown fields (`additionalProperties: false`). The HTTP one has no `system_prompt`, and neither has `visibility`, `headers` or `global_headers`. The management guide lists `headers` and `auth_data` among the updatable fields: the specification does not, and wins.
- Visibility: `PUT`/`DELETE /v1/connectors/{connector_id}/share` (workspace) and `.../organization/share` take no body and no target: they toggle the connector between private and shared with the workspace or the organization of the key. A share "transfers ownership of a private user-owned connector"; an unshare reverts it to private and removes the "workspace-scoped [or organization-scoped] connections and other members' connections", keeping the creator's. Only the creator can share with the workspace; `shared_org` connectors are for organization admins (management guide). A response may answer `shared_global` for connectors Mistral publishes.
- API key "connector access scope" (<https://docs.mistral.ai/admin/identity-access/api-keys>): `Shared connectors only` (the default) or `Private and shared connectors`. A key of the first kind does not see private connectors.
- Credentials: `POST` and `PATCH /v1/connectors/{connector_id_or_name}/{consumer_scope}/credentials` (`CredentialsCreateOrUpdate`: `name`, `title`, `is_default`, `credentials`), `DELETE .../{consumer_scope}/credentials/{credentials_name}`, and the lists `GET .../{workspace,organization,user}/credentials`, not paginated, which answer `AuthenticationConfiguration` entries without secrets and the scope as a `ConsumerType` (`org` for the organization). There is no read by name. The `PATCH` is documented as "Create or update consumer credentials". `credentials` is `ConnectionCredentialsInput`: `headers` (an object of strings), `bearer_token`, or `oauth` (`OAuth2ClientCredentialsInput`), and refuses other fields. The specification spells out the `is_default` rules of a create and of an update.

## Decisions

- **Visibility is an attribute** of `mistral_connector`, not a resource: the share calls are toggles without a target. The create sends it in its body; should the response answer another one, the share calls apply the planned one. An update sends the `PATCH`, then the share calls. Since a share takes a private connector, a move between `shared_workspace` and `shared_org` unshares first (`moveVisibility`). The share calls answer a message, so the connector is read back after them. When a share call fails, the state keeps what the create or the `PATCH` answered: a created connector stays in the state, tainted.
- **`visibility` is required**, against the convention of server-side defaults: the default `private` is invisible to a key with the default access scope, so a create with it would vanish at the next refresh. The page says so.
- **Full bodies.** The create and the `PATCH` send every managed field, null included (`client.ConnectorUpdate.MarshalJSON` adds `system_prompt` for an MCP connector only). The management guide says to "only include the fields you want to change"; whether an explicit null clears a field is our reading of the specification (`anyOf [..., null]`). `TestAccConnectorResource` checks it on its third step.
- **Replacements.** `protocol` cannot change in place. `system_prompt` replaces an `http` connector only (`replaceIfHTTP`).
- **Credential scopes**: `workspace` and `organization`. `user` is refused: it binds the credentials to the person who owns the key, which a shared configuration rarely wants.
- **`credentials_wo` is a JSON string**, not a map of strings: `oauth` is a nested object. A validator checks the three fields and their types. The `POST` sends it; the `PATCH` sends it only when `credentials_wo_version` changes, and leaves it out otherwise (the specification leaves an omitted field unchanged, at least for `is_default`). The state never holds it (`TestConnectorCredentialResourceLifecycle` scans the state for the dummy secrets).
- **Credentials create checks first.** `Create` lists the scope and fails with the import block when the name exists. Then the `POST`, which is not retried after a 5xx; the `PATCH`, a documented upsert, is (`idempotent: true`). Both answer a message: `readBack` lists the scope to write the state.
- **`is_default` is optional and computed**, with `UseStateForUnknown`. The body carries it only when the configuration sets it, so the API applies its own rules otherwise. A promotion of other credentials demotes these without a plan.
- **The data source looks up by `name`**, the natural key of *Lookups*: users choose the name in AI Studio, not the UUID. Since the `GET` also answers an ID, a connector whose name differs from the one asked for is "not found".
- **Not mapped yet:** `auth_methods` (a union with OAuth2 `auth_data` secrets and server metadata; the default the API answers for a connector created without it is undocumented), `headers` and `global_headers` (secrets, create-only), `oauth2_server_metadata` and `oauth2_server_metadata_url`. The tools of a connector and `auth_url` are outside Terraform's model.
- **Out of scope:** activation and deactivation (`POST .../{consumer_scope}/activate` and `deactivate`): no read endpoint, so no drift detection (`blocked_by` might reflect it, unverified). `authentication_methods`, the tools listing and the deprecated tool call return data no resource consumes. `DELETE .../user/credentials` belongs to the `user` scope.

## To check on the first live run

- That the create honours `visibility` in its body, so no share call follows.
- That an explicit null clears `title`, `icon_url` and `system_prompt` on `PATCH`, and `title` of credentials.
- That a `PATCH` of credentials without `credentials` keeps the secret.
- The status and body of a share call on a connector that is not private, and of a duplicate connector or credentials name.
- How the API sets `authentication_type` of credentials (the fake reads it from the fields of `credentials`), and whether a connector created without `auth_methods` accepts a `bearer_token`.
- Whether a key with the `Shared connectors only` scope can create a private connector, and the 404 bodies of a missing connector or credentials.
- Whether the `connector_id` of an agent's `connector` tool takes the UUID, as the data source documents.

## Tests

- `fake_test.go` serves the twelve routes: the create defaults, the update schemas, the share rules (a share takes a private connector, an unshare removes the credentials of its scope), and credentials with the `is_default` rules. `createAnswersPrivate` makes a create ignore its visibility. `seed` and `seedCredentials` store objects built outside Terraform. Its clock moves one minute per connector write: `at(n)` is the n-th.
- `connector_resource_fake_test.go` holds the lifecycle, the order of the writes (`assertWrites`), drift and import; `_errors_` the 5xx, share and read failures and the validators.
- `connector_credential_resource_fake_test.go` holds the lifecycle with the secret rotation, the promotion of the default, the refusal to take over, and the deletions outside Terraform; `_errors_` the retries, the errors, canned servers for a list that does not return created credentials, and the validators.
- `connector_data_source_fake_test.go`, `connectors_internal_test.go` (helpers), and `TestAccConnectorResource` (live, against the DeepWiki MCP server of the guide).
