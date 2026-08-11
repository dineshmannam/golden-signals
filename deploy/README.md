# Deploy: Config Sync GitOps

The cluster is reconciled from this directory by **Config Sync**. `RootSync`
watches this repo and applies `deploy/overlays/prod` (a Kustomize overlay over
`deploy/base`).

## Layout

```
deploy/
├── rootsync.yaml            # RootSync: repo + branch + dir the cluster syncs
├── base/                    # environment-agnostic manifests
│   ├── namespace.yaml
│   ├── serviceaccounts.yaml # KSAs with Workload Identity annotations
│   ├── collector-rbac.yaml  # RBAC for the collector's k8sattributes processor
│   ├── collector-configmap.yaml
│   ├── collector.yaml       # OTel Collector DaemonSet (node-local agent)
│   ├── orders.yaml          # Deployment + Service
│   ├── gateway.yaml         # Deployment + Service (LoadBalancer)
│   ├── podmonitoring.yaml   # GMP scrape configs for /metrics
│   └── kustomization.yaml
└── overlays/prod/
    └── kustomization.yaml   # sets images (Artifact Registry) + tags
```

Render locally to check it:

```bash
kubectl kustomize deploy/overlays/prod
```

## One-time setup before Config Sync

1. **Fill in your project ID.** The KSAs reference GSA emails containing the
   project. Replace the placeholder in your fork:

   ```bash
   sed -i '' 's/PROJECT_ID/your-gcp-project-id/g' \
     deploy/base/serviceaccounts.yaml deploy/overlays/prod/kustomization.yaml
   # (GNU sed: drop the '' after -i)
   ```

   The GSA emails must match what Terraform created — see
   `terraform output service_accounts`.

2. **Set the image tags.** In `overlays/prod/kustomization.yaml`, point
   `newName` at your Artifact Registry repo and `newTag` at a built tag (e.g. the
   commit SHA from Cloud Build).

3. **Create the database Secret.** `orders` reads `DATABASE_URL` from a Secret
   named `orders-db`. Populate it from the Secret Manager value Terraform stored:

   ```bash
   DB_URL=$(gcloud secrets versions access latest \
     --secret="$(cd infra/terraform && terraform output -raw database_url_secret)")
   kubectl create namespace golden-signals --dry-run=client -o yaml | kubectl apply -f -
   kubectl -n golden-signals create secret generic orders-db \
     --from-literal=url="$DB_URL"
   ```

   (In production, prefer the Secret Manager CSI driver or External Secrets so the
   value is never handled manually.)

## Turn on Config Sync

Enable Config Sync on the cluster (via the GKE Enterprise/Config Management
feature), set your fork in `rootsync.yaml` (`GITHUB_OWNER`), then apply it:

```bash
kubectl apply -f deploy/rootsync.yaml
```

Config Sync then continuously reconciles `deploy/overlays/prod`. Check status:

```bash
kubectl get rootsync -n config-management-system
```

## Notes

- The collector runs as a **DaemonSet**; pods send OTLP to their node's collector
  via `status.hostIP:4317`. Workload Identity lets the collector write to Cloud
  Trace/Logging/Monitoring with no keys.
- `deploy/base/collector-configmap.yaml` mirrors `otel/collector-config.yaml`
  (the standalone copy). Keep them in sync.
- The gateway Service is a `LoadBalancer` for a quick public IP. Put it behind a
  GKE Gateway/Ingress with TLS for anything real.
