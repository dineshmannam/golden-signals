output "gke_cluster_name" {
  description = "Name of the GKE Autopilot cluster."
  value       = google_container_cluster.autopilot.name
}

output "gke_cluster_location" {
  description = "Location (region) of the GKE cluster."
  value       = google_container_cluster.autopilot.location
}

output "get_credentials_command" {
  description = "Command to configure kubectl against the cluster."
  value       = "gcloud container clusters get-credentials ${google_container_cluster.autopilot.name} --region ${var.region} --project ${var.project_id}"
}

output "artifact_registry_repo" {
  description = "Docker repository path for pushing/pulling images."
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}"
}

output "cloudsql_instance" {
  description = "Cloud SQL instance connection name."
  value       = google_sql_database_instance.postgres.connection_name
}

output "cloudsql_private_ip" {
  description = "Private IP of the Cloud SQL instance."
  value       = google_sql_database_instance.postgres.private_ip_address
}

output "database_url_secret" {
  description = "Secret Manager secret holding the orders DATABASE_URL."
  value       = google_secret_manager_secret.database_url.secret_id
}

output "service_accounts" {
  description = "GSA emails to annotate the matching Kubernetes service accounts with (Workload Identity)."
  value = {
    collector   = google_service_account.collector.email
    orders      = google_service_account.orders.email
    gateway     = google_service_account.gateway.email
    fulfillment = google_service_account.fulfillment.email
  }
}

output "pubsub_topic" {
  description = "Pub/Sub topic OrderCreated events are published to."
  value       = google_pubsub_topic.order_created.name
}

output "pubsub_subscription" {
  description = "Pull subscription the fulfillment worker consumes."
  value       = google_pubsub_subscription.order_created_fulfillment.name
}

output "pubsub_dead_letter_subscription" {
  description = "Subscription retaining messages that exhausted their delivery attempts."
  value       = google_pubsub_subscription.order_created_dead_letter.name
}

output "dashboard_id" {
  description = "Cloud Monitoring dashboard resource name."
  value       = google_monitoring_dashboard.golden_signals.id
}
