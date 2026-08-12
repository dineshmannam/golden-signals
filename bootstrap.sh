#!/usr/bin/env bash
#
# bootstrap.sh — stand up (or tear down) the prerequisites Terraform needs.
#
# What it creates, idempotently:
#   1. Enables the Google Cloud APIs the stack and provisioning depend on.
#   2. A versioned GCS bucket for Terraform remote state.
#   3. A least-privilege "provisioning" service account with the roles needed to
#      apply infra/terraform/ and deploy the app, plus object access to the
#      state bucket.
#   4. Prints the values the operator needs next (bucket name, SA email).
#
# The operator runs this against an already-selected GCP project; it does NOT
# authenticate for you. Re-running is safe (each step is a describe-then-create).
#
# Teardown: `./bootstrap.sh --destroy` removes the SA and its IAM bindings and
# (with --force) the state bucket. Enabled APIs are intentionally left in place —
# disabling them can break other workloads in a shared project (this mirrors
# infra/terraform/apis.tf's disable_on_destroy = false).
#
# Everything is parameterized via flags or env vars; see --help.
#
set -euo pipefail

# --- Defaults (override via flags or env) ------------------------------------
PROJECT_ID="${PROJECT_ID:-}"
REGION="${REGION:-us-central1}"
# BUCKET defaults to "<project>-golden-signals-tfstate" once PROJECT_ID is known.
BUCKET="${BUCKET:-}"
SA_NAME="${SA_NAME:-golden-signals-provisioner}"
DESTROY=0
FORCE=0

# Project-level roles granted to the provisioning service account. Scoped to
# what infra/terraform/ actually provisions (GKE, Cloud SQL, Artifact Registry,
# Pub/Sub, networking, monitoring, Secret Manager, IAM) — not roles/owner.
PROVISIONER_ROLES=(
  "roles/container.admin"                  # GKE Autopilot cluster
  "roles/cloudsql.admin"                   # Cloud SQL for Postgres
  "roles/artifactregistry.admin"           # Docker image repository
  "roles/pubsub.admin"                     # Pub/Sub (fulfillment path)
  "roles/compute.networkAdmin"             # VPC, subnet, global address
  "roles/servicenetworking.networksAdmin"  # Private Services Access peering
  "roles/monitoring.admin"                 # SLOs, alert policies, dashboard
  "roles/secretmanager.admin"              # DATABASE_URL secret
  "roles/iam.serviceAccountAdmin"          # per-workload GSAs
  "roles/iam.serviceAccountUser"           # act-as for the GSAs
  "roles/resourcemanager.projectIamAdmin"  # project IAM bindings in iam.tf
  "roles/serviceusage.serviceUsageAdmin"   # enable APIs in apis.tf
)

# APIs to enable up front. Superset of infra/terraform/apis.tf plus the ones
# bootstrapping itself needs (resource manager, service usage, storage).
REQUIRED_APIS=(
  "cloudresourcemanager.googleapis.com"
  "serviceusage.googleapis.com"
  "iam.googleapis.com"
  "storage.googleapis.com"
  "compute.googleapis.com"
  "container.googleapis.com"
  "sqladmin.googleapis.com"
  "servicenetworking.googleapis.com"
  "artifactregistry.googleapis.com"
  "pubsub.googleapis.com"
  "monitoring.googleapis.com"
  "logging.googleapis.com"
  "cloudtrace.googleapis.com"
  "secretmanager.googleapis.com"
  "cloudbuild.googleapis.com"
)

usage() {
  cat <<'EOF'
Usage: ./bootstrap.sh [options]

Stands up the prerequisites Terraform needs (APIs, state bucket, provisioning
service account). Idempotent and safe to re-run.

Options:
  --project ID        GCP project ID           (env PROJECT_ID) [required]
  --region REGION     Region for the bucket    (env REGION)   [default: us-central1]
  --bucket NAME       Terraform state bucket    (env BUCKET)
                        [default: <project>-golden-signals-tfstate]
  --sa-name NAME      Provisioning SA name      (env SA_NAME)
                        [default: golden-signals-provisioner]
  --destroy           Tear down what this script created (SA + bindings; bucket
                        only with --force).
  --force             Allow destructive bucket deletion under --destroy.
  -h, --help          Show this help.

Examples:
  ./bootstrap.sh --project my-proj
  ./bootstrap.sh --project my-proj --bucket my-tf-state --region us-east1
  ./bootstrap.sh --project my-proj --destroy --force
EOF
}

# --- Flag parsing ------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --project)  PROJECT_ID="$2"; shift 2 ;;
    --region)   REGION="$2";     shift 2 ;;
    --bucket)   BUCKET="$2";     shift 2 ;;
    --sa-name)  SA_NAME="$2";    shift 2 ;;
    --destroy)  DESTROY=1;       shift ;;
    --force)    FORCE=1;         shift ;;
    -h|--help)  usage; exit 0 ;;
    *) echo "error: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

# --- Validation --------------------------------------------------------------
if ! command -v gcloud >/dev/null 2>&1; then
  echo "error: gcloud not found on PATH" >&2
  exit 1
fi

if [[ -z "$PROJECT_ID" ]]; then
  echo "error: --project (or PROJECT_ID) is required" >&2
  usage >&2
  exit 2
fi

# Derive the bucket default now that the project is known.
BUCKET="${BUCKET:-${PROJECT_ID}-golden-signals-tfstate}"

