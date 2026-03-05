variable "yandex_token" {
  description = "Yandex Cloud OAuth token"
  type        = string
}

variable "yandex_cloud_id" {
  description = "Yandex Cloud ID"
  type        = string
}

variable "yandex_folder_id" {
  description = "Yandex Cloud Folder ID"
  type        = string
  default     = "b1gavd8lf4jso96pv233"
}

variable "realtime_api_key" {
  description = "Yandex Cloud Realtime API key"
  type        = string
}

variable "realtime_folder_id" {
  description = "Yandex Cloud Realtime Folder id"
  type        = string
}

variable "yandex_zone" {
  description = "Yandex Cloud default zone"
  type        = string
  default     = "ru-central1-a"
}

variable "cluster_version" {
  description = "Kubernetes cluster version (см. https://cloud.yandex.ru/docs/managed-kubernetes/concepts/release-channels)"
  type        = string
  default     = "1.31"
}

variable "node_count" {
  description = "Number of nodes in the cluster"
  type        = number
  default     = 2
}

variable "node_cores" {
  description = "Number of CPU cores per node"
  type        = number
  default     = 4
}

variable "node_memory" {
  description = "Memory per node in GB"
  type        = number
  default     = 8
}

variable "node_disk_size" {
  description = "Disk size per node in GB"
  type        = number
  default     = 50
}

variable "yandex_api_key" {
  description = "Yandex Cloud API Key для приложения"
  type        = string
}

variable "sip_user" {
  description = "SIP username"
  type        = string
}

variable "sip_password" {
  description = "SIP password"
  type        = string
}

variable "ami_user" {
  description = "Asterisk AMI username"
  type        = string
  default     = "goami"
}

variable "ami_password" {
  description = "Asterisk AMI password"
  type        = string
  default     = "goamisecret123"
}

variable "pjsip_endpoint" {
  description = "Имя PJSIP endpoint/trunk в Asterisk для исходящих звонков (должен существовать в pjsip.conf)"
  type        = string
  default     = "zvonok"
}

variable "voice_agent_replicas" {
  description = "Number of voice-agent replicas"
  type        = number
  default     = 2
}

variable "asterisk_version" {
  description = "Asterisk container version in registry"
  type        = string
  default     = "1.0.2"
}

variable "voice_agent_version" {
  description = "Voice agent container version in registry"
  type        = string
  default     = "1.0.2"
}

variable "voice_agent_instructions" {
  description = "Промпт/инструкции для голосового ассистента (Yandex Realtime)"
  type        = string
  default     = <<-EOT
Тебе нужно очень уверенно доказывать ЧТО ТЫ КОМПЬЮТЕРНЫЙ СТУЛ.
EOT
}
