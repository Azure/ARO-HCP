Some notes on how to use status in backup controller
---
Existing Validation Examples

The ServiceProviderClusterStatus.Validations[] has these concrete validation types, each implemented as a ClusterValidation
interface:

Validation Name: AzureResourceProvidersRegistrationValidation
File: validations/azure_rp_registration_validation.go
What it checks: Microsoft.Authorization, Compute, Network, Storage are registered in the subscription
────────────────────────────────────────
Validation Name: AzureClusterResourceGroupExistenceValidation
File: validations/azure_cluster_resource_group_existence_validation.go
What it checks: The cluster's resource group exists
────────────────────────────────────────
Validation Name: AzureClusterMISExistenceValidation
File: validations/azure_cluster_mis_existence_validation.go
What it checks: Cluster managed identities exist
────────────────────────────────────────
Validation Name: AlwaysSuccessValidation
File: validations/always_success_validation.go
What it checks: No-op (test/placeholder)

Each stores a condition like {Type: "AzureResourceProvidersRegistrationValidation", Status: True/False} in
ServiceProviderCluster.Status.Validations[].

Where Your Manifest-Applying Controller Should Write Status

There are two separate status locations, and the wrapping clusterWatchingController already handles one of them automatically:

1. Controller Health — automatic, you get it for free

Looking at cluster_watching_controller.go:110-129, the wrapping controller calls WriteController + ReportSyncError after your
syncer's SyncOnce returns. Your syncer does NOT need to call WriteController — just return an error from SyncOnce and the
Degraded condition gets set automatically on api.Controller.Status.Conditions[].

This tells you "is the controller broken" — not "what's the state of the work."

2. Work Results — write to ServiceProviderCluster.Status

For tracking "applied manifest / failed to apply / pending," follow the existing patterns:

- Validations use ServiceProviderCluster.Status.Validations[] (conditions slice)
- Maestro bundles use ServiceProviderCluster.Status.MaestroReadonlyBundles (structured reference list)
- Desired cluster spec uses ServiceProviderCluster.Spec.DesiredHostedCluster

For a controller that applies manifests to the mgmt cluster, the ServiceProviderCluster is the right place. You'd either:

Option A: Add a new conditions slice (like Validations) if you want to track multiple independent manifest-apply outcomes:
type ServiceProviderClusterStatus struct {
// ... existing fields ...
ManifestConditions []Condition `json:"manifestConditions,omitempty"`
}

Option B: Add a specific field (like MaestroReadonlyBundles) if the data is more structured than pass/fail:
type ServiceProviderClusterStatus struct {
// ... existing fields ...
SomeManifestReference *SomeRef `json:"someManifestRef,omitempty"`
}

Then in your syncer's SyncOnce, follow the Maestro bundle controller pattern at
create_cluster_scoped_maestro_readonly_bundles_controller.go:110-205:
1. Get the ServiceProviderCluster via GetOrCreateServiceProviderCluster
2. Check if work is needed (idempotency guard)
3. Do the work (apply manifests)
4. Update existingServiceProviderCluster.Status.YourField
5. serviceProviderClustersDBClient.Replace(ctx, existingServiceProviderCluster, nil)
6. Return error (the wrapping controller handles Degraded condition)

The key design principle: Controller status = "is the controller healthy." ServiceProviderCluster status = "what is the state
of the work for this cluster." Other controllers can read ServiceProviderCluster to gate on your controller's work being
complete, just like the validation controllers gate on IsConditionTrue.
