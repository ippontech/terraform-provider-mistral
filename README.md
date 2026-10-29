# Terraform Provider for Mistral AI

A Terraform and OpenTofu provider for [Mistral AI](https://mistral.ai):

- in a workspace, with a standard API key: agents, libraries and their documents, connectors, prompts, skills and service accounts;
- in an organization, with an Admin API key: workspaces, API keys, invites, user groups and the spend limit. The Admin API is available on Enterprise plans only.

Nothing is released yet. The provider will be published as `ippontech/mistral` on the Terraform Registry and the OpenTofu Registry.

## Usage

```terraform
terraform {
  required_providers {
    mistral = {
      source = "ippontech/mistral"
    }
  }
}

provider "mistral" {}
```

The provider reads `MISTRAL_API_KEY` and `MISTRAL_ADMIN_API_KEY` from the environment. See [the provider documentation](docs/index.md) for the arguments and the workspace model.

## Requirements

- Terraform or OpenTofu 1.11 or later
- To build: Go (the version in `go.mod`)

## Development

```sh
make build       # compile
make test        # unit tests, offline, against a fake of the Mistral API
make generate    # format the examples and regenerate docs/ from the schema, templates/ and examples/
make testacc     # acceptance tests against the live API (needs MISTRAL_API_KEY, and creates real objects)
```

`docs/` is generated: edit `templates/` and `examples/`, then run `make generate`.

## License

[MPL-2.0](LICENSE)
