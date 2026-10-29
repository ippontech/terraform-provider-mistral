# Offline: the provider is mocked, so these runs check the example module of
# mistral_model, its postcondition and its output, without a credential.
mock_provider "mistral" {
  mock_data "mistral_model" {
    defaults = {
      canonical_id = "mistral-medium-2604"
      capabilities = {
        completion_chat  = true
        function_calling = true
      }
    }
  }
}

run "resolves_the_alias" {
  command = plan

  module {
    source = "../examples/data-sources/mistral_model"
  }

  assert {
    condition     = output.medium_model == "mistral-medium-2604"
    error_message = "The output must be the canonical ID of the model."
  }
}

run "fails_without_function_calling" {
  command = plan

  module {
    source = "../examples/data-sources/mistral_model"
  }

  override_data {
    target = data.mistral_model.medium
    values = {
      canonical_id = "mistral-medium-2604"
      capabilities = {
        completion_chat  = true
        function_calling = false
      }
    }
  }

  expect_failures = [data.mistral_model.medium]
}
