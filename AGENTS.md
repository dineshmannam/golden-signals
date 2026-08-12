# Project agent memory

This file is the project's committed home for project-intrinsic agent knowledge: build, test, release, architecture, and sharp-edge notes that should travel with the code.

- Add durable project-specific notes here as they are discovered through real work.

## Build & test

- Single Go module (`github.com/dineshmannam/golden-signals`, Go 1.21). `go build ./...`, `go test ./...`, `gofmt -l .` from the repo root.
- **macOS/darwin-arm64 gotcha:** the Go 1.21 internal linker produces test binaries that fail at runtime here with `dyld: missing LC_UUID load command`. Locally run tests with `go test -ldflags=-linkmode=external ./...`. Linux CI (Cloud Build `golang:1.21`) is unaffected — do not add this flag to `cloudbuild.yaml`.
- Terraform (in `infra/terraform/`): `terraform fmt -check -recursive`; validate offline with `terraform init -backend=false && terraform validate`. Never `apply` from here — no GCP creds; the operator/Cloud Build applies.
- Deploy tooling: `bootstrap.sh` (APIs + state bucket + provisioning SA, `--destroy` teardown), `scripts/create-triggers.sh` (2nd-gen Cloud Build infra/app triggers), `cloudbuild-infra.yaml` (Terraform pipeline). Full flow in `DEPLOY.md`. Shell scripts must stay `shellcheck`-clean and run on macOS bash 3.2 (guard empty-array expansion with `${arr[@]+"${arr[@]}"}`). The operator runs them — no GCP auth here.
- K8s manifests: `kubectl kustomize deploy/overlays/prod` must render clean. `kubeconform`/`kustomize` binaries are not installed in this env.

## Architecture / sharp edges

- `internal/httpx.Handler` middleware order is deliberate: access-log/metrics wrap the fault layer so fault-injected aborts are still counted in the golden signals. Don't reorder.
- SLOs (in `infra/terraform/monitoring.tf`) key off the **native Prometheus** metrics registered in `internal/httpx/metrics.go` (`http_requests_total`, `http_request_duration_seconds`) with a constant `service` label, scoped to `gateway`. Renaming those metrics/labels breaks the SLO filters.
- `deploy/base/collector-configmap.yaml` duplicates `otel/collector-config.yaml` — keep the two in sync.
- Placeholders the operator replaces per-fork: `PROJECT_ID` in `deploy/`, `YOUR_TF_STATE_BUCKET`, `GITHUB_OWNER` in `deploy/rootsync.yaml`. Project ID is a Terraform variable — never hard-code a real one.
- Scope boundary: foundation only (gateway + orders + Postgres + OTel + SLOs + Config Sync + Cloud Build). The Pub/Sub `fulfillment` worker is intentionally out of scope.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
