// mgmt-cluster-exists.bicep
//
// Sentinel ARM step that establishes a stable topology barrier downstream of
// the management cluster step without inheriting its LRO state.
//
// The full management-cluster step (templates/mgmt-cluster.bicep) is a
// PUT against the AKS Managed Cluster and can fail when the underlying
// resource is in a degraded state (e.g. stuck nodepool update,
// OperationNotAllowed). When that PUT fails every downstream consumer that
// depends on the step is skipped even when the cluster itself is fully
// functional and only requires read-only access.
//
// This sentinel performs a read-only existing lookup of the AKS Managed
// Cluster and emits its name and resource id. The deployment is treated as
// successful whenever ARM can GET the cluster, regardless of whether the
// last LRO terminated in Succeeded or Failed. Downstream RBAC steps and
// external service rollouts (Cluster Service, HyperShift Operator) can
// depend on this step instead of the management-cluster step to ride out
// transient PUT failures without losing safety: if the cluster has never
// been provisioned the GET will 404 and the sentinel will fail, gating the
// rest of the pipeline correctly.
//
// See: AROSLSRE-904 (decouple service rollouts from AKS MC ARM LRO state).

@description('The name of the AKS management cluster.')
param aksClusterName string

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-10-01' existing = {
  name: aksClusterName
}

output aksClusterName string = aksCluster.name
output aksClusterId string = aksCluster.id
// Emitting properties.provisioningState forces ARM to perform a GET on the
// cluster, which fails if the cluster does not exist. Crucially, the GET
// succeeds for any provisioningState (Succeeded, Failed, Updating, Canceled),
// so the sentinel rides out degraded LRO states.
output aksClusterProvisioningState string = aksCluster.properties.provisioningState
