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

# Alternative: externally issued bearer token (including an OIDC JWT).
# Set VYOS_AUTH_MODE=bearer and VYOS_BEARER_TOKEN; unset VYOS_API_KEY.
# The provider does not acquire or renew external tokens.
#
# Alternative: exchange an API key for short-lived VyOS tokens.
# Set VYOS_AUTH_MODE=api_key_token and VYOS_API_KEY.
# Requires a supporting rolling build with /token.
#
# Alternative: mTLS (TLS 1.2 only with current VyOS).
# Set VYOS_AUTH_MODE=mtls, VYOS_CLIENT_CERTIFICATE_FILE and VYOS_CLIENT_KEY_FILE;
# unset VYOS_API_KEY. Keep ca_certificate_file for server trust.
# Each credential's explicit attribute overrides its environment variable.
# Supplying credentials for more than one mode is an error.
