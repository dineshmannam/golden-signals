# Pub/Sub for the asynchronous streaming path:
#
#   orders --(publish OrderCreated)--> topic ---> subscription ---> fulfillment
#                                                     |
#                                          (after N failed deliveries)
#                                                     v
#                                       dead-letter topic ---> dead-letter subscription
#
# IAM is least-privilege and per-resource: orders may only publish to the topic,
# fulfillment may only subscribe to the subscription. The Pub/Sub service agent
# needs publish/subscribe rights to move messages onto the dead-letter topic.

# --- Main topic + fulfillment subscription. ---
resource "google_pubsub_topic" "order_created" {
  name = var.pubsub_topic_order_created

  depends_on = [google_project_service.enabled]
}

resource "google_pubsub_subscription" "order_created_fulfillment" {
  name  = var.pubsub_subscription_order_created
  topic = google_pubsub_topic.order_created.id

  ack_deadline_seconds       = var.pubsub_ack_deadline_seconds
  message_retention_duration = "604800s" # 7 days

  # Exponential backoff between redeliveries of a Nacked message.
  retry_policy {
    minimum_backoff = "10s"
    maximum_backoff = "600s"
  }

  # After max_delivery_attempts failed deliveries, route the message to the DLQ
  # instead of redelivering forever. This is the poison-message escape hatch.
  dead_letter_policy {
    dead_letter_topic     = google_pubsub_topic.order_created_dead_letter.id
    max_delivery_attempts = var.pubsub_dead_letter_max_attempts
  }
}

# --- Dead-letter topic + subscription (so failed messages are retained). ---
resource "google_pubsub_topic" "order_created_dead_letter" {
  name = "${var.pubsub_topic_order_created}-dead-letter"

  depends_on = [google_project_service.enabled]
}

resource "google_pubsub_subscription" "order_created_dead_letter" {
  name  = "${var.pubsub_subscription_order_created}-dead-letter"
  topic = google_pubsub_topic.order_created_dead_letter.id

  # Keep dead-lettered messages long enough to inspect/replay them.
  message_retention_duration = "604800s" # 7 days
  ack_deadline_seconds       = var.pubsub_ack_deadline_seconds
}

# --- Application IAM: orders publishes, fulfillment subscribes. ---
resource "google_pubsub_topic_iam_member" "orders_publish" {
  topic  = google_pubsub_topic.order_created.name
  role   = "roles/pubsub.publisher"
  member = "serviceAccount:${google_service_account.orders.email}"
}

resource "google_pubsub_subscription_iam_member" "fulfillment_subscribe" {
  subscription = google_pubsub_subscription.order_created_fulfillment.name
  role         = "roles/pubsub.subscriber"
  member       = "serviceAccount:${google_service_account.fulfillment.email}"
}

# --- Pub/Sub service agent IAM for dead-lettering. ---
#
# To move a message to the dead-letter topic, the Pub/Sub service account must be
# able to publish to that topic and to acknowledge on the source subscription.
locals {
  pubsub_service_agent = "serviceAccount:service-${data.google_project.this.number}@gcp-sa-pubsub.iam.gserviceaccount.com"
}

resource "google_pubsub_topic_iam_member" "dead_letter_publisher" {
  topic  = google_pubsub_topic.order_created_dead_letter.name
  role   = "roles/pubsub.publisher"
  member = local.pubsub_service_agent
}

resource "google_pubsub_subscription_iam_member" "dead_letter_acker" {
  subscription = google_pubsub_subscription.order_created_fulfillment.name
  role         = "roles/pubsub.subscriber"
  member       = local.pubsub_service_agent
}
