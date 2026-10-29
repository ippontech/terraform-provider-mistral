resource "mistral_user_group" "interns" {
  name = "Interns"
}

# One resource per user: each membership is added and removed on its own.
resource "mistral_user_group_member" "interns" {
  for_each = toset([
    "8d1e2f3a-4b5c-4d6e-8f7a-9b0c1d2e3f4a",
    "9e2f3a4b-5c6d-4e7f-9a8b-0c1d2e3f4a5b",
  ])

  group_id = mistral_user_group.interns.id
  user_id  = each.value
}
