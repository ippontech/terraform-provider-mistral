data "mistral_assignable_service_account_roles" "this" {
  workspace_id = "5d3c4c7e-1f0a-4b8e-9c61-0a6f3e2b9d10"
}

output "assignable_role_ids" {
  description = "The IDs of the roles a service account of the workspace can hold, grouped by role name."
  value       = { for r in data.mistral_assignable_service_account_roles.this.roles : r.name => r.id... }
}
