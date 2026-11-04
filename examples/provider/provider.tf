terraform {
  required_providers {
    vyos = {
      source = "TGNThump/vyos"
    }
  }
}

provider "vyos" {
  endpoint            = "https://router.example.net"
  ca_certificate_file = "/path/to/router-ca.pem"
  # Supply VYOS_API_KEY through the execution environment.
}
