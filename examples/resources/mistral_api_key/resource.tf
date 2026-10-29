variable "service_user_id" {
  description = "The ID of the user the key is created for."
  type        = string
}

data "mistral_workspace" "production" {
  name = "Production"
}

# The secret is in mistral_api_key.backend.key, and in the state: hand it to
# a secret store rather than to an output.
resource "mistral_api_key" "backend" {
  workspace_id = data.mistral_workspace.production.id
  user_id      = var.service_user_id
  name         = "prod-backend"
  expiration   = "2027-06-30"
}

output "backend_key_expires_on" {
  description = "The date the key expires, which an expiration policy can set earlier than requested."
  value       = mistral_api_key.backend.expiration_date
}
