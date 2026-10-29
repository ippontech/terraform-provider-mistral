variable "admin_user_id" {
  description = "The ID of the user who administers the new workspace."
  type        = string
}

resource "mistral_workspace" "ml" {
  name          = "ML Research"
  description   = "Models and experiments of the research team"
  icon          = "🧪"
  admin_user_id = var.admin_user_id

  # Destroying or replacing the workspace archives it, with everything it
  # holds, and an archived workspace cannot be restored.
  lifecycle {
    prevent_destroy = true
  }
}
