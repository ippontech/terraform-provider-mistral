data "mistral_user" "ada" {
  email = "ada@example.com"
}

output "ada_user_id" {
  description = "The user ID of Ada, which workspace and user group memberships take."
  value       = data.mistral_user.ada.id
}
