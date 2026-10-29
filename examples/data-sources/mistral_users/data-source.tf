data "mistral_users" "all" {}

output "user_ids_by_email" {
  description = "The user ID of each member of the organization, by email address."
  value       = { for u in data.mistral_users.all.users : u.email => u.id if u.email != null }
}
