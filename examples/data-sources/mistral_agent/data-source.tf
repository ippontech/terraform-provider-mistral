# An agent built in AI Studio, whose versions Terraform names.
data "mistral_agent" "studio" {
  id = "ag_0684fe0e0b98773e8000323fc71a3986"
}

resource "mistral_agent_alias" "production" {
  agent_id = data.mistral_agent.studio.id
  alias    = "production"
  version  = data.mistral_agent.studio.version
}
