resource "mistral_library" "support" {
  name = "support-docs"
}

# Every Markdown and PDF file of docs/, kept in git next to the configuration.
# source_hash replaces a document whose file changed.
resource "mistral_library_document" "docs" {
  for_each = fileset("${path.module}/docs", "*.{md,pdf}")

  library_id  = mistral_library.support.id
  source      = "${path.module}/docs/${each.value}"
  source_hash = filesha256("${path.module}/docs/${each.value}")
  attributes  = { team = "support" }

  timeouts {
    create = "30m"
  }
}
