# Minimal Resource Requests Feature

## Overview

The Minimal Resource Requests feature allows ARO-HCP clusters to be provisioned with reduced
CPU and memory requests for Hosted Control Plane components. This is useful for development
and testing environments where resource constraints are tighter and allows more clusters to
run on limited infrastructure.

## Feature Flag

This feature is controlled by an Azure Feature Exposure Control (AFEC) flag:

- **Feature Name:** `Microsoft.RedHatOpenShift/MinimalResourceRequests`
- **Constant:** `api.FeatureMinimalResourceRequests` (defined in `internal/api/featureflags.go`)

## How It Works

1. When a subscription has the `MinimalResourceRequests` AFEC registered, cluster creation
   and update operations will include the `hosted_cluster_minimal_resource_requests: "true"`
   property in requests to Cluster Service.

2. Cluster Service passes this property to HyperShift, which configures the control plane
   components with reduced resource requests.

## Enabling the Feature

Register the AFEC flag on your expected subscription. All future requests to create/update clusters in that subscription will have the minimal resources set. Unsetting the flag will allow you to Update a cluster to use production resource settings.

## Limitations

- This feature is intended for development and testing environments only
- Production clusters should use standard resource requests for stability and performance
- The feature applies to both cluster creation and update operations
- Once a cluster is created with minimal resources, this property persists


## Troubleshooting Guide

### Checking if Feature Was Enabled for a Cluster

#### 1. Check Frontend Logs

Look for the feature flag evaluation log entry. The log includes the cluster ID and feature flag state:

```
evaluated feature flags for cluster creation clusterId=... minimalResourceRequests=true/false
```

#### 2. Check Cluster Service Properties

Query the Cluster Service to see the properties set on the cluster:

```bash
# Using ocm CLI
ocm get /api/clusters_mgmt/v1/clusters/<cluster-id> | jq '.properties.hosted_cluster_minimal_resource_requests'
```

#### 3. Check Subscription Feature Registration

Verify if the AFEC is registered on the subscription:

```bash
az feature show \
  --namespace Microsoft.RedHatOpenShift \
  --name MinimalResourceRequests \
  --subscription <subscription-id> \
  --query properties.state -o tsv
```

Expected output: `Registered` if the feature is enabled.

### Tracing

The feature flag state is captured in distributed traces. Look for the `aro.feature.minimal_resource_requests` attribute on `ClusterServiceClient.PostCluster` and `ClusterServiceClient.UpdateCluster` spans.

### Emergency Disable Procedure

If this feature causes issues:

1. **Unregister the AFEC from affected subscriptions:**
   ```bash
   az feature unregister \
     --namespace Microsoft.RedHatOpenShift \
     --name MinimalResourceRequests \
     --subscription <subscription-id>
   ```

2. **New clusters** will no longer have minimal resources enabled.

3. **Existing clusters** retain their properties until Update. To update an existing cluster, trigger a cluster update operation (the frontend will re-evaluate feature flags on each update).


## Related Documentation

- [Demo README](../../demo/README.md) - Instructions for using feature flags
- [High-Level Architecture](../high-level-architecture.md) - ARO-HCP architecture overview
