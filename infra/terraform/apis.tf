# Enable the Google Cloud APIs the stack depends on. Every other resource
# depends on these via the implicit reference through google_project_service.
locals {
  required_apis = [
    "compute.googleapis.com",
    "container.googleapis.com",
    "sqladmin.googleapis.com",
    "servicenetworking.googleapis.com",
    "artifactregistry.googleapis.com",
    "monitoring.googleapis.com",
    "logging.googleapis.com",
    "cloudtrace.googleapis.com",
    "pubsub.googleapis.com",
    "iam.googleapis.com",
    "secretmanager.googleapis.com",
    "cloudbuild.googleapis.com",
  ]
}

resource "google_project_service" "enabled" {
  for_each = toset(local.required_apis)

  project = var.project_id
  service = each.value

  # Keep APIs enabled if the stack is destroyed; disabling them can break other
  # workloads in a shared project.
  disable_on_destroy = false
}
