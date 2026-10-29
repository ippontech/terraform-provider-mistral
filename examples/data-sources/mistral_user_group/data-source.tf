# A group that the identity provider syncs: Terraform reads it by name and
# gives its members access to a workspace, while the identity provider keeps
# its membership.
data "mistral_user_group" "engineering" {
  name = "Engineering"
}

resource "mistral_user_group_workspace" "engineering_production" {
  group_id     = data.mistral_user_group.engineering.id
  workspace_id = "5c6d7e8f-9a0b-4c1d-8e2f-3a4b5c6d7e8f"
  role_names   = ["user"]
}
