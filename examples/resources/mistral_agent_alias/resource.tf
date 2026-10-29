resource "mistral_agent" "support" {
  name  = "support"
  model = "mistral-medium-latest"
}

# "staging" follows every new version of the agent.
resource "mistral_agent_alias" "staging" {
  agent_id = mistral_agent.support.id
  alias    = "staging"
  version  = mistral_agent.support.version
}

# "production" stays on a reviewed version until it is promoted here.
resource "mistral_agent_alias" "production" {
  agent_id = mistral_agent.support.id
  alias    = "production"
  version  = 3
}
