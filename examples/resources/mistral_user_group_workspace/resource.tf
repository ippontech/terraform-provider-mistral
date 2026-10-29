resource "mistral_user_group" "developers" {
  name = "Developers"
}

# Every member of the group can use Le Chat and Studio in the workspace.
resource "mistral_user_group_workspace" "developers_production" {
  group_id     = mistral_user_group.developers.id
  workspace_id = "5c6d7e8f-9a0b-4c1d-8e2f-3a4b5c6d7e8f"
  role_names   = ["user", "dev"]
}
