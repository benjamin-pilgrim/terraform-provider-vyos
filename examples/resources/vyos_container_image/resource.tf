resource "vyos_container_image" "booter" {
  image = "ghcr.io/siderolabs/booter@sha256:f2ee41daabd2e698184c0a6dc29fe9321aabc829fd18046be54c7da923b0cea3"
}

resource "vyos_config_batch" "booter" {
  entries = {
    booter_image = {
      path  = ["container", "name", "booter", "image"]
      value = vyos_container_image.booter.image
    }
  }
}
