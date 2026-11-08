resource "vyos_config_batch" "policies" {
  entries = {
    ingress = {
      path  = ["firewall", "ipv4", "name", "APP-INGRESS"]
      value = { "default-action" = "drop" }
    }
    egress = {
      path  = ["firewall", "ipv4", "name", "APP-EGRESS"]
      value = { "default-action" = "accept" }
    }
  }
}
