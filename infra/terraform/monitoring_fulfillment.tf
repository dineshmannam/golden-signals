# Cloud Monitoring for the asynchronous fulfillment path. This mirrors the
# gateway SLO pattern in monitoring.tf but keys off the worker's own RED metric
# (internal/pubsubx/metrics.go), scraped by the fulfillment PodMonitoring. It does
# NOT touch the existing gateway metrics/labels the gateway SLOs depend on.
#
# In Cloud Monitoring the worker's counter surfaces as:
#
#   prometheus.googleapis.com/messages_processed_total/counter
#
# scoped to the worker via metric.label."service"="fulfillment" and split good vs
# bad by metric.label."result".

locals {
  fulfillment_processed_base = join(" ", [
    "metric.type=\"prometheus.googleapis.com/messages_processed_total/counter\"",
    "resource.type=\"prometheus_target\"",
    "metric.label.\"service\"=\"fulfillment\"",
  ])

  # Guarded on var.create_slos for the same reason as the gateway SLO names in
  # monitoring.tf: the fulfillment SLO keys off messages_processed_total, which
  # only exists once the worker has processed traffic, so it is a post-deploy
  # (Step 3) resource. Only the gated fast-burn alert consumes this local.
  fulfillment_slo_name = var.create_slos ? "projects/${var.project_id}/services/${google_monitoring_custom_service.fulfillment[0].service_id}/serviceLevelObjectives/${google_monitoring_slo.fulfillment_success[0].slo_id}" : ""
}

resource "google_monitoring_custom_service" "fulfillment" {
  count = var.create_slos ? 1 : 0

  service_id   = "${var.name_prefix}-fulfillment"
  display_name = "golden-signals fulfillment"

  depends_on = [google_project_service.enabled]
}

# --- Processing-success SLO: good = messages processed without error. ---
resource "google_monitoring_slo" "fulfillment_success" {
  count = var.create_slos ? 1 : 0

  service      = google_monitoring_custom_service.fulfillment[0].service_id
  slo_id       = "processing-success"
  display_name = "Fulfillment - ${var.slo_fulfillment_goal * 100}% of messages processed successfully"

  goal                = var.slo_fulfillment_goal
  rolling_period_days = var.slo_rolling_period_days

  request_based_sli {
    good_total_ratio {
      total_service_filter = local.fulfillment_processed_base
      good_service_filter  = "${local.fulfillment_processed_base} metric.label.\"result\"=\"success\""
    }
  }
}

# --- Fast-burn alert (page) on the fulfillment error budget. ---
resource "google_monitoring_alert_policy" "fulfillment_fast_burn" {
  count = var.create_slos ? 1 : 0

  display_name = "golden-signals fulfillment fast burn (page)"
  combiner     = "AND"

  conditions {
    display_name = "1h burn rate > 14.4"
    condition_threshold {
      filter          = "select_slo_burn_rate(\"${local.fulfillment_slo_name}\", \"3600s\")"
      comparison      = "COMPARISON_GT"
      threshold_value = 14.4
      duration        = "0s"
      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_MEAN"
      }
    }
  }

  conditions {
    display_name = "5m burn rate > 14.4"
    condition_threshold {
      filter          = "select_slo_burn_rate(\"${local.fulfillment_slo_name}\", \"300s\")"
      comparison      = "COMPARISON_GT"
      threshold_value = 14.4
      duration        = "0s"
      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_MEAN"
      }
    }
  }

  notification_channels = var.notification_channels

  documentation {
    content   = "Fulfillment error budget burning fast (14.4x over 1h and 5m). Messages are failing to process and heading for the dead-letter topic."
    mime_type = "text/markdown"
  }
}
