# Remote state in a GCS bucket.
#
# The bucket name is intentionally NOT hard-coded here: backend blocks cannot use
# variables, so the recommended flow is partial configuration. Create the bucket
# once (see infra/terraform/README.md), then pass its name at init time:
#
#   terraform init -backend-config="bucket=YOUR_TF_STATE_BUCKET"
#
# To validate or plan locally without any backend, initialise with -backend=false:
#
#   terraform init -backend=false
#
terraform {
  backend "gcs" {
    prefix = "golden-signals"
    # bucket = "..."   # supplied via -backend-config at init time.
  }
}