SA_EMAIL="${SA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"
SA_MEMBER="serviceAccount:${SA_EMAIL}"

log() { echo "==> $*"; }

# --- Create path -------------------------------------------------------------
enable_apis() {
  log "Enabling ${#REQUIRED_APIS[@]} APIs on ${PROJECT_ID} (idempotent)…"
  # A single enable call is idempotent and much faster than one call per API.
  gcloud services enable "${REQUIRED_APIS[@]}" --project "$PROJECT_ID"
}

create_bucket() {
  local uri="gs://${BUCKET}"
  if gcloud storage buckets describe "$uri" --project "$PROJECT_ID" >/dev/null 2>&1; then
    log "Bucket ${uri} already exists — ensuring versioning is on."
  else
    log "Creating bucket ${uri} in ${REGION}…"
    gcloud storage buckets create "$uri" \
      --project "$PROJECT_ID" \
      --location "$REGION" \
      --uniform-bucket-level-access \
      --public-access-prevention
  fi
  # Versioning is required so Terraform state history is retained.
  gcloud storage buckets update "$uri" --versioning --project "$PROJECT_ID"
}

create_sa() {
  if gcloud iam service-accounts describe "$SA_EMAIL" --project "$PROJECT_ID" >/dev/null 2>&1; then
    log "Service account ${SA_EMAIL} already exists."
  else
    log "Creating service account ${SA_EMAIL}…"
    gcloud iam service-accounts create "$SA_NAME" \
      --project "$PROJECT_ID" \
      --display-name "golden-signals provisioner"
  fi
}

grant_roles() {
  log "Granting ${#PROVISIONER_ROLES[@]} project roles to ${SA_EMAIL} (idempotent)…"
  local role
  for role in "${PROVISIONER_ROLES[@]}"; do
    gcloud projects add-iam-policy-binding "$PROJECT_ID" \
      --member "$SA_MEMBER" \
      --role "$role" \
      --condition=None \
      --quiet >/dev/null
    echo "    + ${role}"
  done

  # Object-level access to the state bucket (least privilege vs project storage.admin).
  log "Granting objectAdmin on gs://${BUCKET} to ${SA_EMAIL}…"
  gcloud storage buckets add-iam-policy-binding "gs://${BUCKET}" \
    --project "$PROJECT_ID" \
    --member "$SA_MEMBER" \
    --role "roles/storage.objectAdmin" \
    --quiet >/dev/null
}

print_summary() {
  cat <<EOF

------------------------------------------------------------------------
Bootstrap complete. Values for the next steps:

  PROJECT_ID   : ${PROJECT_ID}
  REGION       : ${REGION}
  STATE_BUCKET : ${BUCKET}
  PROVISIONER  : ${SA_EMAIL}

Next:
  1. Create the Cloud Build GitHub host connection in the console (see DEPLOY.md).
  2. Run the trigger scripts in scripts/ with the connection name.
  3. terraform init -backend-config="bucket=${BUCKET}"  (in infra/terraform/)

To let Cloud Build / your user impersonate the provisioner, grant
roles/iam.serviceAccountTokenCreator on ${SA_EMAIL} to that principal.
------------------------------------------------------------------------
EOF
}

do_create() {
  enable_apis
  create_bucket
  create_sa
  grant_roles
  print_summary
}

# --- Destroy path ------------------------------------------------------------
revoke_roles() {
  if ! gcloud iam service-accounts describe "$SA_EMAIL" --project "$PROJECT_ID" >/dev/null 2>&1; then
    return 0
  fi
  log "Removing project IAM bindings for ${SA_EMAIL}…"
  local role
  for role in "${PROVISIONER_ROLES[@]}"; do
    gcloud projects remove-iam-policy-binding "$PROJECT_ID" \
      --member "$SA_MEMBER" \
      --role "$role" \
      --condition=None \
      --quiet >/dev/null 2>&1 || true
    echo "    - ${role}"
  done
}

delete_sa() {
  if gcloud iam service-accounts describe "$SA_EMAIL" --project "$PROJECT_ID" >/dev/null 2>&1; then
    log "Deleting service account ${SA_EMAIL}…"
    gcloud iam service-accounts delete "$SA_EMAIL" --project "$PROJECT_ID" --quiet
  else
    log "Service account ${SA_EMAIL} not found — skipping."
  fi
}

delete_bucket() {
  local uri="gs://${BUCKET}"
  if ! gcloud storage buckets describe "$uri" --project "$PROJECT_ID" >/dev/null 2>&1; then
    log "Bucket ${uri} not found — skipping."
    return 0
  fi
  if [[ "$FORCE" -ne 1 ]]; then
    log "Refusing to delete ${uri} (holds Terraform state). Re-run with --force to delete it."
    return 0
  fi
  log "Deleting bucket ${uri} and all objects/versions…"
  gcloud storage rm --recursive "${uri}" --project "$PROJECT_ID" --quiet || true
  gcloud storage buckets delete "$uri" --project "$PROJECT_ID" --quiet
}

do_destroy() {
  log "Tearing down bootstrap resources for ${PROJECT_ID}…"
  revoke_roles
  delete_sa
  delete_bucket
  log "Teardown complete. Enabled APIs were left in place (disabling can break other workloads)."
}

# --- Dispatch ----------------------------------------------------------------
if [[ "$DESTROY" -eq 1 ]]; then
  do_destroy
else
  do_create
fi
