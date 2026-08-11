# GKE Autopilot cluster.
#
# Autopilot manages nodes for us and enables Workload Identity by default, which
# is exactly the identity model this stack uses (see iam.tf). The cluster is
# VPC-native, using the secondary ranges defined on the subnet.

resource "google_container_cluster" "autopilot" {
  name     = "${var.name_prefix}-cluster"
  location = var.region

  enable_autopilot = true

  network    = google_compute_network.vpc.id
  subnetwork = google_compute_subnetwork.gke.id

  ip_allocation_policy {
    cluster_secondary_range_name  = "pods"
    services_secondary_range_name = "services"
  }

  release_channel {
    channel = var.gke_release_channel
  }

  # Workload Identity is on by default under Autopilot; declaring the pool makes
  # the binding target explicit and documents the trust domain.
  workload_identity_config {
    workload_pool = "${var.project_id}.svc.id.goog"
  }

  deletion_protection = var.deletion_protection

  depends_on = [google_project_service.enabled]
}
