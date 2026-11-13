# Remediation: Unrecoverable Cluster Due to Azure AD Tenant Transfer or Expiration

## Overview

When a customer's Azure AD (Entra ID) tenant expires or their subscription is transferred to a different tenant, running ARO HCP clusters become **permanently unrecoverable**. This is a terminal condition with no repair path — the cluster must be recreated in the new tenant.

### Why This Breaks the Cluster

User-Assigned Managed Identities (UAMIs) have Azure resources in the subscription, but their backing service principal lives in the Entra tenant. When a subscription is transferred to a new tenant or the original tenant expires, all existing UAMIs become invalid because the new tenant has no record of their service principals.

The failure cascade:

1. **Managed identity invalidation** — All UAMIs associated with the cluster are now invalid in the new tenant
2. **KMS encryption failure** — The KMS sidecar can no longer authenticate to Azure Key Vault (`403 Forbidden` / `ForbiddenByRbac`)
3. **etcd encryption key inaccessible** — The KMS cannot decrypt the etcd encryption key
4. **kube-apiserver health failure** — KAS readiness checks fail because the KMS health check fails
5. **All operators fail** — Loss of API server causes all cluster operators to report unavailable
6. **ClusterVersion stuck Partial** — The CVO cannot complete any upgrade; both current and target versions remain Partial indefinitely

**Why identity recreation doesn't help**: A recreated UAMI is a new identity with no relationship to the original. The cluster's managed identities can no longer access the etcd encryption key stored in Azure Key Vault, and a newly created identity does not inherit the Key Vault access policies or RBAC permissions of the original identity.

## Tracking

This issue was first systematically observed across multiple incidents in October 2026:

- **Incident A** — Central India region, ClusterVersion 4.22.19 stuck Partial for ~42 hours before detection
- **Incident B** — Similar pattern, AADSTS5000229 errors in KMS/kube-apiserver logs
- **Multiple incidents observed** — Tenant transfer/expiration pattern reported across different regions in October 2026

Related issues:
- **ARO-30258** — Cluster deletion stalls on AADSTS5000229 (deletion path)
- **ARO-30096** — Cluster deletion stuck when subscription moved to different tenant (deletion path)
- **ARO-30031** — Subscription tenant transfer is unsupported (documentation)

## Symptoms

All of the following will be present on an affected cluster:

| Signal | Detail |
|---|---|
| **ClusterVersion** | Stuck in `Partial` state, unable to complete any upgrade (including z-stream patches initiated by SRE) |
| **kube-apiserver** | `KubeAPIServerAvailable=False`, readiness checks failing |
| **KMS errors** | `403 Forbidden`, `ForbiddenByRbac`, AADSTS5000229 in kube-apiserver and KMS logs |
| **Operator status** | All cluster operators report unavailable or degraded |
| **Duration** | Can persist for days before detection if no upgrade is attempted; often only surfaces when an SRE-initiated z-stream upgrade triggers an IcM alert |
| **Error signature** | `AADSTS5000229: The tenant '<tenant-id>' is not available due to expiration`, `InvalidAuthenticationTokenTenant` |

## Diagnosis

### 1. Confirm AADSTS5000229 / InvalidAuthenticationTokenTenant in Logs

Check KMS and kube-apiserver logs for tenant-related authentication failures:

```bash
# On the management cluster (via hcpctl mc breakglass <mc-name>)
export CLUSTER_NAME="<cluster-name>"
export CLUSTER_NAMESPACE="ocm-<prefix>-<cluster-id>-${CLUSTER_NAME}"

# Check kube-apiserver logs
kubectl logs -n ${CLUSTER_NAMESPACE} deployment/kube-apiserver --tail=100 | grep -i "AADSTS\|InvalidAuthenticationTokenTenant\|ForbiddenByRbac"

# Check KMS sidecar logs
kubectl logs -n ${CLUSTER_NAMESPACE} deployment/kube-apiserver -c kms | grep -i "AADSTS\|403\|Forbidden"
```

Look for error patterns like:
- `AADSTS5000229: The tenant '<tenant-id>' is not available due to expiration`
- `InvalidAuthenticationTokenTenant`
- `403 Forbidden` / `ForbiddenByRbac` on Key Vault operations

### 2. Confirm Tenant Mismatch

Verify that the subscription's current tenant does not match the tenant ID in the error logs:

```bash
# Get the subscription's current tenant (force refresh to detect transfers)
az account list --refresh --query "[?id=='<subscription-id>'].tenantId | [0]" -o tsv

# If the subscription is absent from the list, sign in to its current tenant and repeat.
# Do not infer expiration from a missing subscription result.

# Compare with the tenant ID from the AADSTS5000229 error
# If they differ, the subscription was transferred
```

### 3. Check ClusterVersion Status

