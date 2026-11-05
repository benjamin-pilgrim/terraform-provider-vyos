resource "vyos_config_batch" "policies" {
  entries = {
    ingress = {
      path  = ["firewall", "ipv4", "name", "APP-INGRESS"]
      value = jsonencode({ "default-action" = "drop" })
    }
    egress = {
      path  = ["firewall", "ipv4", "name", "APP-EGRESS"]
      value = jsonencode({ "default-action" = "accept" })
    }
  }
}
