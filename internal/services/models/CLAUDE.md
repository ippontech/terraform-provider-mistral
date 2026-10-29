# internal/services/models

`mistral_model` and `mistral_models`: the models the workspace of the standard key can use, which every type that names a model (`mistral_agent` first) consumes. Read only: there is no model resource.

## API

- `GET /v1/models` lists every model in one answer, `{"object": "list", "data": [...]}`, with no pagination. `GET /v1/models/{model_id}` takes a model ID or an alias. Reference: <https://docs.mistral.ai/api/endpoint/models>.
- A card is a `BaseModelCard` (`type: base`) or an `FTModelCard` (`type: fine-tuned`), which adds `job`, `root` and `archived`. Both are decoded into `client.Model`.
- Renamed on the Terraform side: `created` (Unix seconds) is `created_at` (RFC 3339), `root` is `base_model`, `default_model_temperature` is `default_temperature`. Not mapped: `object`, and `job`, since fine-tuning is deprecated at Mistral.
- A capability Mistral adds needs a field in `client.ModelCapabilities` and a key in `capabilityAttrTypes`; until then the decoder ignores it.

## Decisions

- No resource for fine-tuned models (the API updates, archives, unarchives and deletes them): fine-tuning is deprecated, and Terraform does not create these models.
- `mistral_model.id` keeps the value the user set, alias included; `canonical_id` is the ID the API answered with. Writing the canonical ID into `id` would make the configuration and the state disagree on every plan where an alias is used.
- A deprecated model reads with a warning, not an error: a configuration that pins a model must keep planning until the deprecation date.
- `modelValues` builds every value with the `Must` constructors from its own type, so it returns no diagnostics: a mismatch with `modelAttrTypes` is a programming error that the fake tests catch.

## To check on the first live run

- Whether `GET /v1/models` lists an alias as an entry of its own (with its own `id`) or only in the `aliases` of its model. The fake lists each model once; `mistral_models` documents neither behaviour.
- Whether a fine-tuned model of another workspace of the organization appears.
- That `mistral-small-latest`, which `TestAccModelDataSource` reads, still exists.

## Tests

- `fake_test.go` serves both routes over the cards a test passes; `GET /v1/models/{model_id}` matches the ID or an alias and answers 404 otherwise.
- `tests/model_data_source.tftest.hcl` runs the example module of `mistral_model` against `mock_provider`. `mistral_models` has no native test: Terraform 1.17.1 rejects a mocked value for a list nested attribute ("incompatible types; expected object type, found tuple"), with `mock_data` defaults and with `override_data` alike.
