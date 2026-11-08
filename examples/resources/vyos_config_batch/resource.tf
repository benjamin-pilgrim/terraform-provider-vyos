resource "vyos_config_batch" "policies" {
  entries = {
    ingress = {
      path  = ["firewall", "ipv4", "name", "APP-INGRESS"]
      value = { "default-action" = "drop" }
    }
    dns_clients = {
      path    = ["service", "dns", "forwarding", "allow-from"]
      include = ["10.114.0.0/24"]
    }
  }
}
