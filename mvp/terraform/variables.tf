variable "yandex_token" {
  description = "Yandex Cloud OAuth token"
  type        = string
  sensitive   = true
}

variable "yandex_cloud_id" {
  description = "Yandex Cloud ID"
  type        = string
}

variable "yandex_folder_id" {
  description = "Yandex Cloud Folder ID"
  type        = string
}

variable "yandex_zone" {
  description = "Yandex Cloud default zone"
  type        = string
  default     = "ru-central1-a"
}

variable "cluster_version" {
  description = "Kubernetes cluster version"
  type        = string
  default     = "1.30"
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

# Переменные для развертывания приложения
variable "yandex_api_key" {
  description = "Yandex Cloud API Key для приложения"
  type        = string
  sensitive   = true
}

variable "sip_user" {
  description = "SIP username"
  type        = string
  sensitive   = true
}

variable "sip_password" {
  description = "SIP password"
  type        = string
  sensitive   = true
}

variable "ami_user" {
  description = "Asterisk AMI username"
  type        = string
  default     = "goami"
  sensitive   = true
}

variable "ami_password" {
  description = "Asterisk AMI password"
  type        = string
  default     = "goamisecret123"
  sensitive   = true
}

variable "voice_agent_replicas" {
  description = "Number of voice-agent replicas"
  type        = number
  default     = 2
}

