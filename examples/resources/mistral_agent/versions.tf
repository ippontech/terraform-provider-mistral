terraform {
  required_version = ">= 1.11"

  required_providers {
    mistral = {
      source  = "ippontech/mistral"
      version = "~> 0.1"
    }
  }
}
