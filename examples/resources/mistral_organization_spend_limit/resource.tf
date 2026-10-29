# 5000 in the billing currency of the organization, not cents.
resource "mistral_organization_spend_limit" "this" {
  amount = 5000
}
