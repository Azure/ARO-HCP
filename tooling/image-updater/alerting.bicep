// Prometheus alert rules for the image-updater Prow periodic jobs in the
// opstool environment. Consumes metrics from the image-updater monitor
// exporter (tooling/image-updater `monitor` subcommand) and fires through
// the shared Action Group from the Infra pipeline.

@description('Azure Monitor Workspace resource ID')
param azureMonitorWorkspaceId string

@description('Shared Action Group resource ID from Infra pipeline')
param sharedActionGroupId string

@description('Enable or disable alerting (leave false while validating rules)')
param alertingEnabled bool = false

@description('Consecutive failed/errored completed runs before alerting; the job runs 3x/day on weekdays, so 3 is roughly one full weekday of failed runs')
param maxConsecutiveFailures int = 3

@description('Maximum expected duration in seconds of a single image-updater run')
param maxRunDurationSeconds int = 3600

@description('Maximum time in seconds between completed runs on Tue-Fri before a run is considered stalled; 9h covers the 8h gap between the 06:00/14:00/22:00 schedule plus run time')
param weekdayStallSeconds int = 32400

@description('Maximum time in seconds between completed runs before a Monday alert; the job does not run on weekends, so the real gap from Friday ~22:25 completion to Monday ~06:25 completion is ~56h. 58h leaves ~2h grace for the Monday 06:00 run before firing ~08:00 UTC. Backtested against all 120 runs Sep-Nov 2026: 34h (an earlier value) fired at Monday 00:00 every week.')
param mondayStallSeconds int = 208800

// day_of_week(): Sunday=0, Monday=1 ... Saturday=6. The stall rule is
// day-aware rather than hour-gated: an hour gate would false-fire Monday
// mornings, when elapsed time since Friday's last completion already
// exceeds the weekday threshold while the 06:00 run is still in flight.
var weekdayStallGate = 'day_of_week() >= 2 and day_of_week() <= 5'
var mondayStallGate = 'day_of_week() == 1'

// The managed Prometheus agent scrapes each target from two replicas, and the
// AMW stores both series (differing only in the prometheus_replica label).
// Every rule expression therefore aggregates that label away: without this,
// a breaching metric yields one alert instance per replica and the action
// group is notified twice per firing.
var replicaDedup = 'max without (prometheus_replica)'

