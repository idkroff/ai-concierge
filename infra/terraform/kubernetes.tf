# Настройка Kubernetes провайдера
provider "kubernetes" {
  host                   = yandex_kubernetes_cluster.concierge_cluster.master.0.external_v4_endpoint
  cluster_ca_certificate = yandex_kubernetes_cluster.concierge_cluster.master.0.cluster_ca_certificate
  token                  = data.yandex_client_config.client.iam_token
}

# Получение токена для доступа к Kubernetes
data "yandex_client_config" "client" {}

# Namespace для приложения
resource "kubernetes_namespace" "concierge" {
  metadata {
    name = "concierge"
    labels = {
      name = "concierge"
    }
  }
}

# ConfigMap для voice-agent
resource "kubernetes_config_map" "concierge_config" {
  metadata {
    name      = "concierge-config"
    namespace = kubernetes_namespace.concierge.metadata[0].name
  }

  data = {
    HTTP_PORT         = "8080"
    AMI_HOST          = "asterisk-service"
    AMI_PORT          = "5038"
    AUDIO_SOCKET_PORT = ":9092"
  }
}

# ConfigMap для Asterisk
resource "kubernetes_config_map" "asterisk_config" {
  metadata {
    name      = "asterisk-config"
    namespace = kubernetes_namespace.concierge.metadata[0].name
  }

  data = {
    "extensions.conf" = <<-EOT
      [general]
      static=yes
      writeprotect=no

      [default]
      exten => s,1,NoOp(Starting outbound call)
       same => n,Answer()
       same => n,Wait(1)
       same => n,Playback(custom/output)
       same => n,Wait(1)
       same => n,Hangup()

      exten => _X.,1,NoOp(Call to $${EXTEN})
       same => n,Answer()
       same => n,Wait(1)
       same => n,Playback(custom/$${AUDIOFILE})
       same => n,Wait(1)
       same => n,Hangup()

      [audiosocket]
      exten => s,1,NoOp(AudioSocket for session $${SESSION_ID})
       same => n,AudioSocket($${SESSION_ID},voice-agent-service:9092)
       same => n,Hangup()
    EOT
  }
}

# Переменные окружения приложений (без Secret — обычный ConfigMap)
resource "kubernetes_config_map" "concierge_env" {
  metadata {
    name      = "concierge-env"
    namespace = kubernetes_namespace.concierge.metadata[0].name
  }

  data = {
    API_KEY        = var.realtime_api_key
    FOLDER         = var.realtime_folder_id
    SIP_USER       = var.sip_user
    SIP_PASS       = var.sip_password
    AMI_USER       = var.ami_user
    AMI_PASSWORD   = var.ami_password
    PJSIP_ENDPOINT = var.pjsip_endpoint
    INSTRUCTIONS   = var.voice_agent_instructions
  }
}

# Deployment для Asterisk
resource "kubernetes_deployment" "asterisk" {
  wait_for_rollout = false

  metadata {
    name      = "asterisk"
    namespace = kubernetes_namespace.concierge.metadata[0].name
    labels = {
      app = "asterisk"
    }
  }

  spec {
    replicas = 1

    selector {
      match_labels = {
        app = "asterisk"
      }
    }

    template {
      metadata {
        labels = {
          app = "asterisk"
        }
      }

      spec {
        container {
          name  = "asterisk"
          image = "cr.yandex/${yandex_container_registry.concierge_registry.id}/asterisk:${var.asterisk_version}"
          image_pull_policy = "Always"

          port {
            name           = "sip"
            container_port = 5060
            protocol       = "UDP"
          }

          port {
            name           = "ami"
            container_port = 5038
            protocol       = "TCP"
          }

          port {
            name           = "http"
            container_port = 8088
            protocol       = "TCP"
          }

          port {
            name           = "rtp-start"
            container_port = 10000
            protocol       = "UDP"
          }

          port {
            name           = "rtp-end"
            container_port = 20000
            protocol       = "UDP"
          }

          env {
            name = "SIP_USER"
            value_from {
              config_map_key_ref {
                name = kubernetes_config_map.concierge_env.metadata[0].name
                key  = "SIP_USER"
              }
            }
          }

          env {
            name = "SIP_PASS"
            value_from {
              config_map_key_ref {
                name = kubernetes_config_map.concierge_env.metadata[0].name
                key  = "SIP_PASS"
              }
            }
          }

          env {
            name = "AMI_USER"
            value_from {
              config_map_key_ref {
                name = kubernetes_config_map.concierge_env.metadata[0].name
                key  = "AMI_USER"
              }
            }
          }

          env {
            name = "AMI_PASSWORD"
            value_from {
              config_map_key_ref {
                name = kubernetes_config_map.concierge_env.metadata[0].name
                key  = "AMI_PASSWORD"
              }
            }
          }

          env {
            name  = "TZ"
            value = "Europe/Moscow"
          }

          volume_mount {
            name       = "asterisk-config"
            mount_path = "/etc/asterisk/extensions.conf"
            sub_path   = "extensions.conf"
          }

          resources {
            requests = {
              memory = "512Mi"
              cpu    = "500m"
            }
            limits = {
              memory = "1Gi"
              cpu    = "2000m"
            }
          }

          security_context {
            privileged = true
            capabilities {
              add = ["NET_ADMIN", "SYS_ADMIN"]
            }
          }
        }

        volume {
          name = "asterisk-config"
          config_map {
            name = kubernetes_config_map.asterisk_config.metadata[0].name
          }
        }
      }
    }
  }
}

