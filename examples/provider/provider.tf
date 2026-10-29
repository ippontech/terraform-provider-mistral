terraform {
  required_version = ">= 1.11"

  required_providers {
    mistral = {
      source  = "ippontech/mistral"
      version = "~> 0.1"
    }
  }
}

# Credentials come from the environment: MISTRAL_API_KEY for the workspace
# resources, MISTRAL_ADMIN_API_KEY for the Admin API ones.
provider "mistral" {}
