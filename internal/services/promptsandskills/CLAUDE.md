# internal/services/promptsandskills

`mistral_prompt` and `mistral_skill`: the versioned prompts and skills of the workspace of the standard key. Every endpoint is under `/v2` and tagged `beta.prompts` or `beta.skills`. The specification calls both "registry objects" (`RegistrySharingScope`, "a shared registry object"); the package is named after the two types instead, so that it cannot be confused with the type registry of `internal/provider/registry.go`.

## API

- References: <https://docs.mistral.ai/api/endpoint/beta/prompts>, <https://docs.mistral.ai/api/endpoint/beta/skills>; guide: <https://docs.mistral.ai/studio/conversations/chat-completion/prompt-registry>. No guide covers the skills API; the MCP page (<https://docs.mistral.ai/resources/mcp>) only says a skill has files, versions and sharing.
- Both types have the same nine routes: `POST /v2/<x>`, `GET`/`PATCH`/`DELETE /v2/<x>/{id}`, `GET`/`POST .../versions`, `GET`/`PATCH .../versions/{version}`, and a cursor-paged list (`pageToken`, `nextPageToken`).
- The bodies use lowerCamelCase (`sharingScope`, `createdAt`, `latestVersion`), declare `additionalProperties: false`, and look like a protobuf gateway: answers leave out empty fields, `Timestamp` is RFC 3339, versions are `int32` numbers.
- `GET /v2/<x>/{id}` without a query returns the latest version ("By default, `get` returns the latest version of a prompt", the guide); `version` and `alias` queries pin one. The guide passes a prompt's name as `prompt_id`.
- A create takes the first version (`definition`, `notes`, `aliases`) with the object's own fields. `POST .../versions` takes `{definition, notes, aliases}` and answers `{version, deduplicated}` only.
- `PATCH /v2/<x>/{id}` takes what no version holds: `title`, `description`, `sharingScope`, `workspaceRelation` for a prompt; `sharingScope`, `workspaceRelation` only for a skill. Neither takes `name`.
- `PATCH .../versions/{version}` ("UpdateXVersionMetadata") takes `notes` and `aliases`, an `AliasList {values}` whose absence leaves the aliases unchanged and whose empty `values` clears them. It changes no definition.
- `workspaceRelation` (`reader`, `writer`): "Defaults to `writer` ... Rejected unless sharing_scope is `workspace`." The `Prompt` and `Skill` answers do not contain it.
- A prompt definition is `{content (required), variables: [{name}]}`. A skill definition is `{description, body, assets}`, where `assets` maps a file name to `SkillAssetContent`, a oneOf of `rawContent` (bytes, base64) and `textContent`, with `isExecutable`. The files travel inline in the JSON body: there is no multipart and no archive upload for skills.
- No field of the specification takes a prompt or a skill ID: agents, conversations and chat completions have none, and the guide copies the prompt's content into a system message. The Vibe admin config's `system_prompt_id` names a prompt of the user's project, not of this API. Hence no data source.

## Decisions

- **One package for both types.** The routes, the envelope, the version protocol and the sharing settings are the same; only the definition and the prompt's display fields differ. `sharing.go`, `version.go`, `common.go` and the fake are written once.
- **Versions folded in.** `version` is the latest version, the one a read returns. `layout` lists, per type, the attributes of a version and those of the object: a change of the first creates a version (`POST .../versions`), a change of `notes` alone patches the notes of the latest version, a change of the second patches the object. `versionPlan` keeps `version` known in the plan unless the definition changes.
- **Order of an update** (`versionedUpdate.apply`): the version first, as the call that cannot be replayed and the likeliest to be refused, then the object, then a read when a version was created. A failure keeps the prior state; the refresh of the next plan reads what the earlier calls changed, and the plan holds only what is left.
- **Deduplication.** After a new version, the read must return that version as the latest. Otherwise the apply fails with the reason (an earlier version matched, or another client wrote meanwhile) instead of writing a state that contradicts the plan.
- **Aliases are not managed.** No request sends `aliases`, so an update leaves them where they are. `PATCH .../versions/{version}` sets the aliases of a version, and `GET ?alias=` resolves one to a version: an alias is a label on one version, as for agents. A later `mistral_prompt_alias` and `mistral_skill_alias` (`<id>:<alias>`, with `version`) could manage one each; since the API sets the whole list of a version, they would read the version's aliases and write them back with theirs added or removed.
- **Clearing.** `PATCH` bodies have no update mask, so a null may mean "unchanged": an absent `title`, `description` or `notes` is sent as `""`. Create and new version bodies send null, since nothing is there to keep. `variables` and `assets` are sent as `[]` and `{}`, the specification typing them as not nullable.
- **Defaults.** `sharing_scope` defaults to `private`, and the mapping reads an absent or `sharing_scope_unspecified` scope as `private`: our reading, since the specification gives no default. `workspace_relation` is planned `writer` with the workspace scope and null otherwise (`relationDefault`), refused in the configuration without the workspace scope, kept from the prior state since no answer holds it.
- **Assets** are `content` (text) or `content_base64` (binary), the Terraform names of `local_file` and `aws_s3_object`; `assetState` keeps the prior attribute and text when the API returns the same bytes in another form or base64 alphabet.
- **Name** forces a replacement. A create answered 409 points to an import block; that the API answers 409 for a taken name is assumed.

## To check on the first live run

- The ID format, the first version number (1 in the examples of the specification) and the 404 and 409 bodies.
- That `""` clears a title, a description or notes on `PATCH`, that null leaves them, and whether `variables: []` clears the variables of a new version.
- The scope of an object created without `sharingScope`, and how a read shows it.
- What `deduplicated` compares with: the latest version or every version. `TestAccPromptResource` never sends a definition twice; send an earlier one by hand.
- Whether a new version keeps the aliases of the previous one or leaves them on it.
- Whether the API returns `textContent` and `rawContent` as sent, in which base64 alphabet, and whether it derives `variables` from the template when they are left out.
- Whether an answer gives empty `variables` and `assets` as `[]` and `{}` or leaves them out: the provider sends them empty when the configuration has none, refuses them empty in the configuration, and reads both answers as null (root `CLAUDE.md`, *Empty collections*).

## Tests

- `fake_test.go` serves the twelve routes of both types through one `kind` per type: versions, the latest-version read, deduplication against the latest version (`dedupAnyVersion` for every version), the null-keeps rule of `PATCH`, the relation check, and answers without empty fields. It fails the test on a body field the request schema does not declare. Its clock moves one minute per write: `at(n)` is the n-th write.
- `prompt_resource_fake_test.go` and `skill_resource_fake_test.go` hold the lifecycles, with the exact bodies of each call, drift, recreation and import; `prompt_resource_errors_fake_test.go` and the end of the skill file the errors and validation; `promptsandskills_internal_test.go` the order of `versionedUpdate.apply`, the asset mapping and the helpers; `skill_assets_internal_test.go` that an asset unknown at plan time does not end the validation of the others (a survivor of the first mutation run). `TestAcc*` in `*_resource_test.go` are written and never run here.
