# Dump Custom Resources for a HostedCluster

This document describes how to use the `hcpctl mc dump-crs` command to dump all Custom Resources (CRs) associated with a HostedCluster from a Management Cluster.

## Overview

When troubleshooting issues with a HostedCluster, it's often necessary to examine the Custom Resources that define and manage the cluster. The `dump-crs` command collects all CRs from a Management Cluster that are associated with a specific HostedCluster namespace and writes them to YAML files for analysis.

## Prerequisites

- Azure CLI authentication (`az login`)
- JIT permissions for the target Management Cluster (Azure Kubernetes Service RBAC Cluster Admin role)
- The HostedCluster namespace

## Command

```bash
hcpctl mc dump-crs <MC_NAME> --hosted-cluster-namespace <NAMESPACE> -o <OUTPUT_PATH>
```

### Parameters

| Parameter | Required | Description |
|-----------|----------|-------------|
| `MC_NAME` | Yes | Name of the Management Cluster (positional argument) |
| `--hosted-cluster-namespace` | Yes | Namespace of the HostedCluster to dump CRs for |
| `-o, --output-path` | No | Directory to write the CR files (default: current directory) |

### Aliases

- `hcpctl mc dc` - Short alias for `dump-crs`

## Usage Examples

### Basic Usage

```bash
# Dump CRs for a HostedCluster to the current directory
hcpctl mc dump-crs int-eastus-mgmt-1 --hosted-cluster-namespace ocm-12345678-abcd-1234-5678-123456789abc
```

### Specify Output Directory

```bash
# Create output directory and dump CRs
mkdir -p /tmp/hcp-debug
hcpctl mc dump-crs int-eastus-mgmt-1 \
  --hosted-cluster-namespace ocm-12345678-abcd-1234-5678-123456789abc \
  -o /tmp/hcp-debug
```

### Using the Short Alias

```bash
hcpctl mc dc int-eastus-mgmt-1 --hosted-cluster-namespace ocm-12345678-abcd-1234-5678-123456789abc
```

## Output

The command creates a `crs/<namespace>/` subdirectory containing YAML files for each CR type. For example:

```
<output-path>/
└── crs/
    └── <namespace>/
        ├── hostedclusterlist.hypershift.openshift.io.yaml
        ├── nodepool.hypershift.openshift.io.yaml
        ├── managedclusterlist.cluster.open-cluster-management.io.yaml
        ├── managedclusterinfolist.internal.open-cluster-management.io.yaml
        ├── manifestworklist.work.open-cluster-management.io.yaml
        ├── secretproviderclasslist.secrets-store.csi.x-k8s.io.yaml
        ├── secretsynclist.secret-sync.x-k8s.io.yaml
        └── ...
```

Each file contains all CRs of that type associated with the HostedCluster.

## What Gets Collected

The command collects:

1. **Namespace-scoped CRs**: All CRs in the HostedCluster namespace
2. **ManifestWork CRs**: From the `local-cluster` namespace, filtered by the cluster ID label
3. **ManagedCluster CR**: The cluster-scoped ManagedCluster resource matching the cluster ID


## Troubleshooting

### "namespace X missing label api.openshift.com/id"

The specified namespace doesn't have the required cluster ID label. Verify you have the correct namespace name.

### "failed to get namespace 'X'"

The namespace doesn't exist on the Management Cluster. Check that you're connected to the correct MC and the namespace name is correct.

### Permission Errors

Ensure you have active JIT permissions for the target Management Cluster with the Azure Kubernetes Service RBAC Cluster Admin role.
