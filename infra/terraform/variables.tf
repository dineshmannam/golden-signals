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

variable "pubsub_topic_order_created" {
  description = "Pub/Sub topic for OrderCreated events (must match orders' PUBSUB_TOPIC_ORDER_CREATED)."
  type        = string
  default     = "order-created"
}

variable "pubsub_subscription_order_created" {
  description = "Pull subscription the fulfillment worker consumes (must match its PUBSUB_SUBSCRIPTION_ORDER_CREATED)."
  type        = string
  default     = "order-created-fulfillment"
}

variable "pubsub_dead_letter_max_attempts" {
  description = "Delivery attempts before a message is routed to the dead-letter topic (min 5 per Pub/Sub)."
  type        = number
  default     = 5
}

variable "pubsub_ack_deadline_seconds" {
  description = "Ack deadline for the fulfillment subscription."
  type        = number
  default     = 30
}

variable "slo_fulfillment_goal" {
  description = "Fraction of fulfillment messages that must process successfully (0.99 = 99%)."
  type        = number
  default     = 0.99
}

variable "deletion_protection" {
  description = "Protect the Cloud SQL instance and GKE cluster from accidental deletion."
  type        = bool
  default     = true
}

variable "create_slos" {
  description = "Create the Cloud Monitoring SLOs, burn-rate alerts, and SLO dashboard tiles. Keep false for the initial infra apply — the SLIs filter on Prometheus metrics (http_requests_total, messages_processed_total) that only exist once the app is deployed and serving traffic. Flip to true in the post-deploy Step 3 apply (see DEPLOY.md)."
  type        = bool
  default     = false
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
