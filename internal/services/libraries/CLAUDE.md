# internal/services/libraries

`mistral_library`, `mistral_library_share`, `mistral_library_document` and the `mistral_library` data source: the libraries of the workspace of the standard key, which the `document_library` tool of an agent searches. Every endpoint is tagged `beta.libraries`, `beta.libraries.accesses` or `beta.libraries.documents` in the specification.

## API

- Reference: <https://docs.mistral.ai/api/endpoint/beta/libraries>; guide: <https://docs.mistral.ai/studio/search/libraries>.
- Libraries: `POST /v1/libraries` (201), `GET`, `PATCH` and `DELETE /v1/libraries/{library_id}`. The `PUT` is deprecated for the `PATCH`. `DELETE` answers 200 with the library today and announces 204; it deletes the documents of the library. `owner_type` (`User` or `Workspace`) "defaults to 'Workspace' for API key sessions"; only a key with the "Private and shared connectors" connector access scope can create a `User` library.
- Deprecated and left out: `chunk_size`, `emoji`, `generated_name`, `generated_description`, `explicit_*_members_count`, `org_sharing_role` (libraries); `org_id` and `user_id` (grants); `hash`, `processing_status`, `tokens_processing_*` (documents). The guide still shows `processing_status: "Running"` and `"Completed"`; the specification's `process_status` is the field read.
- Grants: `PUT /v1/libraries/{library_id}/share` "create[s] or update[s] the access level of an entity" (`level`, `share_with_uuid`, `share_with_type`); `GET .../share` lists every grant, unpaginated, as `{"data": [...]}`, with the level in `role`; `DELETE .../share` takes the entity in its body and answers 200 with the grant, announced to become 204. Only the owner shares; an owner cannot change or delete their own access; no sharing outside the organization.
- Documents: `POST .../documents` is a multipart upload with one `file` part. It answers 201 with a new document, or 200 with "a document with the same hash found in this library". `GET`, `PATCH` (`name`, `attributes`, `expires_at`) and `DELETE .../documents/{document_id}`; `GET .../status` gives `process_status`. The `PUT` is deprecated. `expires_at`: "the document will be automatically deleted after this date".
- `GET /v1/libraries` (cursor, `page_token`) and `GET .../documents` (page, `page`) are not used.

## Decisions

- **Library.** `owner_type` is a schema `Default` (`Workspace`), always sent, and forces a replacement. `nb_documents`, `total_size` and `updated_at` have no plan modifier: they follow the documents, and Terraform deletes a document before it updates its library, so a `UseStateForUnknown` would plan a count the `PATCH` no longer returns ("inconsistent result after apply"). `TestLibraryResourceCountsFollowTheDocuments` covers it.
- **Grant identity.** A grant is its library and its entity (`share_with_type`, `share_with_id`, the API's `share_with_uuid` renamed by the `<object>_id` rule); all three force a replacement, `level` updates through the same `PUT`. `Create` lists the grants and fails with the `import` block when the entity has one, as `mistral_agent_alias` does. `find` compares the entity without case, since the answer types `share_with_type` as a free string; the state keeps the configured text.
- **Document, on the model of `aws_s3_object`.** `source` and `source_hash` replace the document (`RequiresReplaceIf`), except from null (after an import, which cannot recover the file), so the first apply after an import records them in place. `name` defaults, in the plan, to the file name of `source` (`nameFromSource`), which the upload uses: removing `name` renames the document back. The upload carries only the file, so the create `PATCH`es the name, the attributes and the expiry after the processing, when they differ.
- **No silent adoption of a document.** An upload answered 200 means the library already holds that content: `Create` fails with the `import` block of that document. Without it, two resources with the same file would manage one document, and destroying one would delete it for both. The client reads the status through `call.status` (`internal/client/client.go`).
- **Processing.** The create writes the document to the state first, then polls `.../status` (1 s, doubled up to 10 s; `export_test.go` shortens it). `done`, `noop` and `self_managed` end the wait; `error` and `missing_content` fail it; every other value, the ones Mistral may add included, keeps it going. A failure or a timeout leaves the document tainted, and the next apply replaces it. `timeouts.create` bounds the whole create, by default 20 minutes: the guide's 9-page PDF took 8 seconds, OCR of a long scan and `waiting_for_capacity` take longer, and 20 minutes still fail a stuck document within one CI job.
- **Size is not compared with the file.** A hash catches every change and a size does not, and the specification does not say that `size` is the byte count of the upload: a mismatch would replace the document on every plan. `source_hash` is the documented way to follow the content.
- **Attributes are strings.** `attributes` is a `map(string)`, like the agent's `metadata`; a value set outside Terraform as another JSON type reads as its JSON text, and an update sends it back as a string. The specification accepts booleans, numbers, dates and lists: a typed attribute would need another attribute, not a change of this one.
- **Expiry.** `Read` removes a document past its `expires_at` even if the API still returns it. `ModifyPlan` refuses an `expires_at` in the past on a create or a change of it: the document would be deleted right after its upload, and uploaded again at every apply.
- **Identity before removal.** Every `Read` that removes its resource sets the identity from the state first (`removeResource`): during an import, terraform-plugin-framework v1.19.0 otherwise fails with "Missing Resource Identity After Read" instead of "Cannot import non-existent remote object". `TestImportOfAMissingObjectReportsIt` and `TestLibraryDocumentResourceTreatsAnExpiredDocumentAsGone` cover it.
- **Full bodies.** The `PATCH` of a library sends `name` and `description`, null included; the `PATCH` of a document sends `name`, `attributes` and `expires_at`, null included, so an update clears what the configuration removed.

## To check on the first live run

- That an explicit null clears `description` on the `PATCH` of a library, and `attributes` and `expires_at` on the `PATCH` of a document (our reading of `anyOf [..., null]`); `TestAccLibraryResource` checks the three.
- That an upload of the same content answers 200, as the specification says, and whether the hash covers only the content or the name too.
- The values `process_status` takes in practice: whether a text file goes to `done` or `noop`, and what an empty or image-only file gives.
- That `size` is in bytes, and that `total_size` is their sum.
- Whether the answer of `PATCH` returns the attributes as sent, with no attribute of its own, and `expires_at` at the same instant: either difference fails an apply with "inconsistent result".
- Whether a document past its `expires_at` answers 404 at once, and whether a re-upload of its content answers 200 with it.
- The case of `share_with_type` and `role` in the list of grants, and whether the owner appears in it.
- The 404 bodies of a missing library, document or grant, and the answer to a library ID that is not a UUID (probably 422).
- `TestAccLibraryShareResource` needs `MISTRAL_TEST_SHARE_USER_ID`, a user of the organization other than the owner of the key.

## Tests

- `fake_test.go` serves the twelve routes: owner defaults, counts that follow the documents, grants upserted by entity, the 201/200 upload with a content hash, a status that moves one step per read (`process` sets the steps for a file name), and expiry. `seedLibrary` and `seedDocument` store objects made outside Terraform. Its clock moves one minute per write: `at(n)` is the n-th write.
- `main_test.go` sets the poll delays to a millisecond through `export_test.go`.
- `library_resource_fake_test.go`, `library_share_resource_fake_test.go`, `library_document_resource_fake_test.go` (lifecycle, replacement, processing, adoption, import, expiry) and `library_document_errors_fake_test.go` hold the fake tests; `library_data_source_fake_test.go` the data source; `library_internal_test.go` the helpers and the wait; `library_resource_test.go` the acceptance tests.
