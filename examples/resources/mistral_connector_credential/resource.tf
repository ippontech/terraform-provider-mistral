variable "orders_api_key" {
  description = "The API key of the orders MCP server."
  type        = string
  sensitive   = true
  ephemeral   = true
}

resource "mistral_connector" "orders" {
  name        = "orders"
  description = "Looks up orders and their status."
  server      = "https://orders.example.com/mcp"
  visibility  = "shared_workspace"
}

resource "mistral_connector_credential" "orders" {
  connector_id = mistral_connector.orders.id
  scope        = "workspace"
  name         = "production"
  title        = "Production API key"

  credentials_wo = jsonencode({
    headers = { "X-Api-Key" = var.orders_api_key }
  })
  # Raise it after a rotation of the key, to send the new one.
  credentials_wo_version = 1
}
