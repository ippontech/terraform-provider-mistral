# Root module of the Terraform native tests (make terraform-test-mock and
# make terraform-test): maps the provider source the test files resolve
# against. A file with a mock_provider block runs offline on every pull
# request; a file without one calls the live API, in testacc.yml only.
terraform {
  required_providers {
    mistral = {
      source = "ippontech/mistral"
    }
  }
}
