@description('Resource ID of the Azure Monitor Workspace the inventory series is emitted into')
param azureMonitoringWorkspaceId string

@description('Name of the underlay (service or management) cluster this series represents. Must match the `cluster` external label that this cluster\'s Prometheus stamps onto its metrics.')
param clusterName string

// Emits a static `underlay_clusters{cluster="<name>", source="bicep"} = 1` series for the
// service or management cluster this deployment owns. Together, across every cluster deployment,
// these series form the authoritative list -- declared at deploy time -- of which underlay
// clusters should be running. Alerts join against it to detect a cluster that has gone
// completely absent from the workspace (a case a plain `up`-based alert cannot see, because a
// vanished cluster reports no `up` series at all).
//
// This module is instantiated from each cluster's own template (svc-cluster.bicep,
// mgmt-cluster.bicep), so the series is tied to that cluster's lifecycle: when a (stamped)
// cluster is decommissioned, its inventory series is torn down with it and the absence alert
// does not false-fire for an intentionally-removed cluster.
//
// `vector(1)` produces a constant with no labels; the `labels` block stamps the cluster identity
// onto it. The `source="bicep"` label distinguishes this declared-inventory series from any
// metric the cluster reports about itself.
resource underlayClusterInventory 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'underlay-clusters-metric-${clusterName}'
  location: resourceGroup().location
  properties: {
    description: 'Authoritative list entry declaring that underlay cluster ${clusterName} should be running.'
    scopes: [
      azureMonitoringWorkspaceId
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'underlay_clusters'
        expression: 'vector(1)'
        labels: {
          cluster: clusterName
          source: 'bicep'
        }
      }
    ]
  }
}
