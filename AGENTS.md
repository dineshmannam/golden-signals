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
- **Streaming path (orders → Pub/Sub → fulfillment):** trace context crosses the queue via `internal/pubsubx` — `InjectTraceContext` on publish, `ExtractTraceContext` on receive. This depends on the global `TextMapPropagator` (TraceContext) set in `internal/telemetry.Setup`; don't remove it or the trace breaks at the async boundary. The `OrderCreated` event schema is shared in `internal/events` — change both producer (`services/orders`) and consumer (`services/fulfillment`) together.
- The `orders` table's `fulfilled_at` column is written by `fulfillment` but the schema is owned by `orders` (`services/orders/store.go` `Migrate`). `MarkFulfilled` is idempotent (`WHERE fulfilled_at IS NULL`) because Pub/Sub is at-least-once.
- The fulfillment SLO keys off a **separate** native metric, `messages_processed_total` (`result` label), registered in `internal/pubsubx/metrics.go` and scoped by a constant `service="fulfillment"` label. It is defined in `infra/terraform/monitoring_fulfillment.tf`, kept apart from the gateway SLO metrics so neither renames the other's series.
- Local `docker compose` runs a Pub/Sub emulator; the one-shot `pubsub-init` service creates the topic/subscription/DLQ (mirror of `infra/terraform/pubsub.tf`) — keep the two in sync when changing names/attempts.
- Scope boundary: gateway + orders + fulfillment + Postgres + Pub/Sub (topic/sub/DLQ) + OTel + SLOs + Config Sync + Cloud Build. GCP bootstrap (state bucket, provisioning SA) and the deploy pipeline / Cloud Build trigger are owned by the operator — not in this repo.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
