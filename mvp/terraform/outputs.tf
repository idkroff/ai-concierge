output "cluster_id" {
  description = "Kubernetes cluster ID"
  value       = yandex_kubernetes_cluster.concierge_cluster.id
}

output "cluster_name" {
  description = "Kubernetes cluster name"
  value       = yandex_kubernetes_cluster.concierge_cluster.name
}

output "cluster_endpoint" {
  description = "Kubernetes cluster endpoint"
  value       = yandex_kubernetes_cluster.concierge_cluster.master.0.external_v4_endpoint
}

output "registry_id" {
  description = "Container Registry ID"
  value       = yandex_container_registry.concierge_registry.id
}

output "registry_url" {
  description = "Container Registry URL"
  value       = "cr.yandex/${yandex_container_registry.concierge_registry.id}"
}

output "network_id" {
  description = "VPC network ID"
  value       = yandex_vpc_network.concierge_network.id
}

output "subnet_ids" {
  description = "Subnet IDs"
  value = {
    a = yandex_vpc_subnet.concierge_subnet_a.id
    b = yandex_vpc_subnet.concierge_subnet_b.id
    c = yandex_vpc_subnet.concierge_subnet_c.id
  }
}

# Outputs для приложения
output "voice_agent_service_ip" {
  description = "Voice Agent Service ClusterIP"
  value       = try(kubernetes_service.voice_agent.spec[0].cluster_ip, "Not created yet")
}

output "voice_agent_service_external_ip" {
  description = "Voice Agent Service External IP"
  value       = try(kubernetes_service.voice_agent.status[0].load_balancer[0].ingress[0].ip, "Pending")
}

output "asterisk_service_external_ip" {
  description = "Asterisk Service External IP"
  value       = try(kubernetes_service.asterisk.status[0].load_balancer[0].ingress[0].ip, "Pending")
}

output "kubeconfig_command" {
  description = "Command to get kubeconfig"
  value       = "yc managed-kubernetes cluster get-credentials --id ${yandex_kubernetes_cluster.concierge_cluster.id} --external"
}

