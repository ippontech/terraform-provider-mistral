data "mistral_models" "all" {}

output "vision_models" {
  description = "The models of the workspace that accept images and have no planned deprecation."
  value = [
    for m in data.mistral_models.all.models : m.id
    if m.capabilities.vision && m.deprecation == null
  ]
}
