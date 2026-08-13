#!/usr/bin/env bash
#
# create-triggers.sh — create the Cloud Build triggers for golden-signals.
#
# Creates two 2nd-gen (GitHub host connection) push triggers against a repository
# that is already linked to an existing Cloud Build host connection (the operator
# creates that connection in the console — see DEPLOY.md):
#
#   infra : fires on changes under infra/**, runs cloudbuild-infra.yaml
#           (terraform init/plan/apply against the remote state bucket).
#   app   : fires on changes under services/**, runs cloudbuild.yaml
#           (lint/test + build/push the service images).
#
# The connection and repository must already exist; this script only creates the
# triggers. Re-running deletes and recreates a trigger of the same name so it is
# effectively idempotent. Everything is parameterized; see --help.
#
set -euo pipefail

# --- Defaults (override via flags or env) ------------------------------------
PROJECT_ID="${PROJECT_ID:-}"
REGION="${REGION:-us-central1}"
CONNECTION="${CONNECTION:-}"
REPO="${REPO:-golden-signals}"
BRANCH="${BRANCH:-main}"
STATE_BUCKET="${STATE_BUCKET:-}"
SERVICE_ACCOUNT="${SERVICE_ACCOUNT:-}"
WHICH="both"   # both | infra | app

usage() {
  cat <<'EOF'
Usage: ./scripts/create-triggers.sh [options]

Creates Cloud Build push triggers (2nd-gen / GitHub host connection) for the
infra and app pipelines. The host connection and linked repository must already
exist (created by the operator in the console).

Options:
  --project ID          GCP project ID              (env PROJECT_ID) [required]
  --region REGION       Trigger/connection region   (env REGION)    [default: us-central1]
  --connection NAME     Existing host connection    (env CONNECTION) [required]
  --repo NAME           Linked repository name      (env REPO)      [default: golden-signals]
  --branch REGEX        Branch to match             (env BRANCH)    [default: main]
  --state-bucket NAME   TF state bucket, passed to the infra build
                          (env STATE_BUCKET) [required for the infra trigger]
  --service-account SA  Service account to run builds as (email or full resource
                          path). (env SERVICE_ACCOUNT) [optional]
  --only infra|app      Create just one trigger instead of both.
  -h, --help            Show this help.

Examples:
  ./scripts/create-triggers.sh --project my-proj --connection gh-conn \
    --repo golden-signals --state-bucket my-tf-state \
    --service-account golden-signals-provisioner@my-proj.iam.gserviceaccount.com

  ./scripts/create-triggers.sh --project my-proj --connection gh-conn \
    --only app
EOF
}

# --- Flag parsing ------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --project)          PROJECT_ID="$2";      shift 2 ;;
    --region)           REGION="$2";          shift 2 ;;
    --connection)       CONNECTION="$2";      shift 2 ;;
    --repo)             REPO="$2";            shift 2 ;;
    --branch)           BRANCH="$2";          shift 2 ;;
    --state-bucket)     STATE_BUCKET="$2";    shift 2 ;;
    --service-account)  SERVICE_ACCOUNT="$2"; shift 2 ;;
    --only)             WHICH="$2";           shift 2 ;;
    -h|--help)          usage; exit 0 ;;
    *) echo "error: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

# --- Validation --------------------------------------------------------------
if ! command -v gcloud >/dev/null 2>&1; then
  echo "error: gcloud not found on PATH" >&2
  exit 1
fi
if [[ -z "$PROJECT_ID" ]]; then
  echo "error: --project (or PROJECT_ID) is required" >&2; usage >&2; exit 2
fi
if [[ -z "$CONNECTION" ]]; then
  echo "error: --connection (or CONNECTION) is required" >&2; usage >&2; exit 2
fi
case "$WHICH" in
  both|infra|app) ;;
  *) echo "error: --only must be 'infra' or 'app'" >&2; exit 2 ;;
esac

# Full resource name of the connection-linked repository (2nd-gen triggers use
# --repository rather than --repo-name/--repo-owner).
REPO_RESOURCE="projects/${PROJECT_ID}/locations/${REGION}/connections/${CONNECTION}/repositories/${REPO}"

log() { echo "==> $*"; }

# Build the common --service-account flag (empty when not supplied).
sa_flag() {
  [[ -n "$SERVICE_ACCOUNT" ]] && printf -- '--service-account=%s' "$SERVICE_ACCOUNT"
}

# Recreate a trigger idempotently: delete any existing one of the same name,
# then create it. `create` alone fails if the name is taken.
recreate() {
  local name="$1"; shift
  if gcloud builds triggers describe "$name" \
       --project "$PROJECT_ID" --region "$REGION" >/dev/null 2>&1; then
    log "Trigger ${name} exists — recreating."
    gcloud builds triggers delete "$name" \
      --project "$PROJECT_ID" --region "$REGION" --quiet
  fi
  log "Creating trigger ${name}…"
  gcloud builds triggers create github "$@" \
    --name="$name" \
    --project="$PROJECT_ID" \
    --region="$REGION" \
    --repository="$REPO_RESOURCE" \
    --branch-pattern="$BRANCH"
}

create_infra_trigger() {
  if [[ -z "$STATE_BUCKET" ]]; then
    echo "error: --state-bucket is required for the infra trigger" >&2
    exit 2
  fi
  local extra=()
  local sa; sa="$(sa_flag)"; [[ -n "$sa" ]] && extra+=("$sa")
  recreate "golden-signals-infra" \
    --description="golden-signals: terraform on infra/** changes" \
    --included-files="infra/**" \
    --build-config="cloudbuild-infra.yaml" \
    --substitutions="_STATE_BUCKET=${STATE_BUCKET},_REGION=${REGION}" \
    ${extra[@]+"${extra[@]}"}
}

create_app_trigger() {
  local extra=()
  local sa; sa="$(sa_flag)"; [[ -n "$sa" ]] && extra+=("$sa")
  recreate "golden-signals-app" \
    --description="golden-signals: build/push images on services/** changes" \
    --included-files="services/**" \
    --build-config="cloudbuild.yaml" \
    --substitutions="_REGION=${REGION},_REPO=golden-signals" \
    ${extra[@]+"${extra[@]}"}
}

case "$WHICH" in
  infra) create_infra_trigger ;;
  app)   create_app_trigger ;;
  both)  create_infra_trigger; create_app_trigger ;;
esac

log "Done."
