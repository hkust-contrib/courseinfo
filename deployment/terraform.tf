terraform {
  required_version = ">= 1.9.8, < 2.0.0"

  # Configure a courseinfo-only endpoint through TF_HTTP_*; never share concierge state.
  backend "http" {}

  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "= 5.23.0"
    }
  }
}
