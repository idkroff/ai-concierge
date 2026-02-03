terraform {
  required_version = ">= 1.0"

  required_providers {
    yandex = {
      source  = "yandex-cloud/yandex"
      version = "~> 0.100"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.23"
    }
  }

  # Опционально: хранение состояния в Object Storage
  # backend "s3" {
  #   endpoint   = "storage.yandexcloud.net"
  #   bucket     = "terraform-state"
  #   region     = "ru-central1"
  #   key        = "ai-concierge/terraform.tfstate"
  #   access_key = "YOUR_ACCESS_KEY"
  #   secret_key = "YOUR_SECRET_KEY"
  # }
}

provider "yandex" {
  token     = var.yandex_token
  cloud_id  = var.yandex_cloud_id
  folder_id = var.yandex_folder_id
  zone      = var.yandex_zone
}

# Создание VPC сети
resource "yandex_vpc_network" "concierge_network" {
  name = "concierge-network"
}

# Создание подсетей в разных зонах
resource "yandex_vpc_subnet" "concierge_subnet_a" {
  name           = "concierge-subnet-a"
  zone           = "ru-central1-a"
  network_id     = yandex_vpc_network.concierge_network.id
  v4_cidr_blocks = ["10.1.0.0/24"]
}

resource "yandex_vpc_subnet" "concierge_subnet_b" {
  name           = "concierge-subnet-b"
  zone           = "ru-central1-b"
  network_id     = yandex_vpc_network.concierge_network.id
  v4_cidr_blocks = ["10.1.1.0/24"]
}

resource "yandex_vpc_subnet" "concierge_subnet_c" {
  name           = "concierge-subnet-c"
  zone           = "ru-central1-d"
  network_id     = yandex_vpc_network.concierge_network.id
  v4_cidr_blocks = ["10.1.2.0/24"]
}

# Сервисный аккаунт для Kubernetes кластера
resource "yandex_iam_service_account" "k8s_sa" {
  name        = "k8s-service-account"
  description = "Service account for Kubernetes cluster"
}

# Роли для сервисного аккаунта
resource "yandex_resourcemanager_folder_iam_member" "k8s_sa_roles" {
  folder_id = var.yandex_folder_id
  role      = "editor"
  member    = "serviceAccount:${yandex_iam_service_account.k8s_sa.id}"
}

# Сервисный аккаунт для узлов кластера
resource "yandex_iam_service_account" "node_sa" {
  name        = "k8s-node-service-account"
  description = "Service account for Kubernetes node group"
}

# Роли для узлов
resource "yandex_resourcemanager_folder_iam_member" "node_sa_roles" {
  folder_id = var.yandex_folder_id
  role      = "container-registry.images.puller"
  member    = "serviceAccount:${yandex_iam_service_account.node_sa.id}"
}

# Container Registry
resource "yandex_container_registry" "concierge_registry" {
  name = "concierge-registry"
}

# Kubernetes кластер
resource "yandex_kubernetes_cluster" "concierge_cluster" {
  name        = "concierge-cluster"
  description = "Kubernetes cluster for AI Concierge"

  network_id = yandex_vpc_network.concierge_network.id

  master {
    zonal {
      zone      = yandex_vpc_subnet.concierge_subnet_a.zone
      subnet_id = yandex_vpc_subnet.concierge_subnet_a.id
    }

    public_ip = true
    version   = var.cluster_version

    maintenance_policy {
      auto_upgrade = true
      maintenance_window {
        day        = "monday"
        start_time = "03:00"
        duration   = "3h"
      }
    }
  }

  service_account_id      = yandex_iam_service_account.k8s_sa.id
  node_service_account_id = yandex_iam_service_account.node_sa.id

  release_channel = "REGULAR"

  depends_on = [
    yandex_resourcemanager_folder_iam_member.k8s_sa_roles,
    yandex_resourcemanager_folder_iam_member.node_sa_roles
  ]
}

# Группа узлов Kubernetes
resource "yandex_kubernetes_node_group" "concierge_nodes" {
  cluster_id = yandex_kubernetes_cluster.concierge_cluster.id
  name       = "concierge-nodes"
  version    = var.cluster_version

  instance_template {
    platform_id = "standard-v2"

    resources {
      memory        = var.node_memory
      cores         = var.node_cores
      core_fraction = 100
    }

    boot_disk {
      type = "network-ssd"
      size = var.node_disk_size
    }

    network_interface {
      nat        = true
      subnet_ids = [
        yandex_vpc_subnet.concierge_subnet_a.id,
        yandex_vpc_subnet.concierge_subnet_b.id
      ]
    }

    container_runtime {
      type = "containerd"
    }

    scheduling_policy {
      preemptible = true
    }
  }

  scale_policy {
    fixed_scale {
      size = var.node_count
    }
  }

  allocation_policy {
    location {
      zone = "ru-central1-a"
    }
    location {
      zone = "ru-central1-b"
    }
  }

  maintenance_policy {
    auto_upgrade = true
    auto_repair  = true
    maintenance_window {
      day        = "sunday"
      start_time = "03:00"
      duration   = "3h"
    }
  }
}

