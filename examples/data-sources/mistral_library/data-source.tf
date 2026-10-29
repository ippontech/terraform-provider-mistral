# A library built in AI Studio, which an agent managed here searches.
data "mistral_library" "handbook" {
  id = "0197f425-5e85-7353-b8e7-e8b974b9c613"
}

resource "mistral_agent" "hr" {
  name  = "hr-assistant"
  model = "mistral-medium-latest"
  tools = [
    { type = "document_library", library_ids = [data.mistral_library.handbook.id] },
  ]
}