```bash
# On the hosted cluster (if API server is still accessible)
kubectl get clusterversion version -o yaml

# Look for:
# - status.desired.version stuck for extended period
# - status.history showing Partial state for both current and target versions
```

If the API server is completely inaccessible, check the HyperShift HostedCluster status from the management cluster:

```bash
# On the management cluster
kubectl get hostedcluster -n ocm-<prefix>-<cluster-id> <cluster-name> -o jsonpath='{.status.version}' | jq .
```

### 4. Rule Out Other Causes

Before concluding this is a tenant transfer issue, rule out:

**Azure Policy interference** (e.g., AROSLSRE-2701 pattern — publicNetworkAccess disabled on storage accounts):
```bash
# Check for recent policy assignments on the subscription
az policy assignment list --subscription <subscription-id> --query "[?metadata.createdOn>'2026-10-01'].{name:name, policy:policyDefinitionId, createdOn:metadata.createdOn}" -o table
```

**General Key Vault access issues** (firewall, RBAC, deny assignments):

Identify the configured Key Vault key/version and the KMS managed identity. Inspect that identity's required cryptographic-operation permissions, applicable deny assignments, and the vault's firewall/private-endpoint configuration for the KMS network path. Testing secret-list access with the operator's Azure CLI credentials does not validate KMS key access.

**Network connectivity** (if KMS errors show timeout rather than 403):
```bash
# Check kube-apiserver to Key Vault connectivity from within the cluster
# (requires API server to be partially functional)
```

Once alternative causes have been ruled out, proceed to Remediation Step 1 and evaluate either the transfer evidence path or the expiration evidence path.

## Remediation Procedure

### Step 1: Confirm the Cluster is Unrecoverable

Before engaging the customer, confirm evidence of tenant transfer or expiration:

**Transfer evidence (subscription moved to different tenant):**
- [ ] Subscription tenant ID mismatch confirmed (current tenant ID ≠ tenant ID in AADSTS errors)
- [ ] AADSTS5000229 or InvalidAuthenticationTokenTenant confirmed in logs

**Expiration evidence (tenant disabled/deleted, subscription still in same tenant):**
- [ ] AADSTS5000229 with "not available due to expiration" or similar tenant-unavailable error
- [ ] Subscription tenant ID matches the tenant in error logs (tenant not transferred, but unavailable)

**Supporting evidence (not required, but strengthens diagnosis):**
- ClusterVersion stuck Partial with no progress
- KMS cannot authenticate to Key Vault (403 / ForbiddenByRbac)
- Extended outage duration (>24 hours)

**Decision gate**: Either the transfer evidence path OR the expiration evidence path must be satisfied. If neither path is clear, do not proceed with customer communication about cluster recreation — investigate alternative causes first.

**Recovery confirmation required**: Before declaring the cluster unrecoverable to the customer, confirm with the service team and Azure Key Vault/identity owners that no recovery path exists. Authentication failures alone do not prove that the encryption key is permanently inaccessible — verify that identity restoration or key access restoration is not possible.

### Step 2: Engage Customer Support

