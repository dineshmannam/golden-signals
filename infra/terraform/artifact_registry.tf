# Artifact Registry Docker repository for the service images. Cloud Build pushes
# here (see cloudbuild.yaml) and GKE pulls from here.
resource "google_artifact_registry_repository" "images" {
  location      = var.region
  repository_id = var.name_prefix
  format        = "DOCKER"
  description   = "Container images for the golden-signals services."

  depends_on = [google_project_service.enabled]
}
