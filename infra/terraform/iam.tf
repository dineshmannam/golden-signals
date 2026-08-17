# Least-privilege service accounts and Workload Identity bindings.
#
# Each workload gets its own Google service account (GSA) bound to the matching
# Kubernetes service account (KSA) via Workload Identity. Roles are scoped to the
# minimum each workload needs:
#
#   collector   : write traces, logs and metrics to Google Cloud.
#   orders      : connect to Cloud SQL, read the DB-URL secret, publish OrderCreated.
#   gateway     : nothing project-wide (it talks only to orders and the collector).
#   fulfillment : connect to Cloud SQL, read the DB-URL secret, subscribe to events.
#
# The Pub/Sub publish/subscribe bindings themselves live in pubsub.tf, scoped to
# the specific topic/subscription for least privilege.
#
# The KSA<->GSA link is the roles/iam.workloadIdentityUser binding on the GSA for
# the member serviceAccount:PROJECT.svc.id.goog[NAMESPACE/KSA].

data "google_project" "this" {
  project_id = var.project_id
}

locals {
  ns = var.kubernetes_namespace

  # Workload identity member strings for each KSA.
  wi_members = {
    collector   = "serviceAccount:${var.project_id}.svc.id.goog[${local.ns}/otel-collector]"
    orders      = "serviceAccount:${var.project_id}.svc.id.goog[${local.ns}/orders]"
    gateway     = "serviceAccount:${var.project_id}.svc.id.goog[${local.ns}/gateway]"
    fulfillment = "serviceAccount:${var.project_id}.svc.id.goog[${local.ns}/fulfillment]"
  }
}

# --- Collector GSA ---
resource "google_service_account" "collector" {
  account_id   = "${var.name_prefix}-collector"
  display_name = "golden-signals OTel Collector"
}

resource "google_project_iam_member" "collector_trace" {
  project = var.project_id
  role    = "roles/cloudtrace.agent"
  member  = "serviceAccount:${google_service_account.collector.email}"
}

resource "google_project_iam_member" "collector_logs" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.collector.email}"
}

resource "google_project_iam_member" "collector_metrics" {
  project = var.project_id
  role    = "roles/monitoring.metricWriter"
  member  = "serviceAccount:${google_service_account.collector.email}"
}

resource "google_service_account_iam_member" "collector_wi" {
  service_account_id = google_service_account.collector.name
  role               = "roles/iam.workloadIdentityUser"
  member             = local.wi_members.collector
}

# --- Orders GSA ---
resource "google_service_account" "orders" {
  account_id   = "${var.name_prefix}-orders"
  display_name = "golden-signals orders service"
}

resource "google_project_iam_member" "orders_sql" {
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = "serviceAccount:${google_service_account.orders.email}"
}

resource "google_secret_manager_secret_iam_member" "orders_db_secret" {
  secret_id = google_secret_manager_secret.database_url.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.orders.email}"
}

resource "google_service_account_iam_member" "orders_wi" {
  service_account_id = google_service_account.orders.name
  role               = "roles/iam.workloadIdentityUser"
  member             = local.wi_members.orders
}

# --- Fulfillment GSA ---
resource "google_service_account" "fulfillment" {
  account_id   = "${var.name_prefix}-fulfillment"
  display_name = "golden-signals fulfillment worker"
}

resource "google_project_iam_member" "fulfillment_sql" {
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = "serviceAccount:${google_service_account.fulfillment.email}"
}

resource "google_secret_manager_secret_iam_member" "fulfillment_db_secret" {
  secret_id = google_secret_manager_secret.database_url.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.fulfillment.email}"
}

resource "google_service_account_iam_member" "fulfillment_wi" {
  service_account_id = google_service_account.fulfillment.name
  role               = "roles/iam.workloadIdentityUser"
  member             = local.wi_members.fulfillment
}

# --- Gateway GSA (no project-level roles: least privilege) ---
resource "google_service_account" "gateway" {
  account_id   = "${var.name_prefix}-gateway"
  display_name = "golden-signals gateway service"
}

resource "google_service_account_iam_member" "gateway_wi" {
  service_account_id = google_service_account.gateway.name
  role               = "roles/iam.workloadIdentityUser"
  member             = local.wi_members.gateway
}

# --- Image pull / push ---

# Autopilot pulls images with the default compute service account.
resource "google_project_iam_member" "gke_artifact_reader" {
  project = var.project_id
  role    = "roles/artifactregistry.reader"
  member  = "serviceAccount:${data.google_project.this.number}-compute@developer.gserviceaccount.com"
}

# Cloud Build pushes images to Artifact Registry.
resource "google_project_iam_member" "cloudbuild_artifact_writer" {
  project = var.project_id
  role    = "roles/artifactregistry.writer"
  member  = "serviceAccount:${data.google_project.this.number}@cloudbuild.gserviceaccount.com"
}