Notify the customer through the appropriate support channel (TAM, support case, or escalation path depending on the customer's support tier).

**Template message** (adapt to your support process):

```
Subject: ARO HCP Cluster Unrecoverable Due to Azure AD Tenant Change

Hello [Customer Name],

We have identified that your ARO HCP cluster "<cluster-name>" (ID: <cluster-id>)
in subscription <subscription-id> is in an unrecoverable state due to an Azure AD
(Entra ID) tenant change on the subscription.

What happened:
- The subscription's Azure AD tenant was [transferred/expired] on approximately [date]
- The cluster's managed identities are tied to the original tenant and cannot be
  migrated to the new tenant
- The cluster's managed identities can no longer access the configured KMS key
  needed to decrypt etcd data

Impact:
- The cluster API server is unavailable or degraded
- All cluster operators are failing
- The cluster cannot be upgraded or repaired

Required action:
- Workloads must be migrated off this cluster
- A new cluster must be created in the new tenant
- The existing cluster should be deleted once migration is complete

We understand this is disruptive and are available to assist with the migration
process. Please reply to this message or contact [escalation contact] for further
assistance.

Best regards,
[SRE Team / Support Team]
```

**Key points to communicate:**
1. The cluster is unrecoverable (this is not a configuration issue)
2. The customer must migrate workloads and recreate the cluster
3. Support is available to assist with the migration
4. The underlying cause was the subscription tenant change, not a cluster configuration issue

### Step 3: Suppress or Resolve Stuck Upgrade Alerts

If the cluster is stuck in Partial state due to an SRE-initiated z-stream upgrade, the IcM alert will continue firing until resolved.

**Option A: Suppress the alert** (if the customer migration will take days/weeks)

```bash
# Suppress the IcM alert with a note linking to this runbook and the customer engagement
# Example (adapt to your alerting system):
# "Cluster unrecoverable due to tenant transfer (ARO-30499). Customer engaged [date].
#  Migration in progress. Suppressing alert until cluster deletion."
```

**Option B: Resolve the alert** (if customer has confirmed deletion timeline)

If the customer has acknowledged the situation and scheduled cluster deletion, resolve the alert with a note explaining the resolution path:

```
Cluster is unrecoverable due to Azure AD tenant transfer/expiration (ARO-30499).
Customer notified [date], deletion scheduled [date]. Alert resolved as cluster
will be decommissioned rather than repaired.
```

### Step 4: Coordinate Cluster Deletion

Once the customer has migrated workloads and is ready to delete the cluster:

- [ ] Confirm with customer that all workloads have been migrated
- [ ] Confirm a replacement cluster has been created (if applicable)
- [ ] Initiate cluster deletion via standard ARM API (customer-initiated or SRE-assisted)
- [ ] Monitor for stuck deletion issues (see [Cleanup Procedure for Stuck Deletions](cleanup-stuck-cluster-deletion.md) if deletion stalls due to AADSTS5000229)

**Note**: The cluster deletion itself may also encounter AADSTS5000229 errors (tracked in ARO-30258, ARO-30096). If the deletion becomes stuck, use the manual cleanup procedures documented in the stuck-deletion runbook.

### Step 5: Document the Incident

Update the IcM, Jira, or internal tracking system with:
- Customer engagement timeline
- Migration completion date
- Cluster deletion date
- Any lessons learned specific to this customer's scenario

## Post-Resolution

### 1. Verify Cluster Deletion Completed

```bash
# Confirm the cluster is gone from Clusters Service on the service cluster
hcpctl sc breakglass <svc-name>
export KUBECONFIG="<service-cluster-path-from-output>"
export CLUSTER_ID="<cluster-id>"
kubectl exec -n clusters-service deployment/clusters-service -c clusters-service-server -- \
  curl -sS -o /dev/null -w 'HTTP %{http_code}\n' "http://localhost:8000/api/clusters_mgmt/v1/clusters/${CLUSTER_ID}"
# Expected: HTTP 404

# Confirm no orphaned resources remain on the management cluster
hcpctl mc breakglass <mc-name>
export KUBECONFIG="<management-cluster-path-from-output>"
kubectl get namespaces | grep -F -- "${CLUSTER_ID}"
kubectl get manifestwork -n local-cluster | grep -F -- "${CLUSTER_ID}"

# Verify Azure resources are cleaned up
export SUBSCRIPTION_ID="<subscription-id>"

# Use the cluster's managed resource group name, recorded before cluster deletion,
# not the resource group containing the cluster ARM resource.
# The managed resource group name is stored in CustomerProperties.Platform.ManagedResourceGroup
# and can be retrieved from the cluster document in Cosmos before deletion.
export RESOURCE_GROUP="<cluster-managed-resource-group>"

# Check for remaining VMs, NICs, disks, and other resources
az resource list --resource-group ${RESOURCE_GROUP} --subscription ${SUBSCRIPTION_ID} -o table

# If resources remain, check for deny assignments or RBAC issues preventing cleanup
# Consult the cleanup runbook for manual resource deletion if needed
```

### 2. Close Related Alerts

Resolve or close any related alerts that fired during the incident:
- Stuck upgrade alerts
- KubeAPIServerAvailable alerts
- Operator degraded alerts

### 3. Update Customer Communication

Send a closure message confirming:
- Cluster deletion completed
- Azure resources verified clean (or document any intentionally retained customer resources)
- Migration validated (if applicable)

## Prevention and Future Work

This runbook addresses remediation only. The following preventive measures are tracked separately:

1. **Automated detection** (ARO-30499) — A controller that detects AADSTS5000229 / tenant mismatch on running clusters and surfaces it as a cluster condition or alert **before** an upgrade monitor fires days later.

2. **Upgrade guard rails** (ARO-30499) — Pre-check tenant/identity health before initiating z-stream upgrades, to avoid triggering IcM alerts on already-broken clusters.

3. **Customer documentation** (ARO-30031) — Public documentation that subscription tenant transfer is unsupported and will result in cluster loss.

## Common Scenarios

### Scenario 1: Customer Unaware of Tenant Transfer

**Symptoms**: Customer opens a support case reporting that their cluster is "broken" or "won't upgrade" but has no knowledge of a tenant transfer.

**Diagnosis**: Work backward from the subscription's current tenant ID and the AADSTS errors to determine when the transfer occurred. Azure subscription activity logs may show the transfer event.

**Remediation**: Follow the standard remediation procedure but include an explanation of how the tenant transfer occurred (if discoverable) in the customer communication.

### Scenario 2: Cluster Partially Functional (Some Operators Still Working)

**Symptoms**: Some operators are still reporting available, or the API server is intermittently accessible.

**Cause**: Not all cluster components may have attempted to use the invalidated identities yet. The failure is progressive as components try to authenticate.

**Remediation**: Even if the cluster appears partially functional, the condition is terminal. The etcd encryption key cannot be decrypted, so any restart of kube-apiserver or etcd will cause total failure. Proceed with customer engagement and migration.

### Scenario 3: Deletion Also Stuck (ARO-30258 / ARO-30096)

**Symptoms**: Customer agrees to delete the cluster, but the deletion stalls indefinitely with AADSTS5000229 errors.

**Cause**: The cluster deletion flow also requires managed identity authentication to clean up Azure resources. The invalidated identities block deletion just as they blocked cluster operations.

**Remediation**: Use the [Cleanup Procedure for Stuck Deletions](cleanup-stuck-cluster-deletion.md) to manually clean up Maestro ResourceBundles, ManifestWorks, and Kubernetes resources. Azure resource cleanup may require manual intervention from the subscription owner if deny assignments prevent automated cleanup.

### Scenario 4: Multiple Clusters Affected (Subscription-Wide Tenant Transfer)

**Symptoms**: Multiple clusters in the same subscription all showing AADSTS5000229 simultaneously.

**Cause**: A subscription-wide tenant transfer affects all clusters in that subscription.

**Remediation**: Batch the customer communication to cover all affected clusters. Prioritize based on customer workload criticality. Expect a longer migration timeline as the customer will need to recreate multiple clusters.

## Lessons Learned

### Detection lag can be 40+ hours

An observed incident went undetected for 42+ hours, only surfacing when an SRE-initiated z-stream upgrade triggered an alert. Without automated detection, these failures are silent until an upgrade is attempted.

### Stuck upgrade alerts are symptoms, not the root cause

When an IcM fires for a stuck upgrade on a cluster with AADSTS errors, the root cause is the tenant transfer, not the upgrade process. Investigating the upgrade controller is a dead end — check identity/tenant health first.

### Customer communication is critical

Many customers are unaware that tenant transfer makes their clusters unrecoverable. Clear, empathetic communication about the migration path is essential to avoid customer frustration or escalation.

### Deletion may also require manual intervention

The deletion path has its own tenant-related failure modes (ARO-30258, ARO-30096). Plan for manual cleanup even after the customer agrees to delete.

### Automated detection is essential

Until automated detection is implemented (ARO-30499), rely on proactive monitoring of AADSTS errors in centralized logging (Kusto) to catch these cases early.

## Quick Reference: Error Signatures

| Error | Meaning | Action |
|---|---|---|
| `AADSTS5000229: The tenant '<tenant-id>' is not available due to expiration` | Authentication is blocked because the tenant has expired; this does not establish a subscription transfer or key deletion | Confirm tenant status and recovery options with the tenant owner before applying the remediation decision gate |
| `InvalidAuthenticationTokenTenant` | The identity's tenant does not match the subscription's current tenant | Confirm tenant mismatch (Diagnosis Step 2) |
| `403 Forbidden` / `ForbiddenByRbac` (KMS → Key Vault) | The identity cannot authenticate to Key Vault | Check if this is due to tenant mismatch vs. other RBAC/policy issues (Diagnosis Step 4) |
| `ClusterVersion Partial` for >24h with no progress | Upgrade cannot complete due to API server unavailability | Check if KMS/KAS logs show tenant errors (Diagnosis Step 1) |

## Quick Reference: Customer Communication Checklist

Before engaging the customer:

- [ ] Transfer evidence: Subscription tenant ID mismatch confirmed + AADSTS errors **OR**
- [ ] Expiration evidence: AADSTS5000229 "not available due to expiration" + subscription still in same tenant
- [ ] Alternative causes ruled out (Azure Policy, RBAC, network)
- [ ] Recovery confirmation: Service team and Key Vault/identity owners confirm no recovery path exists

Customer communication should include:

- [ ] Clear statement that the cluster is unrecoverable
- [ ] Explanation of why (tenant transfer + cryptographic binding)
- [ ] Required action (migrate workloads, recreate cluster)
- [ ] Offer of support/assistance with migration
- [ ] Timeline expectations for migration and deletion

## Related Documentation

- [Cleanup Procedure for Stuck Deletions](cleanup-stuck-cluster-deletion.md) — Manual cleanup if cluster deletion stalls due to AADSTS errors
- [ARO-30031](https://issues.redhat.com/browse/ARO-30031) — Document that subscription tenant transfer is unsupported
- [ARO-30258](https://issues.redhat.com/browse/ARO-30258) — Cluster deletion stalls on AADSTS5000229
- [ARO-30096](https://issues.redhat.com/browse/ARO-30096) — Cluster deletion stuck when subscription moved to different tenant