# Deployment для Voice Agent
resource "kubernetes_deployment" "voice_agent" {
  wait_for_rollout = false

  metadata {
    name      = "voice-agent"
    namespace = kubernetes_namespace.concierge.metadata[0].name
    labels = {
      app = "voice-agent"
    }
  }

  spec {
    replicas = var.voice_agent_replicas

    selector {
      match_labels = {
        app = "voice-agent"
      }
    }

    template {
      metadata {
        labels = {
          app = "voice-agent"
        }
      }

      spec {
        container {
          name  = "voice-agent"
          image = "cr.yandex/${yandex_container_registry.concierge_registry.id}/voice-agent:${var.voice_agent_version}"
          image_pull_policy = "Always"

          port {
            name           = "http"
            container_port = 8080
            protocol       = "TCP"
          }

          port {
            name           = "audiosocket"
            container_port = 9092
            protocol       = "TCP"
          }

          env {
            name = "API_KEY"
            value_from {
              config_map_key_ref {
                name = kubernetes_config_map.concierge_env.metadata[0].name
                key  = "API_KEY"
              }
            }
          }

          env {
            name = "FOLDER"
            value_from {
              config_map_key_ref {
                name = kubernetes_config_map.concierge_env.metadata[0].name
                key  = "FOLDER"
              }
            }
          }

          env {
            name = "PJSIP_ENDPOINT"
            value_from {
              config_map_key_ref {
                name = kubernetes_config_map.concierge_env.metadata[0].name
                key  = "PJSIP_ENDPOINT"
              }
            }
          }

          env {
            name = "INSTRUCTIONS"
            value_from {
              config_map_key_ref {
                name = kubernetes_config_map.concierge_env.metadata[0].name
                key  = "INSTRUCTIONS"
              }
            }
          }

          env {
            name = "HTTP_PORT"
            value_from {
              config_map_key_ref {
                name = kubernetes_config_map.concierge_config.metadata[0].name
                key  = "HTTP_PORT"
              }
            }
          }

          env {
            name = "AMI_HOST"
            value_from {
              config_map_key_ref {
                name = kubernetes_config_map.concierge_config.metadata[0].name
                key  = "AMI_HOST"
              }
            }
          }

          env {
            name = "AMI_PORT"
            value_from {
              config_map_key_ref {
                name = kubernetes_config_map.concierge_config.metadata[0].name
                key  = "AMI_PORT"
              }
            }
          }

          env {
            name = "AUDIO_SOCKET_PORT"
            value_from {
              config_map_key_ref {
                name = kubernetes_config_map.concierge_config.metadata[0].name
                key  = "AUDIO_SOCKET_PORT"
              }
            }
          }

          resources {
            requests = {
              memory = "256Mi"
              cpu    = "250m"
            }
            limits = {
              memory = "512Mi"
              cpu    = "1000m"
            }
          }

          liveness_probe {
            http_get {
              path = "/health"
              port = 8080
            }
            initial_delay_seconds = 30
            period_seconds       = 10
          }

          readiness_probe {
            http_get {
              path = "/health"
              port = 8080
            }
            initial_delay_seconds = 10
            period_seconds        = 5
          }
        }
      }
    }
  }
}

resource "kubernetes_secret" "ydb_sa_key" {
  metadata {
    name      = "ydb-sa-key"
    namespace = kubernetes_namespace.concierge.metadata[0].name
  }

  data = {
    "sa-key.json" = var.ydb_sa_key_json
  }
}

