# Resolve an alias to the model it points to, and fail the plan when that
# model lacks a capability the configuration relies on.
data "mistral_model" "medium" {
  id = "mistral-medium-latest"

  lifecycle {
    postcondition {
      condition     = self.capabilities.function_calling
      error_message = "The agents of this configuration call tools: their model must support function calling."
    }
  }
}

output "medium_model" {
  description = "The model the mistral-medium-latest alias resolves to."
  value       = data.mistral_model.medium.canonical_id
}
