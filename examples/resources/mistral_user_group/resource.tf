resource "mistral_user_group" "staff" {
  name        = "Staff"
  description = "Every employee of the company"
}

# Nested inside Staff. Its members get the member role in the organization.
resource "mistral_user_group" "interns" {
  name              = "Interns"
  description       = "Summer interns"
  parent_group_ids  = [mistral_user_group.staff.id]
  organization_role = "member"
}
