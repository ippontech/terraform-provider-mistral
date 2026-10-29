# A standard API key acts in the workspace it was created in: manage two
# workspaces with two aliased provider blocks, one key each, and pass the
# alias to each resource and data source of that workspace.
provider "mistral" {
  alias   = "production"
  api_key = var.production_api_key
}

provider "mistral" {
  alias   = "staging"
  api_key = var.staging_api_key
}

data "mistral_models" "production" {
  provider = mistral.production
}

data "mistral_models" "staging" {
  provider = mistral.staging
}

output "models_only_in_production" {
  description = "The models the production workspace can use and the staging workspace cannot."
  value       = setsubtract(data.mistral_models.production.models[*].id, data.mistral_models.staging.models[*].id)
}

variable "production_api_key" {
  type      = string
  sensitive = true
  ephemeral = true
}

variable "staging_api_key" {
  type      = string
  sensitive = true
  ephemeral = true
}