resource imageUpdaterAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'image-updater-alerts'
  location: resourceGroup().location
  properties: {
    enabled: alertingEnabled
    interval: 'PT1M'
    scopes: [
      azureMonitorWorkspaceId
    ]
    rules: [
      {
        alert: 'ImageUpdaterConsecutiveFailures'
        enabled: true
        expression: '${replicaDedup} (image_updater_consecutive_failures) >= ${maxConsecutiveFailures}'
        for: 'PT15M'
        severity: 3
        labels: {
          severity: 'warning'
        }
        annotations: {
          summary: 'image-updater Prow job has ${maxConsecutiveFailures}+ consecutive failed runs'
          description: '{{ $labels.job_name }} has failed ${maxConsecutiveFailures} or more consecutive scheduled runs (job runs 06:00/14:00/22:00 UTC on weekdays). Most recent failing run: {{ $labels.url }} (build {{ $labels.build_id }}). Failure reason: {{ $labels.failure_reason }}.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md'
        }
        actions: [
          {
            actionGroupId: sharedActionGroupId
          }
        ]
        resolveConfiguration: {
          autoResolved: true
          timeToResolve: 'PT10M'
        }
      }
      {
        alert: 'ImageUpdaterRunStalled'
        enabled: true
        expression: '((time() - ${replicaDedup} (image_updater_last_run_timestamp_seconds) > ${weekdayStallSeconds}) and on() (${weekdayStallGate})) or ((time() - ${replicaDedup} (image_updater_last_run_timestamp_seconds) > ${mondayStallSeconds}) and on() (${mondayStallGate}))'
        for: 'PT30M'
        severity: 3
        labels: {
          severity: 'warning'
        }
        annotations: {
          summary: 'image-updater Prow job has not completed a run in over the schedule-derived stall threshold'
          description: '{{ $labels.job_name }} last completed a run {{ $value | humanizeDuration }} ago, longer than the schedule allows (schedule: 06:00/14:00/22:00 UTC weekdays, none Sat/Sun; thresholds: ${weekdayStallSeconds}s Tue-Fri, ${mondayStallSeconds}s Monday spanning the ~56h weekend gap). The current run may be hung, e.g. waiting on GitHub or Tide state. Job history: https://prow.ci.openshift.org/?job={{ $labels.job_name }}.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md'
        }
        actions: [
          {
            actionGroupId: sharedActionGroupId
          }
        ]
        resolveConfiguration: {
          autoResolved: true
          timeToResolve: 'PT10M'
        }
      }
      {
        alert: 'ImageUpdaterRunDurationExceeded'
        enabled: true
        expression: '${replicaDedup} (image_updater_last_run_duration_seconds) > ${maxRunDurationSeconds}'
        for: 'PT15M'
        severity: 3
        labels: {
          severity: 'warning'
        }
        annotations: {
          summary: 'image-updater Prow job run exceeded ${maxRunDurationSeconds}s'
          description: 'The most recent run of {{ $labels.job_name }} took {{ $value | humanizeDuration }}, longer than the expected maximum of ${maxRunDurationSeconds}s. Offending run: {{ $labels.url }} (build {{ $labels.build_id }}).'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md'
        }
        actions: [
          {
            actionGroupId: sharedActionGroupId
          }
        ]
        resolveConfiguration: {
          autoResolved: true
          timeToResolve: 'PT10M'
        }
      }
      {
        alert: 'ImageUpdaterScrapeFailed'
        enabled: true
        expression: 'absent_over_time(image_updater_scrape_success[15m]) == 1 or ${replicaDedup} (image_updater_scrape_success) == 0 or (time() - ${replicaDedup} (image_updater_last_scrape_timestamp_seconds) > 900)'
        for: 'PT10M'
        severity: 3
        labels: {
          severity: 'warning'
        }
        annotations: {
          summary: 'image-updater monitor exporter is unhealthy'
          description: 'The image-updater monitor exporter has failed its Prow poll, has not scraped in over 15 minutes, or has stopped emitting metrics entirely (pod down or scrape target lost); image-updater alerts cannot be trusted while this fires.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md'
        }
        actions: [
          {
            actionGroupId: sharedActionGroupId
          }
        ]
        resolveConfiguration: {
          autoResolved: true
          timeToResolve: 'PT10M'
        }
      }
      {
        alert: 'ImageUpdaterMonitorBlind'
        enabled: true
        // scrape_success == 1 separates "regex matches nothing" (this alert)
        // from "poll failing" (ImageUpdaterScrapeFailed).
        expression: '${replicaDedup} (image_updater_jobs_matched) == 0 and on() (${replicaDedup} (image_updater_scrape_success) == 1)'
        for: 'PT30M'
        severity: 3
        labels: {
          severity: 'warning'
        }
        annotations: {
          summary: 'image-updater monitor is watching zero Prow jobs'
          description: 'The monitor is polling Prow successfully but no monitored job has a completed run. Either the Prow job was renamed (check https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-image-updater), the jobs aged out of Prow retention, the monitor job-regex flag is wrong, or every recent run is hung and never completed (check the job history for a run pending for hours). All other image-updater alerts are blind while this fires.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md'
        }
        actions: [
          {
            actionGroupId: sharedActionGroupId
          }
        ]
        resolveConfiguration: {
          autoResolved: true
          timeToResolve: 'PT10M'
        }
      }
    ]
  }
}

output imageUpdaterAlertRuleGroupId string = imageUpdaterAlerts.id
