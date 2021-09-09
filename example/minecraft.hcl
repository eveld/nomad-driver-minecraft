container "minecraft" {
  image {
    name = "hashicraft/minecraft:v1.17.1-fabric"
  }
  
  network {
    name = "network.cloud"
  }

  env {
    key = "WHITELIST_ENABLED"
    value = "false"
  }

  env {
    key = "RCON_PASSWORD"
    value = "password"
  }
  
  env {
    key = "RCON_ENABLED"
    value = "true"
  }

  port {
    local = 25565
    remote = 25565
    host = 25565
  }

  port {
    local = 27015
    remote = 27015
    host = 27015
  }
}