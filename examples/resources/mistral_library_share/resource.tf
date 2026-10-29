resource "mistral_library" "support" {
  name = "support-docs"
}

# The support workspace can search the library.
resource "mistral_library_share" "support_workspace" {
  library_id      = mistral_library.support.id
  share_with_type = "Workspace"
  share_with_id   = "3f2e1d0c-9b8a-4f7e-8d6c-5b4a3f2e1d0c"
  level           = "Viewer"
}

# A technical writer can also change it in AI Studio.
resource "mistral_library_share" "writer" {
  library_id      = mistral_library.support.id
  share_with_type = "User"
  share_with_id   = "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
  level           = "Editor"
}
