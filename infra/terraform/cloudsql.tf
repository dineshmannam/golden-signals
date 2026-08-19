# Cloud SQL for PostgreSQL, private-IP only.
#
# The instance has no public IP; the app reaches it over the VPC via the Private
# Services Access peering created in network.tf. A random password is generated
# and stored in Secret Manager (secrets.tf) rather than being printed to state
# as a plaintext output.

resource "google_sql_database_instance" "postgres" {
  name             = "${var.name_prefix}-pg"
  region           = var.region
  database_version = var.postgres_version

  deletion_protection = var.deletion_protection

  depends_on = [google_service_networking_connection.private_services]

  settings {
    # Custom machine tiers (db-custom-*) are only valid on ENTERPRISE. Without
    # this, GCP defaults new instances to ENTERPRISE_PLUS, which rejects the
    # custom tier with an "invalid tier" error at apply time.
    edition           = "ENTERPRISE"
    tier              = var.postgres_tier
    availability_type = "ZONAL"
    disk_autoresize   = true

    ip_configuration {
      ipv4_enabled    = false
      private_network = google_compute_network.vpc.id
    }

    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = true
    }

    insights_config {
      query_insights_enabled = true
    }
  }
}

resource "google_sql_database" "orders" {
  name     = var.postgres_database_name
  instance = google_sql_database_instance.postgres.name
}

resource "random_password" "db" {
  length  = 24
  special = false
}

resource "google_sql_user" "app" {
  name     = var.postgres_user
  instance = google_sql_database_instance.postgres.name
  password = random_password.db.result
}
