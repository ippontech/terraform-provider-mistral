resource "mistral_connector" "deepwiki" {
  name        = "deepwiki"
  title       = "DeepWiki"
  description = "Explores the documentation of public code repositories."
  server      = "https://mcp.deepwiki.com/mcp"
  visibility  = "shared_workspace"
}

# An agent that calls the tools of the connector.
resource "mistral_agent" "support" {
  name  = "support"
  model = "mistral-medium-latest"
  tools = [
    { type = "connector", connector_id = mistral_connector.deepwiki.id },
  ]
}
