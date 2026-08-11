# Cloud Monitoring: a Service, its availability + latency SLOs (with a 28-day
# rolling error budget), multi-window multi-burn-rate alert policies, and a
# golden-signals dashboard. All observability-as-code.
#
# The SLIs are computed from the native Prometheus request metrics the gateway
# exposes (internal/httpx/metrics.go), scraped into Managed Service for
# Prometheus by the PodMonitoring in deploy/. In Cloud Monitoring those series
# are:
#
#   prometheus.googleapis.com/http_requests_total/counter
#   prometheus.googleapis.com/http_request_duration_seconds/histogram
#
# scoped to the gateway via the metric.label."service"="gateway" constant label.

locals {
  # Base metric selectors (Monitoring filter syntax).
  gateway_requests_base = join(" ", [
    "metric.type=\"prometheus.googleapis.com/http_requests_total/counter\"",
    "resource.type=\"prometheus_target\"",
    "metric.label.\"service\"=\"gateway\"",
  ])

  gateway_latency_filter = join(" ", [
    "metric.type=\"prometheus.googleapis.com/http_request_duration_seconds/histogram\"",
    "resource.type=\"prometheus_target\"",
    "metric.label.\"service\"=\"gateway\"",
  ])

  # Latency objective expressed in seconds (the histogram's unit).
  latency_threshold_seconds = var.slo_latency_threshold_ms / 1000

  # Full resource names of the SLOs, used by the burn-rate alert filters.
  availability_slo_name = "projects/${var.project_id}/services/${google_monitoring_custom_service.gateway.service_id}/serviceLevelObjectives/${google_monitoring_slo.availability.slo_id}"
  latency_slo_name      = "projects/${var.project_id}/services/${google_monitoring_custom_service.gateway.service_id}/serviceLevelObjectives/${google_monitoring_slo.latency.slo_id}"
}

resource "google_monitoring_custom_service" "gateway" {
  service_id   = "${var.name_prefix}-gateway"
  display_name = "golden-signals gateway"

  depends_on = [google_project_service.enabled]
}

# --- Availability SLO: good = non-5xx responses. ---
resource "google_monitoring_slo" "availability" {
  service      = google_monitoring_custom_service.gateway.service_id
  slo_id       = "availability"
  display_name = "Availability - ${var.slo_availability_goal * 100}% of requests non-5xx"

  goal                = var.slo_availability_goal
  rolling_period_days = var.slo_rolling_period_days

  request_based_sli {
    good_total_ratio {
      total_service_filter = local.gateway_requests_base
      good_service_filter  = "${local.gateway_requests_base} metric.label.\"status_class\"!=\"5xx\""
    }
  }
}

# --- Latency SLO: good = requests served under the threshold. ---
resource "google_monitoring_slo" "latency" {
  service      = google_monitoring_custom_service.gateway.service_id
  slo_id       = "latency"
  display_name = "Latency - ${var.slo_latency_goal * 100}% of requests < ${var.slo_latency_threshold_ms}ms"

  goal                = var.slo_latency_goal
  rolling_period_days = var.slo_rolling_period_days

  request_based_sli {
    distribution_cut {
      distribution_filter = local.gateway_latency_filter
      range {
        min = 0
        max = local.latency_threshold_seconds
      }
    }
  }
}

# --- Multi-window multi-burn-rate alerts. ---
#
# Two policies per the Google SRE workbook. Each fires only when BOTH a long and
# a short window exceed the burn-rate threshold (combiner = AND), which gives a
# fast, low-false-positive signal:
#
#   fast burn (page):   14.4x over 1h AND 5m   (~2% of budget in 1h)
#   slow burn (ticket): 6x   over 6h AND 30m   (~5% of budget in 6h)
#
# Applied to both SLOs.

