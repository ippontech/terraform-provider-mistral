variable "ada_user_id" {
  description = "The ID of a user of the organization."
  type        = string
}

# A workspace created in the Admin Panel, whose members Terraform manages.
data "mistral_workspace" "production" {
  name = "Production"
}

resource "mistral_workspace_member" "ada" {
  workspace_id = data.mistral_workspace.production.id
  user_id      = var.ada_user_id
  role_names   = ["dev"]
}
