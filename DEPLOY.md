# Deploy golden-signals

End-to-end, repeatable deploy of golden-signals to your own GCP project, plus a
clean teardown path. Runtime is **GKE Autopilot**; region defaults to
**us-central1**; the GCP project stays a variable — nothing here hard-codes a
project ID or a secret.

The flow is:

1. **`bootstrap.sh`** — APIs, Terraform state bucket, provisioning service account.
2. **Create the Cloud Build host connection** (manual, console — a few clicks).
3. **`scripts/create-triggers.sh`** — the infra and app build triggers.
4. **Terraform + Config Sync** apply the infrastructure and reconcile the cluster.
5. **Teardown** when you're done.

Prerequisites: `gcloud` (authenticated: `gcloud auth login` and
`gcloud config set project <id>`), and a GCP project with billing enabled. The
scripts do not authenticate for you.

---

## 1. Bootstrap prerequisites

`bootstrap.sh` is idempotent (safe to re-run) and parameterized. It enables the
required APIs, creates a **versioned** GCS bucket for Terraform remote state, and
creates a least-privilege **provisioning service account**.

```bash
./bootstrap.sh --project my-gcp-project
```

Options (flags or env vars; see `./bootstrap.sh --help`):

| Flag | Env | Default |
|---|---|---|
| `--project` | `PROJECT_ID` | *(required)* |
| `--region` | `REGION` | `us-central1` |
| `--bucket` | `BUCKET` | `<project>-golden-signals-tfstate` |
| `--sa-name` | `SA_NAME` | `golden-signals-provisioner` |

It prints the **state bucket name** and **provisioning SA email** — you need both
below. To let Cloud Build or your user run builds as the provisioner, grant that
principal `roles/iam.serviceAccountTokenCreator` on the SA.

---

## 2. Create the Cloud Build host connection (manual)

The GitHub ↔ Cloud Build **2nd-gen host connection** requires OAuth clicks, so
the operator creates it once in the console (it can't be fully scripted):

1. Console → **Cloud Build → Repositories → 2nd gen → Create host connection**.
2. Region: **us-central1** (must match `--region` used elsewhere). Provider:
   **GitHub**. Authorize the Google Cloud Build GitHub App and install it on the
   `golden-signals` repository.
3. Give the connection a name (e.g. `gh-conn`) and **Link repository**
   `golden-signals`.

Note the **connection name** and **repository name** for the next step. (Docs:
<https://cloud.google.com/build/docs/automating-builds/github/connect-repo-github>.)

---

## 3. Create the build triggers

`scripts/create-triggers.sh` creates two push triggers against the connection you
just made. It only creates triggers — the connection and linked repo must already
exist. Re-running recreates a same-named trigger, so it's effectively idempotent.

```bash
./scripts/create-triggers.sh \
  --project my-gcp-project \
  --connection gh-conn \
  --repo golden-signals \
  --state-bucket my-gcp-project-golden-signals-tfstate \
  --service-account golden-signals-provisioner@my-gcp-project.iam.gserviceaccount.com
```

| Trigger | Fires on | Build config | Purpose |
|---|---|---|---|
| `golden-signals-infra` | `infra/**` | `cloudbuild-infra.yaml` | `terraform init/plan/apply` against remote state |
| `golden-signals-app` | `services/**` | `cloudbuild.yaml` | lint/test + build & push service images |

Flags (see `--help`): `--region` (default `us-central1`), `--branch` (default
`main`), `--only infra|app` to create just one. `--state-bucket` is required for
the infra trigger; `--service-account` is optional but recommended (builds run as
the provisioning SA rather than the default Cloud Build SA).

The infra build passes `_APPLY=true` by default. To make the pipeline plan-only,
create the infra trigger and then set the `_APPLY=false` substitution on it in the
console, or run Terraform manually (below).

---

## 4. Apply infrastructure & reconcile the cluster

You can let the **infra trigger** apply Terraform on the next push under
`infra/**`, or run it manually the first time:

```bash
cd infra/terraform
cp terraform.example.tfvars terraform.tfvars   # set project_id (required)
terraform init -backend-config="bucket=my-gcp-project-golden-signals-tfstate"
terraform plan
terraform apply
```

This provisions GKE Autopilot, Cloud SQL, Artifact Registry, IAM/Workload
Identity, and the Cloud Monitoring SLOs/alerts/dashboard. `terraform output`
prints the image repo, GSA emails, DB-URL secret, and the `get-credentials`
command. See [`infra/terraform/README.md`](infra/terraform/README.md).

Then build images (the app trigger does this on `services/**` changes, or run
`gcloud builds submit --config cloudbuild.yaml`) and follow
[`deploy/README.md`](deploy/README.md) to substitute your project ID, create the
`orders-db` Secret, and apply the `RootSync` so Config Sync reconciles
`deploy/overlays/prod`.

---

## 5. Teardown

Reverse order. First destroy the Terraform-managed infrastructure, then remove the
triggers and the bootstrap resources.

```bash
# 1. Infra. deletion_protection defaults to true on GKE + Cloud SQL — set it to
#    false first (var or terraform.tfvars), re-apply, then destroy.
cd infra/terraform
terraform destroy

# 2. Triggers.
gcloud builds triggers delete golden-signals-infra --region us-central1 --project my-gcp-project
gcloud builds triggers delete golden-signals-app   --region us-central1 --project my-gcp-project

# 3. Host connection: delete it in the console (or `gcloud builds connections delete`).

# 4. Bootstrap resources (SA + IAM bindings; add --force to also delete the
#    state bucket and its contents).
./bootstrap.sh --project my-gcp-project --destroy --force
```

`bootstrap.sh --destroy` leaves the **enabled APIs** in place on purpose —
disabling them can break other workloads in a shared project (this mirrors
`infra/terraform/apis.tf`'s `disable_on_destroy = false`). Disable them by hand if
the project is disposable.
