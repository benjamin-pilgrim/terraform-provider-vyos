resource "vyos_config" "test" {
  path = "firewall ipv4 name TUNNEL-INGRESS"
  value = {
    default-action = "drop"
    rule = {
      "10" = {
        action   = "accept"
        protocol = "tcp"
        destination = {
          port = "443"
        }
      }
    }
  }
}
