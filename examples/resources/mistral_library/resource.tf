resource "mistral_library" "support" {
  name        = "support-docs"
  description = "Product documentation the support agent answers from."
}

# The agent searches the library with its document_library tool.
resource "mistral_agent" "support" {
  name  = "support"
  model = "mistral-medium-latest"
  tools = [
    { type = "document_library", library_ids = [mistral_library.support.id] },
  ]
}
