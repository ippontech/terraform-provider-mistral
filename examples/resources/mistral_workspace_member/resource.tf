variable "ada_user_id" {
  description = "The ID of a user of the organization."
  type        = string
}

data "mistral_workspace" "ml" {
  name = "ML Research"
}

resource "mistral_workspace_member" "ada" {
  workspace_id = data.mistral_workspace.ml.id
  user_id      = var.ada_user_id
  role_names   = ["dev", "observability_viewer"]
}
