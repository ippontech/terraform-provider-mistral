# A connector built in AI Studio, which an agent calls as a tool.
data "mistral_connector" "github" {
  name = "github"
}

resource "mistral_agent" "triage" {
  name  = "triage"
  model = "mistral-medium-latest"
  tools = [
    { type = "connector", connector_id = data.mistral_connector.github.id },
  ]
}
