nomad_cluster "dev" {
  depends_on = ["container.minecraft"]

  network {
    name = "network.cloud"
  }

  volume {
    source = "files/agent.hcl"
    destination = "/etc/nomad.d/agent.hcl"
  }

  volume {
    source = "files/example.nomad"
    destination = "/minecraft.hcl"
  }

  volume {
    source = "files/minecraft"
    destination = "/etc/nomad.d/data/plugins/minecraft"
  }

  volume {
    source      = "/tmp"
    destination = "/files"
  }
}

output "NOMAD_ADDR" {
  value = cluster_api("nomad_cluster.dev")
}