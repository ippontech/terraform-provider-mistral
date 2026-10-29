resource "mistral_prompt" "coding_assistant" {
  name        = "coding-assistant"
  title       = "Coding assistant"
  description = "System prompt of the code review bot."
  content     = "You are a coding assistant. You specialise in {{language}}."
  variables   = [{ name = "language" }]
  notes       = "Specialise by language"

  # Members of the workspace can use the prompt in Studio, not change it.
  sharing_scope      = "workspace"
  workspace_relation = "reader"
}

output "coding_assistant_version" {
  description = "The version an application pins with ?version= when it fetches the prompt."
  value       = mistral_prompt.coding_assistant.version
}
