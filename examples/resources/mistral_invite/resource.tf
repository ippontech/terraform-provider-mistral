# Grace joins as a member, with access to one workspace.
resource "mistral_invite" "grace" {
  email         = "grace@example.com"
  role_names    = ["member"]
  workspace_ids = ["5b0f8a2e-1c3d-4e5f-8a9b-0c1d2e3f4a5b"]
}

# Katherine also manages the subscriptions and invoices.
resource "mistral_invite" "katherine" {
  email      = "katherine@example.com"
  role_names = ["member", "billing_manager"]
}

output "grace_invite_status" {
  description = "pending until Grace accepts the invite, then accepted."
  value       = mistral_invite.grace.status
}
