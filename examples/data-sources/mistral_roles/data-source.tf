data "mistral_roles" "all" {}

output "workspace_admin_role_id" {
  description = "The UUID of the workspace_admin role, for the requests that take role UUIDs."
  value       = one([for r in data.mistral_roles.all.workspace_roles : r.id if r.name == "workspace_admin"])
}
