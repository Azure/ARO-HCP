#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource etcdAvailability 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'etcd-availability'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdBackendCommitDurationHigh1h5m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdBackendCommitDurationHigh1h5m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile backend commit duration of {{ $value }}s (threshold: 25ms). Slow disk performance may impact write performance. Fast burn (1h/5m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile backend commit duration of {{ $value }}s (threshold: 25ms). Slow disk performance may impact write performance. Fast burn (1h/5m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd backend commit duration is high for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd backend commit duration is high for {{ $labels.resource_id }}'
        }
        expression: 'histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_backend_commit_duration_seconds_bucket{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[5m]))) > 0.025 and histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_backend_commit_duration_seconds_bucket{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[1h]))) > 0.025'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdBackendCommitDurationHigh6h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdBackendCommitDurationHigh6h30m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile backend commit duration of {{ $value }}s (threshold: 25ms). Slow disk performance may impact write performance. Medium burn (6h/30m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile backend commit duration of {{ $value }}s (threshold: 25ms). Slow disk performance may impact write performance. Medium burn (6h/30m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd backend commit duration is high for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd backend commit duration is high for {{ $labels.resource_id }}'
        }
        expression: 'histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_backend_commit_duration_seconds_bucket{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[30m]))) > 0.025 and histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_backend_commit_duration_seconds_bucket{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[6h]))) > 0.025'
        for: 'PT30M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdBackendCommitDurationHigh3d'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
        }
        annotations: {
          correlationId: 'userJourneyEtcdBackendCommitDurationHigh3d/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile backend commit duration of {{ $value }}s (threshold: 25ms). Slow disk performance may impact write performance. Slow burn (3d).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile backend commit duration of {{ $value }}s (threshold: 25ms). Slow disk performance may impact write performance. Slow burn (3d).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd backend commit duration is chronically high for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd backend commit duration is chronically high for {{ $labels.resource_id }}'
        }
        expression: 'histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_backend_commit_duration_seconds_bucket{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[6h]))) > 0.025'
        for: 'PT6H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdDatabaseHighFragmentation1h5m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdDatabaseHighFragmentation1h5m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database in-use ratio is {{ $value | humanizePercentage }} (threshold: 50%). Database defragmentation may be needed to reclaim space. Fast burn (1h/5m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database in-use ratio is {{ $value | humanizePercentage }} (threshold: 50%). Database defragmentation may be needed to reclaim space. Fast burn (1h/5m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd database fragmentation is high for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd database fragmentation is high for {{ $labels.resource_id }}'
        }
        expression: '(etcd_mvcc_db_total_size_in_use_in_bytes{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"} / etcd_mvcc_db_total_size_in_bytes{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}) < 0.5'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdDatabaseHighFragmentation6h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdDatabaseHighFragmentation6h30m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database in-use ratio is {{ $value | humanizePercentage }} (threshold: 50%). Database defragmentation may be needed to reclaim space. Medium burn (6h/30m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database in-use ratio is {{ $value | humanizePercentage }} (threshold: 50%). Database defragmentation may be needed to reclaim space. Medium burn (6h/30m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd database fragmentation is high for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd database fragmentation is high for {{ $labels.resource_id }}'
        }
        expression: '(etcd_mvcc_db_total_size_in_use_in_bytes{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"} / etcd_mvcc_db_total_size_in_bytes{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}) < 0.5'
        for: 'PT30M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdDatabaseHighFragmentation3d'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
        }
        annotations: {
          correlationId: 'userJourneyEtcdDatabaseHighFragmentation3d/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database in-use ratio is {{ $value | humanizePercentage }} (threshold: 50%). Database defragmentation may be needed to reclaim space. Slow burn (3d).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database in-use ratio is {{ $value | humanizePercentage }} (threshold: 50%). Database defragmentation may be needed to reclaim space. Slow burn (3d).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd database fragmentation is chronically high for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd database fragmentation is chronically high for {{ $labels.resource_id }}'
        }
        expression: '(etcd_mvcc_db_total_size_in_use_in_bytes{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"} / etcd_mvcc_db_total_size_in_bytes{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}) < 0.5'
        for: 'PT6H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdDatabaseSizeExceeded1h5m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdDatabaseSizeExceeded1h5m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database size is {{ $value | humanize }}B (threshold: 8GB). Database may need compaction or quota increase. Fast burn (1h/5m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database size is {{ $value | humanize }}B (threshold: 8GB). Database may need compaction or quota increase. Fast burn (1h/5m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd database size is too large for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd database size is too large for {{ $labels.resource_id }}'
        }
        expression: 'etcd_mvcc_db_total_size_in_bytes{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"} > 8589934592'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdDatabaseSizeExceeded6h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdDatabaseSizeExceeded6h30m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database size is {{ $value | humanize }}B (threshold: 8GB). Database may need compaction or quota increase. Medium burn (6h/30m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database size is {{ $value | humanize }}B (threshold: 8GB). Database may need compaction or quota increase. Medium burn (6h/30m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd database size is too large for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd database size is too large for {{ $labels.resource_id }}'
        }
        expression: 'etcd_mvcc_db_total_size_in_bytes{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"} > 8589934592'
        for: 'PT30M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdDatabaseSizeExceeded3d'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
        }
        annotations: {
          correlationId: 'userJourneyEtcdDatabaseSizeExceeded3d/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database size is {{ $value | humanize }}B (threshold: 8GB). Database may need compaction or quota increase. Slow burn (3d).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database size is {{ $value | humanize }}B (threshold: 8GB). Database may need compaction or quota increase. Slow burn (3d).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd database size is chronically too large for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd database size is chronically too large for {{ $labels.resource_id }}'
        }
        expression: 'etcd_mvcc_db_total_size_in_bytes{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"} > 8589934592'
        for: 'PT6H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdFrequentLeaderChanges1h5m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdFrequentLeaderChanges1h5m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has seen {{ $value }} leader changes in the last 15 minutes (threshold: 3). This may indicate network issues or cluster instability. Fast burn (1h/5m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has seen {{ $value }} leader changes in the last 15 minutes (threshold: 3). This may indicate network issues or cluster instability. Fast burn (1h/5m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd cluster experiencing frequent leader changes for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd cluster experiencing frequent leader changes for {{ $labels.resource_id }}'
        }
        expression: 'increase(etcd_server_leader_changes_seen_total{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[15m]) > 3'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdFrequentLeaderChanges6h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdFrequentLeaderChanges6h30m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has seen {{ $value }} leader changes in the last 15 minutes (threshold: 3). This may indicate network issues or cluster instability. Medium burn (6h/30m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has seen {{ $value }} leader changes in the last 15 minutes (threshold: 3). This may indicate network issues or cluster instability. Medium burn (6h/30m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd cluster experiencing frequent leader changes for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd cluster experiencing frequent leader changes for {{ $labels.resource_id }}'
        }
        expression: 'increase(etcd_server_leader_changes_seen_total{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[15m]) > 3'
        for: 'PT30M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdFrequentLeaderChanges3d'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
        }
        annotations: {
          correlationId: 'userJourneyEtcdFrequentLeaderChanges3d/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has seen {{ $value }} leader changes in the last 15 minutes (threshold: 3). This may indicate network issues or cluster instability. Slow burn (3d).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has seen {{ $value }} leader changes in the last 15 minutes (threshold: 3). This may indicate network issues or cluster instability. Slow burn (3d).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd cluster experiencing chronic frequent leader changes for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd cluster experiencing chronic frequent leader changes for {{ $labels.resource_id }}'
        }
        expression: 'increase(etcd_server_leader_changes_seen_total{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[15m]) > 3'
        for: 'PT6H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdPeerRoundTripTimeHigh1h5m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdPeerRoundTripTimeHigh1h5m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Fast burn (1h/5m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Fast burn (1h/5m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd peer round-trip time is high for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd peer round-trip time is high for {{ $labels.resource_id }}'
        }
        expression: 'histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_network_peer_round_trip_time_seconds_bucket{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[5m]))) > 0.1 and histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_network_peer_round_trip_time_seconds_bucket{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[1h]))) > 0.1'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdPeerRoundTripTimeHigh6h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdPeerRoundTripTimeHigh6h30m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Medium burn (6h/30m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Medium burn (6h/30m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd peer round-trip time is high for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd peer round-trip time is high for {{ $labels.resource_id }}'
        }
        expression: 'histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_network_peer_round_trip_time_seconds_bucket{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[30m]))) > 0.1 and histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_network_peer_round_trip_time_seconds_bucket{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[6h]))) > 0.1'
        for: 'PT30M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdPeerRoundTripTimeHigh3d'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
        }
        annotations: {
          correlationId: 'userJourneyEtcdPeerRoundTripTimeHigh3d/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Slow burn (3d).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Slow burn (3d).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd peer round-trip time is chronically high for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd peer round-trip time is chronically high for {{ $labels.resource_id }}'
        }
        expression: 'histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_network_peer_round_trip_time_seconds_bucket{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[6h]))) > 0.1'
        for: 'PT6H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdWALFsyncDurationHigh1h5m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdWALFsyncDurationHigh1h5m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 10ms). Slow disk performance may impact cluster stability. Fast burn (1h/5m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 10ms). Slow disk performance may impact cluster stability. Fast burn (1h/5m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd WAL fsync duration is high for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd WAL fsync duration is high for {{ $labels.resource_id }}'
        }
        expression: 'histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_wal_fsync_duration_seconds_bucket{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[5m]))) > 0.01 and histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_wal_fsync_duration_seconds_bucket{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[1h]))) > 0.01'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdWALFsyncDurationHigh6h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdWALFsyncDurationHigh6h30m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 10ms). Slow disk performance may impact cluster stability. Medium burn (6h/30m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 10ms). Slow disk performance may impact cluster stability. Medium burn (6h/30m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd WAL fsync duration is high for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd WAL fsync duration is high for {{ $labels.resource_id }}'
        }
        expression: 'histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_wal_fsync_duration_seconds_bucket{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[30m]))) > 0.01 and histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_wal_fsync_duration_seconds_bucket{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[6h]))) > 0.01'
        for: 'PT30M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyEtcdWALFsyncDurationHigh3d'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
        }
        annotations: {
          correlationId: 'userJourneyEtcdWALFsyncDurationHigh3d/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 10ms). Slow disk performance may impact cluster stability. Slow burn (3d).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 10ms). Slow disk performance may impact cluster stability. Slow burn (3d).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '{{ $labels.instance }} etcd WAL fsync duration is chronically high for {{ $labels.resource_id }}'
          title: '{{ $labels.instance }} etcd WAL fsync duration is chronically high for {{ $labels.resource_id }}'
        }
        expression: 'histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_wal_fsync_duration_seconds_bucket{subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}[6h]))) > 0.01'
        for: 'PT6H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
