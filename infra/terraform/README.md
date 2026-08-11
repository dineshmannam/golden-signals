# Terraform: golden-signals infrastructure

Provisions everything the demo needs on Google Cloud. **Do not** expect to run
this without GCP credentials; `terraform apply` is the operator's job.

## What it creates

| File | Resources |
|---|---|
| `apis.tf` | Enables the required Google Cloud APIs. |
| `network.tf` | VPC, subnet with pod/service secondary ranges, Private Services Access for Cloud SQL. |
| `gke.tf` | GKE **Autopilot** cluster (VPC-native, Workload Identity). |
| `cloudsql.tf` | Cloud SQL for Postgres, **private IP only**, DB + user with a generated password. |
| `secrets.tf` | Stores the assembled `DATABASE_URL` in Secret Manager. |
| `artifact_registry.tf` | Docker repository for the service images. |
| `iam.tf` | Per-workload service accounts, least-privilege roles, Workload Identity bindings, image pull/push grants. |
| `monitoring.tf` | Monitoring Service, availability + latency **SLOs** (28-day rolling budget), **multi-window multi-burn-rate** alert policies, golden-signals **dashboard**. |

## Usage

```bash
# 1. Remote state bucket (once).
gcloud storage buckets create gs://YOUR_TF_STATE_BUCKET --location=us-central1

# 2. Variables.
cp terraform.example.tfvars terraform.tfvars   # set project_id (required)

# 3. Init with the state bucket, then plan/apply.
terraform init -backend-config="bucket=YOUR_TF_STATE_BUCKET"
terraform plan
terraform apply
```

To validate or format without a backend or credentials (what CI does):

```bash
terraform fmt -check -recursive
terraform init -backend=false
terraform validate
```

## Remote state

State lives in a GCS bucket. The bucket name is passed at init time
(`-backend-config`) because backend blocks can't use variables; the `prefix` is
fixed in `backend.tf`. Enable object versioning on the bucket for state history.

## SLO / alerting knobs

`slo_availability_goal` (default 0.99), `slo_latency_goal` (0.99),
`slo_latency_threshold_ms` (300), and `slo_rolling_period_days` (28) are
variables. Alert policies attach to `notification_channels` (empty by default) —
create channels (email/PagerDuty/…) and pass their IDs to actually get paged.

## Notes

- **`deletion_protection`** defaults to `true` on the cluster and Cloud SQL. Set
  it to `false` before `terraform destroy`.
- The SLO metric filters reference the GMP series
  `prometheus.googleapis.com/http_requests_total/counter` and
  `.../http_request_duration_seconds/histogram`, scoped to
  `metric.label."service"="gateway"`. These are produced by the services and
  scraped via the `PodMonitoring` in `deploy/`; they only appear once workloads
  are running.
