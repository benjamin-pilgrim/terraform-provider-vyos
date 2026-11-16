resource "vyos_config_batch" "policies" {
  entries = {
    ingress = {
      path  = ["firewall", "ipv4", "name", "APP-INGRESS"]
      value = { "default-action" = "drop" }
    }
    # Keep an obsolete subtree absent without dropping its ownership silently.
    retired_ingress = {
      path  = ["firewall", "ipv4", "name", "OLD-INGRESS"]
      value = null
    }
    dns_clients = {
      path    = ["service", "dns", "forwarding", "allow-from"]
      include = ["10.114.0.0/24"]
    }
  }
}