resource "google_monitoring_alert_policy" "availability_fast_burn" {
  display_name = "golden-signals availability fast burn (page)"
  combiner     = "AND"

  conditions {
    display_name = "1h burn rate > 14.4"
    condition_threshold {
      filter          = "select_slo_burn_rate(\"${local.availability_slo_name}\", \"3600s\")"
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
      filter          = "select_slo_burn_rate(\"${local.availability_slo_name}\", \"300s\")"
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
    content   = "Availability error budget burning fast (14.4x over 1h and 5m). Page: at this rate ~2% of the 28-day budget is spent in an hour."
    mime_type = "text/markdown"
  }
}

resource "google_monitoring_alert_policy" "availability_slow_burn" {
  display_name = "golden-signals availability slow burn (ticket)"
  combiner     = "AND"

  conditions {
    display_name = "6h burn rate > 6"
    condition_threshold {
      filter          = "select_slo_burn_rate(\"${local.availability_slo_name}\", \"21600s\")"
      comparison      = "COMPARISON_GT"
      threshold_value = 6
      duration        = "0s"
      aggregations {
        alignment_period   = "1800s"
        per_series_aligner = "ALIGN_MEAN"
      }
    }
  }

  conditions {
    display_name = "30m burn rate > 6"
    condition_threshold {
      filter          = "select_slo_burn_rate(\"${local.availability_slo_name}\", \"1800s\")"
      comparison      = "COMPARISON_GT"
      threshold_value = 6
      duration        = "0s"
      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_MEAN"
      }
    }
  }

  notification_channels = var.notification_channels

  documentation {
    content   = "Availability error budget burning steadily (6x over 6h and 30m). Open a ticket and investigate."
    mime_type = "text/markdown"
  }
}

resource "google_monitoring_alert_policy" "latency_fast_burn" {
  display_name = "golden-signals latency fast burn (page)"
  combiner     = "AND"

  conditions {
    display_name = "1h burn rate > 14.4"
    condition_threshold {
      filter          = "select_slo_burn_rate(\"${local.latency_slo_name}\", \"3600s\")"
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
      filter          = "select_slo_burn_rate(\"${local.latency_slo_name}\", \"300s\")"
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
    content   = "Latency error budget burning fast (14.4x over 1h and 5m). Too many requests slower than ${var.slo_latency_threshold_ms}ms."
    mime_type = "text/markdown"
  }
}

# --- Golden-signals dashboard. ---
resource "google_monitoring_dashboard" "golden_signals" {
  dashboard_json = jsonencode({
    displayName = "golden-signals - Golden Signals"
    mosaicLayout = {
      columns = 12
      tiles = [
        {
          width  = 6
          height = 4
          widget = {
            title = "Request rate (req/s) by status class"
            xyChart = {
              dataSets = [{
                timeSeriesQuery = {
                  timeSeriesFilter = {
                    filter = local.gateway_requests_base
                    aggregation = {
                      alignmentPeriod    = "60s"
                      perSeriesAligner   = "ALIGN_RATE"
                      crossSeriesReducer = "REDUCE_SUM"
                      groupByFields      = ["metric.label.\"status_class\""]
                    }
                  }
                }
                plotType = "LINE"
              }]
            }
          }
        },
        {
          xPos   = 6
          width  = 6
          height = 4
          widget = {
            title = "Error ratio (5xx / total)"
            xyChart = {
              dataSets = [{
                timeSeriesQuery = {
                  timeSeriesFilterRatio = {
                    numerator = {
                      filter = "${local.gateway_requests_base} metric.label.\"status_class\"=\"5xx\""
                      aggregation = {
                        alignmentPeriod  = "60s"
                        perSeriesAligner = "ALIGN_RATE"
                      }
                    }
                    denominator = {
                      filter = local.gateway_requests_base
                      aggregation = {
                        alignmentPeriod  = "60s"
                        perSeriesAligner = "ALIGN_RATE"
                      }
                    }
                  }
                }
                plotType = "LINE"
              }]
            }
          }
        },
        {
          yPos   = 4
          width  = 6
          height = 4
          widget = {
            title = "Latency p99 (seconds)"
            xyChart = {
              dataSets = [{
                timeSeriesQuery = {
                  timeSeriesFilter = {
                    filter = local.gateway_latency_filter
                    aggregation = {
                      alignmentPeriod    = "60s"
                      perSeriesAligner   = "ALIGN_PERCENTILE_99"
                      crossSeriesReducer = "REDUCE_MEAN"
                    }
                  }
                }
                plotType = "LINE"
              }]
            }
          }
        },
        {
          xPos   = 6
          yPos   = 4
          width  = 3
          height = 4
          widget = {
            title = "Availability SLO health"
            scorecard = {
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "select_slo_health(\"${local.availability_slo_name}\")"
                }
              }
              gaugeView = {
                lowerBound = 0.0
                upperBound = 1.0
              }
            }
          }
        },
        {
          xPos   = 9
          yPos   = 4
          width  = 3
          height = 4
          widget = {
            title = "Availability error budget remaining"
            scorecard = {
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "select_slo_budget_fraction(\"${local.availability_slo_name}\")"
                }
              }
              gaugeView = {
                lowerBound = 0.0
                upperBound = 1.0
              }
            }
          }
        },
      ]
    }
  })

  depends_on = [google_project_service.enabled]
}
