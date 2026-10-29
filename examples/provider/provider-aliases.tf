# A standard API key acts in the workspace it was created in: manage two
# workspaces with two aliased provider blocks, one key each.
provider "mistral" {
  alias   = "production"
  api_key = var.production_api_key
}

provider "mistral" {
  alias   = "staging"
  api_key = var.staging_api_key
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
