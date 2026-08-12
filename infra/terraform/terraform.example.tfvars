# Copy to terraform.tfvars (gitignored) and fill in your values.
#   cp terraform.example.tfvars terraform.tfvars

# REQUIRED: the GCP project to deploy into. There is no default; never commit a
# real project ID.
project_id = "your-gcp-project-id"

# Region for all regional resources. us-central1 is the project default.
region = "us-central1"

# Optional overrides (defaults shown):
# name_prefix              = "golden-signals"
# gke_release_channel      = "REGULAR"
# kubernetes_namespace     = "golden-signals"
# postgres_version         = "POSTGRES_16"
# postgres_tier            = "db-custom-1-3840"
# deletion_protection      = true

# Pub/Sub streaming path (names must match the services' env; defaults shown):
# pubsub_topic_order_created        = "order-created"
# pubsub_subscription_order_created = "order-created-fulfillment"
# pubsub_dead_letter_max_attempts   = 5
# pubsub_ack_deadline_seconds       = 30

# SLO targets:
# slo_availability_goal    = 0.99
# slo_latency_goal         = 0.99
# slo_latency_threshold_ms = 300
# slo_fulfillment_goal     = 0.99
# slo_rolling_period_days  = 28

# Cloud Monitoring notification channels for the alert policies. Create channels
# first (email/PagerDuty/etc.), then reference their IDs here.
# notification_channels = ["projects/your-gcp-project-id/notificationChannels/1234567890"]
