variable "project_id" {
  description = "GCP project ID to deploy into. Never hard-coded; supplied via tfvars."
  type        = string
}

variable "region" {
  description = "GCP region for regional resources."
  type        = string
  default     = "us-central1"
}

variable "name_prefix" {
  description = "Prefix applied to resource names so multiple stacks can coexist."
  type        = string
  default     = "golden-signals"
}

variable "gke_release_channel" {
  description = "GKE release channel for the Autopilot cluster."
  type        = string
  default     = "REGULAR"
}

variable "kubernetes_namespace" {
  description = "Kubernetes namespace the workloads run in (must match deploy/ manifests)."
  type        = string
  default     = "golden-signals"
}

variable "postgres_version" {
  description = "Cloud SQL Postgres version."
  type        = string
  default     = "POSTGRES_16"
}

variable "postgres_tier" {
  description = "Cloud SQL machine tier. db-custom-1-3840 = 1 vCPU / 3.75GB, fine for a demo."
  type        = string
  default     = "db-custom-1-3840"
}

variable "postgres_database_name" {
  description = "Name of the application database."
  type        = string
  default     = "orders"
}

variable "postgres_user" {
  description = "Application database user."
  type        = string
  default     = "orders"
}

variable "deletion_protection" {
  description = "Protect the Cloud SQL instance and GKE cluster from accidental deletion."
  type        = bool
  default     = true
}

variable "slo_availability_goal" {
  description = "Availability SLO target as a fraction (0.99 = 99%)."
  type        = number
  default     = 0.99
}

variable "slo_latency_goal" {
  description = "Fraction of requests that must be faster than slo_latency_threshold_ms."
  type        = number
  default     = 0.99
}

variable "slo_latency_threshold_ms" {
  description = "Latency objective in milliseconds (p99 target). Requests faster than this are 'good'."
  type        = number
  default     = 300
}

variable "slo_rolling_period_days" {
  description = "Rolling compliance/error-budget window in days."
  type        = number
  default     = 28
}

variable "notification_channels" {
  description = "Cloud Monitoring notification channel IDs to attach to alert policies. Empty by default; the operator creates channels and supplies them."
  type        = list(string)
  default     = []
}
