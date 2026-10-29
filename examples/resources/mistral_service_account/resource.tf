# The workspace of the provider's API key.
locals {
  workspace_id = "5d3c4c7e-1f0a-4b8e-9c61-0a6f3e2b9d10"
}

data "mistral_assignable_service_account_roles" "this" {
  workspace_id = local.workspace_id
}

# The identity of a workflow worker, with the role that registers and runs
# workflows. Its API key or workload identity is added in the Admin Console.
resource "mistral_service_account" "worker" {
  workspace_id = local.workspace_id
  name         = "prod-worker"
  description  = "Runs the workflow workers of the production cluster."
  role_ids = [
    for r in data.mistral_assignable_service_account_roles.this.roles : r.id
    if r.name == "workflow_executor"
  ]
}
