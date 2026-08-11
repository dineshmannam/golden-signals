# Network for the GKE cluster and private Cloud SQL connectivity.
#
# A dedicated VPC with a single subnet in var.region. The subnet carries two
# secondary ranges used by GKE for pod and service IPs (VPC-native / alias IPs).
# Cloud SQL is reached over a private IP via a Private Services Access peering.

resource "google_compute_network" "vpc" {
  name                    = "${var.name_prefix}-vpc"
  auto_create_subnetworks = false
  depends_on              = [google_project_service.enabled]
}

resource "google_compute_subnetwork" "gke" {
  name          = "${var.name_prefix}-gke"
  region        = var.region
  network       = google_compute_network.vpc.id
  ip_cidr_range = "10.10.0.0/20"

  # Alias IP ranges for VPC-native GKE.
  secondary_ip_range {
    range_name    = "pods"
    ip_cidr_range = "10.20.0.0/16"
  }
  secondary_ip_range {
    range_name    = "services"
    ip_cidr_range = "10.30.0.0/20"
  }

  private_ip_google_access = true
}

# --- Private Services Access: lets Cloud SQL expose a private IP in our VPC. ---

resource "google_compute_global_address" "private_services" {
  name          = "${var.name_prefix}-psa"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = google_compute_network.vpc.id
}

resource "google_service_networking_connection" "private_services" {
  network                 = google_compute_network.vpc.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_services.name]
}
