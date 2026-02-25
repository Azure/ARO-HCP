# hcpctl - ARO HCP SRE CLI Tool

A CLI tool for ARO HCP operations, including emergency access (breakglass) functionality into service clusters, management clusters and hosted clusters.

## Purpose

`hcpctl` provides SREs with emergency access capabilities for ARO HCP infrastructure:

- **Service Cluster (SC) access**: Get shell access to AKS service clusters
- **Management Cluster (MC) access**: Get shell access to AKS management clusters
- **Hosted Control Plane (HCP) access**: Break glass into customer HCP clusters for emergency operations
- **Azure AD authentication**: Integrated kubelogin for seamless Entra authentication with AKS clusters

## Installation

The tool will be available on SAW devices similar to how the `oc` utility is accessed. `hcpctl` works in the Red Hat DEV environment, Microsoft INT environment, and on SAW devices to access stage and production in AME.

## Prerequisites

- Azure CLI authentication
- JIT permissions for target clusters (currently Azure Kubernetes Service RBAC Cluster Admin role, subject to change)

## Commands Overview

### Service Cluster Operations (`sc`)

- `hcpctl sc list` - List available service clusters
- `hcpctl sc breakglass <cluster-name>` - Get access to service cluster

### Management Cluster Operations (`mc`)

- `hcpctl mc list` - List available management clusters
- `hcpctl mc breakglass <cluster-name>` - Get access to management cluster
- `hcpctl mc dump-crs <cluster-name> --hosted-cluster-namespace=<ns>` - Dump Custom Resources for a HostedCluster

### Hosted Control Plane Operations (`hcp`)

- `hcpctl hcp list` - List available HCP clusters
- `hcpctl hcp breakglass <cluster-service-id|azure-resource-id>` - Emergency access to HCP cluster

## Example Usage

### List Service Clusters

```bash
# List all available service clusters
hcpctl sc list

# Filter by region
hcpctl sc list --region eastus

# Output as JSON
hcpctl sc list --output json
```

### Breakglass into Service Cluster

```bash
# Get shell access to service cluster
hcpctl sc breakglass int-usw3-svc-1

# Generate kubeconfig only (no shell)
hcpctl sc breakglass int-usw3-svc-1 --output /tmp/sc.kubeconfig --no-shell
KUBECONFIG=/tmp/sc.kubeconfig kubectl get ns
```

### List Management Clusters

```bash
# List all available management clusters
hcpctl mc list

# Filter by region
hcpctl mc list --region eastus

# Output as JSON
hcpctl mc list --output json
```

### Breakglass into Management Cluster

```bash
# Get shell access to management cluster
hcpctl mc breakglass int-usw3-mgmt-1

# Generate kubeconfig only (no shell)
hcpctl mc breakglass int-usw3-mgmt-1 --output /tmp/mc.kubeconfig --no-shell
KUBECONFIG=/tmp/mc.kubeconfig kubectl get ns
```

### Dump Custom Resources for a HostedCluster

```bash
# Dump all CRs for a HostedCluster namespace to current directory
hcpctl mc dump-crs int-usw3-mgmt-1 --hosted-cluster-namespace aro-12345678-abcd-1234-5678-123456789abc

# Dump CRs to a specific directory
hcpctl mc dump-crs int-usw3-mgmt-1 --hosted-cluster-namespace aro-12345678-abcd-1234-5678-123456789abc -o /tmp/hcp-debug
```

### List Hosted Control Planes

```bash
# List HCPs on current management cluster
hcpctl hcp list
```

### Breakglass into HCP

```bash
# Emergency access using cluster ID
hcpctl hcp breakglass 12345678-1234-1234-1234-123456789abc

# Access using Azure resource ID
hcpctl hcp breakglass /subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpClusters/cluster-name

# Privileged access (uses aro-sre-cluster-admin role instead of aro-sre)
hcpctl hcp breakglass 12345678-1234-1234-1234-123456789abc --privileged
```

## Gather logs from Kusto

### Gather Managed cluster logs

This is the usual use case for must-gather kust. You can gather logs for a managed cluster from Kusto. You need to be logged into Azure to access Kusto. You need to set kusto and region to point to the Kusto instance containing the desired logs.

```bash
hcpctl must-gather  query --kusto $kusto --region $region  --subscription-id $subscription_id --resource-group $resource_group
```

If you get an error like, limit execeeded try reducing the amount of data by setting either limit or timestamps, i.e.:

Set `--limit` fetch the first `$limit` number of rows.

```bash
hcpctl must-gather  query \
    --kusto aroint --region eastus \
    --subscription-id $subscription_id --resource-group $resource_group
    --limit 10000
```

The parameters $resource_group and $subscription_id must point to the managed cluster, not the AKS cluster running this HCP/Service.

### Gather infra cluster logs

If you want to gather all Kusto logs for a given infra cluster (servicecluster or management), you can run 

```bash
hcpctl must-gather  query-infra \
    --kusto aroint --region eastus \
    --service-cluster $svc_cluster_name \
    --mgmt-cluster $mgmt_cluster_name \
    --limit 10000

```

You can provide multiple `service-cluster` parameters and multiple `mgmt-cluster`. Logs will be collected sequentially and stored in a single folder for all clusters provided.

## TODO

- use the Hypershift generated clientsets instead of dedicated schema registration
- tests for the `pkg/breakglass/minting` package
- tests for the `pkg/breakglass/portforward` package (e.g. https://github.com/openshift/ci-tools/blob/05305124f711827983c0908af9020a41ad6afacf/pkg/testhelper/accessory.go#L261)
- E2E tests for all breakglass commands
