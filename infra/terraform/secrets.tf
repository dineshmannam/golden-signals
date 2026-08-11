# Store the assembled DATABASE_URL in Secret Manager. The orders service's GCP
# service account is granted accessor rights (iam.tf); the operator wires the
# secret into the pod (e.g. via a Kubernetes Secret or the Secret Manager CSI
# driver) as documented in deploy/README.md.
resource "google_secret_manager_secret" "database_url" {
  secret_id = "${var.name_prefix}-database-url"

  replication {
    auto {}
  }

  depends_on = [google_project_service.enabled]
}

resource "google_secret_manager_secret_version" "database_url" {
  secret = google_secret_manager_secret.database_url.id

  # Private-IP DSN. sslmode=disable is acceptable because traffic stays on the
  # VPC; switch to verify-full with the server CA for defence in depth.
  secret_data = format(
    "postgres://%s:%s@%s:5432/%s?sslmode=disable",
    var.postgres_user,
    random_password.db.result,
    google_sql_database_instance.postgres.private_ip_address,
    var.postgres_database_name,
  )
}
