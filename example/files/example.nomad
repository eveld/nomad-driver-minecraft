job "minecart" {
  datacenters = ["dc1"]
  type        = "service"

  group "minecart" {
    task "minecart" {
      driver = "minecraft"

      config {
        entity = "minecart"
        position = "308 71 175"
        // type = "chest_minecart"
        // type = "tnt_minecart"
        // type = "hopper_minecart"
      }
    }
  }
}
