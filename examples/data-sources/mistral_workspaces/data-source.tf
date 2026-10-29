data "mistral_workspaces" "all" {}

output "default_workspace_id" {
  description = "The ID of the default workspace of the organization."
  value       = one([for w in data.mistral_workspaces.all.workspaces : w.id if w.is_default])
}