# Deployment для tg_bot
resource "kubernetes_deployment" "tg_bot" {
  wait_for_rollout = false

  metadata {
    name      = "tg-bot"
    namespace = kubernetes_namespace.concierge.metadata[0].name
    labels = {
      app = "tg-bot"
    }
  }

  spec {
    replicas = 1

    selector {
      match_labels = {
        app = "tg-bot"
      }
    }

    template {
      metadata {
        labels = {
          app = "tg-bot"
        }
      }

      spec {
        container {
          name              = "tg-bot"
          image             = "cr.yandex/${yandex_container_registry.concierge_registry.id}/tg-bot:${var.tg_bot_version}"
          image_pull_policy = "Always"

          env {
            name  = "BOT_TOKEN"
            value = var.tg_bot_token
          }

          env {
            name  = "CALLER_SERVICE_URL"
            value = "ws://voice-agent-service:8080"
          }

          env {
            name  = "YDB_DSN"
            value = "grpcs://ydb.serverless.yandexcloud.net:2135/ru-central1/b1gtm7vgbkkcd64rh3m8/etn752mqdjopimlmf0jk"
          }

          env {
            name  = "YDB_SA_KEY_FILE"
            value = "/secrets/ydb/sa-key.json"
          }

          volume_mount {
            name       = "ydb-sa-key"
            mount_path = "/secrets/ydb"
            read_only  = true
          }

          resources {
            requests = {
              memory = "256Mi"
              cpu    = "500m"
            }
            limits = {
              memory = "512Mi"
              cpu    = "1000m"
            }
          }
        }

        volume {
          name = "ydb-sa-key"

          secret {
            secret_name = kubernetes_secret.ydb_sa_key.metadata[0].name
          }
        }
      }
    }
  }
}

# Service для Asterisk
resource "kubernetes_service" "asterisk" {
  wait_for_load_balancer = false

  metadata {
    name      = "asterisk-service"
    namespace = kubernetes_namespace.concierge.metadata[0].name
    labels = {
      app = "asterisk"
    }
  }

  spec {
    type = "LoadBalancer"

    selector = {
      app = "asterisk"
    }

    port {
      name        = "sip"
      port        = 5060
      target_port = 5060
      protocol    = "UDP"
    }

    port {
      name        = "ami"
      port        = 5038
      target_port = 5038
      protocol    = "TCP"
    }

    port {
      name        = "http"
      port        = 8088
      target_port = 8088
      protocol    = "TCP"
    }

    port {
      name        = "rtp"
      port        = 10000
      target_port = 10000
      protocol    = "UDP"
    }

    session_affinity = "ClientIP"
    session_affinity_config {
      client_ip {
        timeout_seconds = 10800
      }
    }
  }
}

# Service для Voice Agent
resource "kubernetes_service" "voice_agent" {
  wait_for_load_balancer = false

  metadata {
    name      = "voice-agent-service"
    namespace = kubernetes_namespace.concierge.metadata[0].name
    labels = {
      app = "voice-agent"
    }
  }

  spec {
    type = "LoadBalancer"

    selector = {
      app = "voice-agent"
    }

    port {
      name        = "http"
      port        = 8080
      target_port = 8080
      protocol    = "TCP"
    }

    port {
      name        = "audiosocket"
      port        = 9092
      target_port = 9092
      protocol    = "TCP"
    }
  }
}

# Ingress для Voice Agent
resource "kubernetes_ingress_v1" "voice_agent_ingress" {
  metadata {
    name      = "voice-agent-ingress"
    namespace = kubernetes_namespace.concierge.metadata[0].name
    annotations = {
      "ingress.alb.yc.io/subnets" = join(",", [
        yandex_vpc_subnet.concierge_subnet_a.id,
        yandex_vpc_subnet.concierge_subnet_b.id
      ])
      "ingress.alb.yc.io/group-name" = "concierge-ingress"
    }
  }

  spec {
    ingress_class_name = "alb"

    rule {
      http {
        path {
          path      = "/ws"
          path_type = "Prefix"
          backend {
            service {
              name = kubernetes_service.voice_agent.metadata[0].name
              port {
                number = 8080
              }
            }
          }
        }
        path {
          path      = "/"
          path_type = "Prefix"
          backend {
            service {
              name = kubernetes_service.voice_agent.metadata[0].name
              port {
                number = 8080
              }
            }
          }
        }
      }
    }
  }
}
