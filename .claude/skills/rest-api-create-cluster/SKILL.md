---
name: rest-api-create-cluster
description: Create an HCP cluster and nodepool via direct REST API calls to the local dev RP (localhost:8443). Faster than the E2E test — no test binary needed.
---

# Create Cluster & NodePool via REST API (Dev Environment)

Create an HCP cluster and nodepool by issuing direct REST API calls to the local dev RP. This is faster than running the E2E test binary and gives full control over the request payloads.

## Prerequisites

- Port-forwarding to the dev RP must be set up (the RP listens on `http://localhost:8443`).
- `az login` must have been run so Azure resources (resource group, vnet, subnet) can be queried/created.
- A resource group with a customer vnet/subnet must already exist, OR the user must create them first.

## Constants

```
Subscription ID: 1d3378d3-5a3f-4712-85a1-2485495dfc4b
API version:     2025-12-23-preview
Location:        westus3
```

## Steps

### 1. Determine the resource group and cluster name

If the user provides a resource group and cluster name, use those. Otherwise, generate short random suffixes (6 chars) and use:
- Resource group: `rg-manual-<suffix>`
- Cluster name: `cluster-manual-<suffix>`
- NodePool name: `np-<suffix>`

### 2. Ensure a resource group exists

```bash
az group show --name <rg> 2>/dev/null || az group create --name <rg> --location westus3
```

### 3. Ensure a customer vnet and subnet exist

Check if a vnet already exists in the resource group. If not, create one:

```bash
az network vnet create \
  --resource-group <rg> \
  --name customer-vnet-<suffix> \
  --address-prefix 10.0.0.0/16 \
  --subnet-name customer-subnet-<suffix> \
  --subnet-prefix 10.0.0.0/24 \
  --location westus3
```

Record the full subnet resource ID for use in the cluster and nodepool creation payloads.

### 4. Create the cluster (PUT)

```bash
curl -s -X PUT "http://localhost:8443/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/<rg>/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/<cluster>?api-version=2025-12-23-preview" \
  -H "Content-Type: application/json" \
  -H 'X-Ms-Arm-Resource-System-Data: {"createdBy":"nshneor","createdByType":"User","createdAt":"<now>","lastModifiedBy":"nshneor","lastModifiedByType":"User","lastModifiedAt":"<now>"}' \
  -d '{
    "location": "westus3",
    "properties": {
      "version": {
        "id": "4.20",
        "channelGroup": "stable"
      },
      "network": {
        "networkType": "OVNKubernetes",
        "podCidr": "10.128.0.0/14",
        "serviceCidr": "172.30.0.0/16",
        "machineCidr": "10.0.0.0/16"
      },
      "api": {
        "visibility": "public"
      },
      "platform": {
        "subnetId": "<subnet-resource-id>",
        "networkSecurityGroupId": "",
        "etcdEncryptionKmsKeyURL": ""
      },
      "externalAuth": {
        "enabled": false
      }
    }
  }'
```

- **Cluster version** must be `MAJOR.MINOR` (e.g. `4.20`), NOT `MAJOR.MINOR.PATCH`.
- **Channel group** on dev RP only supports `fast` and `stable` (NOT `candidate`).
- Expect HTTP `201` on success with `provisioningState: "Accepted"`.

### 5. Wait for the cluster to provision

Poll the cluster GET endpoint every 30 seconds until `provisioningState` is `Succeeded` or `Failed`:

```bash
curl -s "http://localhost:8443/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/<rg>/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/<cluster>?api-version=2025-12-23-preview" | jq '.properties.provisioningState'
```

Cluster creation typically takes 10-15 minutes. Report progress to the user periodically.

### 6. Create a nodepool (PUT)

Once the cluster is provisioned (or at least in `Provisioning`):

```bash
curl -s -X PUT "http://localhost:8443/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/<rg>/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/<cluster>/nodePools/<np>?api-version=2025-12-23-preview" \
  -H "Content-Type: application/json" \
  -H 'X-Ms-Arm-Resource-System-Data: {"createdBy":"nshneor","createdByType":"User","createdAt":"<now>","lastModifiedBy":"nshneor","lastModifiedByType":"User","lastModifiedAt":"<now>"}' \
  -d '{
    "location": "westus3",
    "properties": {
      "version": {
        "id": "4.20.8",
        "channelGroup": "stable"
      },
      "replicas": 2,
      "platform": {
        "vmSize": "Standard_D8s_v3",
        "subnetId": "<subnet-resource-id>"
      }
    }
  }'
```

- **NodePool version** must be `MAJOR.MINOR.PATCH` (e.g. `4.20.8`), NOT `MAJOR.MINOR`.
- The minimum valid patch version is `4.20.8`.
- Expect HTTP `201` on success with `provisioningState: "Accepted"`.

### 7. Report the result

Tell the user:
- Cluster name and resource group
- NodePool name
- Current provisioning states
- Remind them that resources must be manually deleted later

## Key gotchas

- **`X-Ms-Arm-Resource-System-Data` header is required for all PUT requests.** ARM normally injects this; without it the local RP returns HTTP 500.
- **DELETE requests do NOT need the system data header.** They return `202` on success, `204` if already gone.
- **Cannot delete the last nodepool** from a cluster — the RP returns `400`.
- **Timestamps in the system data header** should be ISO 8601 format (e.g. `2026-08-26T08:00:00Z`).

## Optional overrides

The user may provide:
- A different cluster/nodepool name
- A different version (cluster: MAJOR.MINOR, nodepool: MAJOR.MINOR.PATCH)
- A different VM size (default: `Standard_D8s_v3`)
- A different replica count (default: 2)
- An existing resource group and subnet ID (skip steps 2-3)

Apply any overrides the user specifies; otherwise use the defaults above.
