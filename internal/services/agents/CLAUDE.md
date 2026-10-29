# internal/services/agents

`mistral_agent`, `mistral_agent_alias` and the `mistral_agent` data source: the agents of the workspace of the standard key. Every endpoint is tagged `beta.agents` in the specification.

## API

- Reference: <https://docs.mistral.ai/api/endpoint/beta/agents>; guide: <https://docs.mistral.ai/studio/agents/agents-api>.
- `POST /v1/agents` creates version 0. `PATCH /v1/agents/{agent_id}` "update[s] an agent attributes and create[s] a new version", which becomes the active one; `GET /v1/agents/{agent_id}` returns the active version. `PATCH /v1/agents/{agent_id}/version` switches the active version: the specification takes `version` in the query, the guide's curl sends it in the body.
- `tools` is a union on `type` (`function`, `web_search`, `web_search_premium`, `code_interpreter`, `image_generation`, `document_library`, `connector`). The specification types it as an array, not nullable: the client always sends `[]`.
- The guide's responses echo `completion_args` with every field, null or not, and `tool_choice: "auto"`; an absent `tools` comes back `[]`.
- Aliases: `PUT /v1/agents/{agent_id}/aliases?alias=&version=` is an upsert, `GET .../aliases` lists them all (no pagination, no read by name), `DELETE .../aliases?alias=`.
- `GET /v1/agents` (page-based) is deprecated for `GET /v1/agents/pages` (cursor). Neither is used yet.

## Decisions

- **Defaults instead of keep-null.** The server-side defaults (`tools = []`, `completion_args` with `tool_choice = "auto"`, a function's `description = ""` and `strict = false`, `json_schema.strict = false`) are schema `Default`s, and the state mapping writes them for a field the API leaves out. Removing an argument then plans the default, which the update sends, and an import of an agent built in AI Studio plans no change. `ippontech/terraform-provider-anthropic` keeps unset values null instead; that needs the prior state, which an import does not have.
- **Full bodies.** Create and update send every field, null included (`client.AgentRequest`, `client.CompletionArgs` have no `omitempty`): an update must clear what the configuration removed. Whether the API reads an explicit null as "clear" is our reading of the specification (every optional field is `anyOf [..., null]`); `TestAccAgentResource` checks it on its third step.
- **No empty collections.** `handoffs`, `metadata`, `stop`, `library_ids` and the `tool_configuration` lists refuse an empty value (`SizeAtLeast(1)`), and the mapping reads an empty value as null, so `[]` and null never fight.
- **JSON documents** (`function.parameters`, `json_schema.schema`) are strings; `jsonState` keeps the prior text when the API returns the same JSON value.
- **`version`** is computed with no plan modifier: unknown on every update, kept otherwise.
- **Not mapped yet:** `guardrails` (moderation settings, a deep union), `deployment_chat` (accepted by the update only), a connector tool's `authorization` (a secret, which belongs in `mistral_connector_credential`), `completion_args.prediction` (an inference-time hint). The update omits them; whether the API then keeps or clears them is undocumented.
- **Alias create checks first.** The `PUT` would take over an alias created outside Terraform, so `Create` lists the aliases and fails with the import block when the name exists.

## To check on the first live run

- That an explicit null clears a field on `PATCH`, and that an omitted field (`guardrails`, `deployment_chat`) is kept.
- That the response echoes `tool_choice: "auto"` and a function's defaults, or leaves them out: the mapping accepts both.
- The 404 body of a missing agent, alias or version.
- Whether deleting an agent deletes its aliases (the fake answers 404 on the aliases of a deleted agent).

## Tests

- `fake_test.go` serves the seven routes with versions, the defaults above and aliases; `seed` stores an agent built outside Terraform. Its clock moves one minute per write: `at(n)` is the n-th write.
- `agent_resource_fake_test.go` holds the lifecycle and the exact bodies; `_errors_`, `_drift_`, `agent_alias_resource_fake_test.go` and `agent_data_source_fake_test.go` the rest; `agent_internal_test.go` the helpers.
