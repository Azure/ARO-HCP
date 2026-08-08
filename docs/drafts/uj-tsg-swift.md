# TSG: SWIFT Networking: Router Pod NIC Assignment Failure

## Table of Contents

- [When to Use This TSG](#when-to-use-this-tsg)
- [Alert Triage](#alert-triage)
- [Cluster Access](#cluster-access)
- [Glossary](#glossary)
- [Purpose](#purpose)
- [Severity and Impact](#severity-and-impact)
- [Customer-Visible Symptoms](#customer-visible-symptoms)
- [Service Symptoms](#service-symptoms)
- [Identify the Problem](#identify-the-problem)
- [Mitigation](#mitigation)
- [Validation and Confirmation](#validation-and-confirmation)
- [After Incident](#after-incident)
- [Appendix: CNS Prometheus Metrics Reference](#appendix-cns-prometheus-metrics-reference)
- [Appendix: Router Pod and SWIFT NIC Metrics](#appendix-router-pod-and-swift-nic-metrics-kube-state-metrics)

# When to Use This TSG

- Use this TSG when router pods are stuck in `ContainerCreating` or `Pending` on management cluster worker nodes and the root cause is suspected to be SWIFT secondary NIC assignment failure.
- Use this TSG when `userJourneySwiftLatencyP99*` or `userJourneySwiftErrors*` alerts fire.
- Use this TSG when `SwiftCNSAvailability3d` or `SwiftPendingProgramming` alerts fire.
- Use this TSG when HCP cluster creation success rate drops significantly in one region compared to others.

See the [SWIFT Networking User Journey Runbook](uj-runbook-swift.md) for background on the SWIFT stack, component ownership, and SLI/SLO definitions.

# Alert Triage

| Alert | What it means | Start here |
|-------|--------------|------------|
| `userJourneySwiftLatencyP991h5m` / `userJourneySwiftLatencyP996h30m` | Router pods are taking too long to start | [Step 1: Establish blast radius](#step-1-establish-blast-radius) |
| `userJourneySwiftErrors1h5m` / `userJourneySwiftErrors6h30m` | NIC assignment requests are failing | [Step 3: Read CNS logs](#step-3-read-cns-logs-and-classify-the-error) |
| `userJourneySwiftCNSLatencyP991h5m` / `userJourneySwiftCNSLatencyP996h30m` | NIC assignment is taking too long | [Step 3: Read CNS logs](#step-3-read-cns-logs-and-classify-the-error) |
| `SwiftCNSAvailability3d` | Some nodes are missing the CNS networking agent | [Step 2a: Check CNS](#step-2-check-hcp-side-prerequisites) |
| `SwiftPendingProgramming` | NICs are reserved but not being attached to pods | [Step 3: Read CNS logs](#step-3-read-cns-logs-and-classify-the-error) then [Step 3.5: AKS Kusto](#step-35-akskusto-dnc-rc-logs) |

# Cluster Access

All `kubectl` commands in this TSG run against a **management cluster**. To get a kubeconfig:

1. Request JIT access to the management cluster's subscription via [aka.ms/jitaccess](https://aka.ms/jitaccess) (role: Azure Kubernetes Service RBAC Admin)
2. Use `hcpctl` to obtain a kubeconfig for the target management cluster

See the [Breakglass Guide](https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/breakglass/index.html) for kubeconfig steps. See the [ARO HCP Access Guide](https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/aro-hcp-access-guide) for general access and IcM account setup.

The `az` commands in Steps 2f, 2g, and 4 require an AME account with Reader on the management subscription.

# Glossary

| Term | Meaning |
|------|---------|
| **AFEC** | Azure Feature Exposure Control: Microsoft's system for registering preview feature flags. SWIFT requires the `NetworkingMultiTenancyPreview` AFEC flag on the management subscription. |
| **CCP (AKS)** | AKS Customer Control Plane: the AKS-managed control plane for the management cluster. DNC-RC and DNC run inside the AKS CCP. The CCP ID is a hex identifier used to scope Kusto log queries. Find via [ASI](https://aka.ms/asi) by searching the cluster FQDN. |
| **HCP** | Hosted Control Plane: a customer's OpenShift cluster control plane running as pods inside an OCM namespace on the management cluster. |
| **2P subnet** | Second-party subnet: the host/infrastructure subnet used by the management cluster's own workloads. |
| **3P subnet** | Third-party subnet: the customer-exclusive subnet used by CX pods, assigned via secondary NICs. |
| **CX pods** | Customer eXclusive pods: in ARO-HCP, the **router pod** is the only CX pod that receives a SWIFT secondary NIC. |
| **CPO** | Control Plane Operator: the HyperShift component that creates the router Deployment and injects the SWIFT pod network instance label onto router pods. |
| **ILB** | Internal Load Balancer: created in the customer's VNet integration subnet during HCP provisioning. Worker nodes reach the kube-apiserver, ignition, OAuth, and Konnectivity endpoints via the `hypershift.local` private DNS zone, which resolves to the ILB frontend IP. |
| **SWIFT / SWIFT V2** | Azure's multi-tenant networking feature for AKS management clusters. These terms are used interchangeably in this document. |

# Purpose

SWIFT secondary NIC assignment can fail at several layers: ARO-HCP-owned configuration (SAL, PodNetwork, PNI, mgmt-agent), managed Azure components (CNS, DNC-RC), or the Azure platform itself (NRP, DNC). The primary goal at 2am is to quickly determine which tier owns the failure so the right team can be engaged.

The single most important decision: **is this on the HCP side (we can fix) or the Azure platform side (requires IcM to MSFT)?**

# Severity and Impact

Severity follows the [Azure CEN](https://aka.ms/AzureCEN). SWIFT incidents can arrive via two paths:

**Platform Incident path** (SRE detects via monitoring/alerts):

| Scenario | Severity |
|----------|----------|
| Isolated CNS issue on a single management cluster worker node, no confirmed customer impact | Sev 4 |
| SWIFT failure on a single management cluster causing router pod networking to fail (a single management cluster hosts multiple customers) | Sev 2 |
| Multi-region SWIFT failure (multiple management clusters across regions affected simultaneously) | Sev 1 |

**CRI path** (customer opens a support ticket):

A single-customer support ticket stays at Sev 3 unless escalated. It becomes Sev 2 when associated with a Sev-A support case with CSS actively engaged, OR signed off by a High Judgement Decision Maker.

- **Customer impact:** Private connectivity between customer VNet and HCP is degraded or broken. Worker nodes go `NotReady`, webhooks fail, `oc logs`/`oc exec` break, new HCP clusters fail to provision.
- **Business impact:** Management cluster functionally impaired for HCP tenants despite appearing healthy at the ARM level.

# Customer-Visible Symptoms

- Worker nodes `NotReady`: kubelet cannot reach the kube-apiserver via the private path
- Admission webhooks failing or timing out
- `oc logs` / `oc exec` returning TCP errors or timing out
- HCP certificate rotations failing (private Key Vault unreachable)
- New HCP cluster provisioning failing or stalled
- Router pods stuck in `ContainerCreating` on management cluster worker nodes

# Service Symptoms

- `userJourneySwiftLatencyP99*` alert firing: router pod startup p99 exceeds 300s
- `userJourneySwiftErrors*` alert firing: CNS `requestipconfig` error rate elevated
- `SwiftPendingProgramming` alert firing: `cx_pending_programming_ips_v2 > 0` sustained
- `kubectl get multitenantpodnetworkconfigs -A | grep -v provisioned` returns output
- CNS logs contain `ErrorCode 4`, `ErrorCode 135`, or DNC-RC error strings
- `cx_pending_programming_ips_v2` sustained > 0 in Grafana
- HCP cluster creation success rate asymmetric across regions

# Identify the Problem

## Dashboards

- **SWIFT Networking** Grafana dashboard (management cluster datasource `^.*-mgmt-\d+$`): router pod startup p99, CNS availability, SWIFT NIC utilization
- **Cluster Provisioning SLO (Fleet Wide)**: compare creation success rate across regional datasources to detect AKS RP regression

## Logging and Queries

- CNS logs: `kubectl logs -n kube-system <cns-pod>`
- AKS Kusto (requires `akshuba.centralus.kusto.windows.net` access): see [Step 3.5](#step-35-akskusto-dnc-rc-logs)

## Diagnostic Steps

### Step 1: Establish blast radius

The blast radius determines whether this is an HCP-side issue or an Azure platform issue before you look at a single log.

- **Goal:** Determine scope of impact to route triage correctly.
- **Action:**
  ```bash
  kubectl get nodes -o wide
  kubectl get pods -A -l 'kubernetes.azure.com/pod-network-instance' | grep -E 'ContainerCreating|Pending'
  ```
- **Expected result:** Isolated to one node, or one management cluster, or multiple clusters/subscriptions.
- **If/then outcome:**

| Pattern | Suggests | Next step |
|---------|----------|-----------|
| Single node, all other nodes fine | CNS pod issue or node-level config (HCP side) | Step 2a |
| All nodes in one management cluster, one region | HCP config or DNC/NRP regional issue | Step 2 |
| Multiple clusters across multiple subscriptions | Azure platform issue (no HCP config change spans multiple subs) | Step 4 directly |
| All `userswft` pools affected, system pools fine | NRP or DNC (SWIFT-specific code path) | Step 4 directly |
| HCP creation success rate asymmetric across regions | Possible AKS RP regression | Step 4 → AKS RP regression signal |

- **If multi-cluster, multi-subscription:** skip Steps 2-3 and jump directly to [Step 4](#step-4-azure-platform-signals).

---

### Step 2: Check HCP-side prerequisites

Run these checks in order. The first failure found is the cause. Fix it and verify before continuing.

- **Goal:** Rule out ARO-HCP-owned configuration as the root cause.
- **Systems involved:** CNS, mgmt-agent, management cluster API server, Azure subscription.

```bash
# 2a. Is CNS running on affected nodes?
kubectl get pods -n kube-system -l k8s-app=azure-cns -o wide
# All pods should be Running. CrashLoopBackOff or missing = HCP side.

# 2b. Does every SWIFT-enabled node have a NodeInfo CRD?
kubectl get nodeinfo -A
# Count should match SWIFT-enabled node count. Missing = CNS startup failure = HCP side.

# 2b.5. Are router pods Running? Check SWIFT NIC capacity if Pending.
kubectl get pods -n <ocm-namespace> -l kubernetes.azure.com/pod-network-instance
# ContainerCreating → check MTPNCs (2c). Pending → check NIC capacity:
kubectl get nodes -o custom-columns='NAME:.metadata.name,SWIFT-NIC:.status.capacity.aro\.openshift\.io/swift-nic'
# Expected: 7 per SWIFT-enabled node. Zero = restart mgmt-agent pod to force resync.

# 2c. Are MTPNCs being created and reaching provisioned?
kubectl get multitenantpodnetworkconfigs -A | grep -v provisioned
# Any output = stuck MTPNC. No output but pods stuck = MTPNC never created; check PNI (2d).

# 2c-detail. Describe a stuck MTPNC - events are more reliable than CRD status fields.
kubectl describe mtpnc <pod-name> -n <namespace>
# Look for Events: "CreateOrUpdateFailed" shows the exact DNC-RC error string.
# DeletionTimestamp present + finalizer still set = stuck deletion (see Mitigation).

# 2c-cns. Query CNS IP state directly:
kubectl exec -n kube-system -it <cns-pod> -- /usr/local/bin/azure-cns -cmd get -darg All
kubectl exec -n kube-system -it <cns-pod> -- /usr/local/bin/azure-cns -cmd get -darg Assigned
kubectl exec -n kube-system -it <cns-pod> -- /usr/local/bin/azure-cns -cmd get -darg Available

# 2c-nnc. Check NodeNetworkConfig for IP allocation state:
kubectl -n kube-system get nnc <node-name>
# NC VERSION ahead of what CNS has programmed = IPs stuck in PendingProgramming.

# 2c-nodeinfo. Check secondary NICs on the node:
kubectl get nodeinfo <node-name> -oyaml
# status.deviceInfos lists MAC addresses. Cross-reference with MTPNC .status.macAddress.

# 2d. Do PodNetwork and PodNetworkInstance CRDs exist?
kubectl get podnetwork pn-<cluster-id>
kubectl describe podnetwork pn-<cluster-id>        # check Events for DNC-RC errors

kubectl get podnetworkinstance -n <ocm-namespace>
kubectl get podnetworkinstance pni-<cluster-id> -n <ocm-namespace> \
  -o jsonpath='{.status.podIPAddresses}'
# Expected: 3 IPs. Empty = DNC-RC has not allocated IPs yet (check Step 3.5).
# Note: the ILB provisioning step is the gate - it retries until IPs appear.

kubectl describe podnetworkinstance pni-<cluster-id> -n <ocm-namespace>  # check Events

# 2e. Does the router pod have the SWIFT label?
kubectl get pod <pod-name> -o jsonpath='{.metadata.labels}'
# Must contain: kubernetes.azure.com/pod-network-instance: <pni-name>

# 2f. Is the AFEC flag registered? (requires AME account + Reader on mgmt subscription)
az feature show --namespace Microsoft.ContainerService \
  --name NetworkingMultiTenancyPreview \
  --subscription <mgmt-subscription-id>
# Must show "state": "Registered".

# 2g. Does the SAL exist? (requires AME account + Reader on mgmt subscription)
az network vnet subnet show \
  -g <mgmt-resource-group> --vnet-name <mgmt-vnet-name> -n <pod-subnet-name> \
  --subscription <mgmt-subscription-id> \
  --query "serviceAssociationLinks[?name=='RedHatOpenShift'].provisioningState" -o tsv
# Expected: Succeeded. Empty = SAL missing = HCP side.
```

- **If all checks pass:** HCP prerequisites are intact. Proceed to [Step 3](#step-3-read-cns-logs-and-classify-the-error).
- **If any check fails:** You have found an HCP-side root cause. Fix the specific failed check.

---

### Step 3: Read CNS logs and classify the error

- **Goal:** Identify the error category from CNS logs to determine whether to fix locally or escalate.
- **Action:**
  ```bash
  kubectl logs -n kube-system <cns-pod> | \
    grep -E "ErrorCode|error code|reservation|capacity|SubnetFull|JsonError|sync host|nmagent"
  ```

**DNC error codes (slot exhaustion):**

| ErrorCode | Message | Category | Next step |
|-----------|---------|----------|-----------|
| `4` | `no free reservation in set` | IP slot exhaustion | Step 3.5 then Step 4 |
| `135` | `NodeCapacityExceeded` | NIC slot exhaustion | Step 3.5 then Step 4 |

Both codes together = DNC slots not being released. Common upstream cause: NRP validation failure blocking node drains.

**DNC-RC error strings (in CNS logs and MTPNC events):**

| Error string | Category | Action |
|---|---|---|
| `JsonError:[Code:SubnetFull...]` | Customer subnet full | Customer must expand subnet or free IPs |
| `network is not ready - mtpnc is not ready` | DNC provisioning in progress | Usually transient; check Step 3.5 if sustained |
| `network is not ready - failed to get MTPNC` | MTPNC not yet created | Check PNI (Step 2d) |
| `network is not ready - mtpnc for previous pod is being deleted` | MTPNC stuck deletion | See Mitigation: Stuck MTPNC deletion |
| `invalid NIC type for SWIFT v2 scenario` | NIC type config error | HCP configuration issue |
| `connection refused` to DNC (port 9000) | DNC down | Escalate: Azure AKS team |
| `status 401` on DNC | Auth/MSI failure | Escalate: AKS DRI |
| `Cosmos...StatusCode=404,ErrorCode=ResourceNotFound` | DNC-RC dependency error | Escalate: AKS DRI |
| `object has been modified; please apply your changes...` | **Benign** | DNC-RC will auto-retry; ignore |
| `sync host error` / `failed to get nc version list from nmagent` | CNS can't reach NMA | Escalate: Cloudnet/Vnet (NMAgent team) |

Also check aggregate CNS metrics:
```promql
# IPs stuck in pending programming? (sustained > 0 = DNC not releasing NICs)
cx_pending_programming_ips_v2

# IP pool divergence: POSITIVE = DNC not fulfilling requests (problem)
# Healthy: total slightly > requested (~-1 buffer); alert on > 0
cx_ipam_requested_ips - cx_ipam_total_ips
```

- **If ErrorCode 4 or 135:** proceed to [Step 3.5](#step-35-akskusto-dnc-rc-logs), then [Step 4](#step-4-azure-platform-signals).
- **If DNC-RC error string matched:** see table above for routing.

---

### Step 3.5: AKS Kusto (DNC-RC logs)

> **Permission required:** `akshuba.centralus.kusto.windows.net` / database `AKSccplogs`. Access via [AKS Kusto Partners](https://coreidentity.microsoft.com/manage/Entitlement/entitlement/akskustopart-mqif).

Find the CCP ID via [ASI](https://aka.ms/asi) by searching the management cluster FQDN under AKS Managed Cluster.

```kusto
// DNC-RC reconciler errors for a specific management cluster
union ControlPlaneEvents, ControlPlaneEventsNonShoebox
| where category == "requestcontroller"
| where namespace == "<CCP ID>"
| extend props=parse_json(properties)
| extend logline=parse_json(tostring(props.log))
| where logline.error != ""
| project PreciseTimeStamp, logline.component, logline.msg, logline.error
| sort by PreciseTimeStamp desc
| limit 1000
// NodeReconciler errors → node registration failures
// NodeNetworkConfigReconciler errors → IP allocation failures
```

```kusto
// Combined DNC + DNC-RC logs around a specific event time
union ControlPlaneEvents, ControlPlaneEventsNonShoebox
| where PreciseTimeStamp between (datetime('<timestamp>') - 10m .. datetime('<timestamp>') + 10m)
| where category in ('requestcontroller', 'dnc')
| where namespace == "<CCP ID>"
| extend propsJson=parse_json(properties)
| project PreciseTimeStamp, propsJson.log
| sort by PreciseTimeStamp asc
```

**Error routing:**

| DNC-RC log pattern | Meaning | Escalate to |
|---|---|---|
| `NodeReconciler` errors | DNC-RC can't register/deregister nodes with DNC | Azure AKS team |
| `NodeNetworkConfigReconciler` + `SubnetFull` | Subnet exhausted | Customer action |
| `NodeNetworkConfigReconciler` + `connection refused` / `status 500` | DNC is down | Azure AKS team (DNC) |
| `NodeNetworkConfigReconciler` + `status 401` | DNC auth failure | AKS DRI |
| `NodeNetworkConfigReconciler` + `Cosmos...ResourceNotFound` | DNC-RC dependency | AKS DRI |

- **If DNC server errors (5xx, connection refused):** escalate to Azure AKS team; they will involve DNC DRI.
- **If NRP validation errors upstream:** escalate to Cloudnet/NRP → proceed to [Step 4](#step-4-azure-platform-signals).

---

### Step 4: Azure platform signals

> At this step, the root cause is in Azure platform infrastructure that Red Hat SREs cannot directly access or modify. File an IcM against the team listed below, then provide the Kusto queries to the Microsoft team on the bridge.

**Signal: NRP VMSS validation failures**

Observable from the HCP side without MSFT Kusto access:

```bash
kubectl get nodes -o wide
kubectl get events -n kube-system --field-selector reason=FailedScaleUp
az aks show -g <rg> -n <cluster-name> --query kubernetesVersion
az aks nodepool show -g <rg> -n <cluster-name> --nodepool-name userswft --query provisioningState
```

File IcM to **Cloudnet/NRP** (not DNC, not RNM, not SdnPubSub) when:
- ErrorCode 4/135 in CNS logs
- `cx_pending_programming_ips_v2` sustained > 0
- Only `userswft` pools affected; system pools fine
- Management cluster upgrade stuck or autoscaler `FailedScaleUp` events

Provide this query to the NRP team:
```kusto
// nrp.kusto.windows.net / mdsnrp
FrontendOperationEtwEvent
| where PreciseTimeStamp > ago(24h)
| where Region =~ "<affected-region>"
| where SubscriptionId in ("<mgmt-sub-1>", "<mgmt-sub-2>")
| where Message has "InvalidVMScaleSetNetworkInterfaceConfigurationIPConfigCount"
| summarize ErrorCount = count() by bin(PreciseTimeStamp, 1h), SubscriptionId, ResourceName
```

Key things for NRP to check if confirmed:
- `IsSwiftV2NetworkProfileWithEmptyIpConfig` returning false
- `NullReferenceException` at `VMScaleSet.cs:1819`

---

**Signal: DNC PubSub partition errors**

If NRP query shows nothing but DNC is still failing to release NICs:

Escalate to **Cloudnet/Container Networking AKS Swift Control Plane**. Provide affected VNet GUIDs and subscription IDs:

```kusto
// aznwsdn.kusto.windows.net / sdnpubsub
PubSubAPICall
| where PreciseTimeStamp > ago(24h)
| where Tenant contains "<region>"
| where isFault == true
| where additional contains "<vnet-guid>"
```

---

**Signal: AKS RP regression / NIC delegation failure**

Suspect this when HCP creation success rate is significantly lower in one region than others.

Observable detection signals:
- Creation failure rate asymmetric across regions (check Cluster Provisioning SLO dashboard, compare regional `Managed_Prometheus_services-*` datasources)
- Only `userswft` pools affected; system pools fine
- Cordoning and recreating affected nodes does not fix the problem (new nodes inherit the broken VMSS template)
- NRP query above shows `InvalidVMScaleSetNetworkInterfaceConfigurationIPConfigCount`

Escalate to **Cloudnet/NRP and AKS RP team** via IcM. Request a force-reconcile rollout for the affected management cluster VMSS instances. Provide: subscription IDs, VMSS names, affected region, regional creation success rate data.

# Mitigation

## Scenario: Stuck MTPNC deletion (MTPNC has DeletionTimestamp, finalizer not clearing)

The chain is: router pod stuck terminating → MTPNC can't be GC'd (ownerRef `blockOwnerDeletion`) → DNC-RC can't release IPs → PNI finalizer never clears.

Fix: force-delete the router pod, or drain and delete the node if it is `NotReady`.

```bash
# Option A: force-delete the stuck router pod
kubectl delete pod <router-pod-name> -n <ocm-namespace> --grace-period=0 --force

# Option B: if the node is NotReady, drain and replace it
# Check for workloads and PodDisruptionBudgets before draining.
kubectl drain <node-name> --ignore-daemonsets --delete-emptydir-data

az vmss delete-instances \
  --resource-group <node-resource-group> \
  --name <vmss-name> \
  --instance-ids <instance-id>

kubectl delete node <node-name>
```

> **Risk:** Draining a node evicts all pods on it. Confirm no critical workloads are pinned and check PodDisruptionBudget constraints before proceeding.

## Scenario: mgmt-agent reporting zero SWIFT NICs on a node

Router pods will be `Pending` with "Insufficient aro.openshift.io/swift-nic" on an otherwise healthy SWIFT-enabled node. Alert: `MgmtClusterNodeSwiftNICCapacityZero`.

```bash
# Restart mgmt-agent to force a resync of NIC capacity
kubectl rollout restart deployment -n mgmt-agent
```

## Scenario: CNS not running on a node (HCP side)

If a CNS pod is missing or in `CrashLoopBackOff`, all router pod NIC assignments on that node fail.

```bash
kubectl describe pod -n kube-system <cns-pod>   # check events and logs
kubectl logs -n kube-system <cns-pod> --previous
```

Escalate to the Azure AKS team if CNS cannot be restarted or the crash is not caused by HCP configuration.

## Scenario: Azure platform issue (NRP / DNC / AKS RP regression)

File an IcM per the routing in [Step 4](#step-4-azure-platform-signals). Provide the Kusto query results and the blast radius data collected in Steps 1-3.

> **Note from ITN-2026-00158:** Route directly to Cloudnet/NRP for NRP validation failures. Routing to SdnPubSub or RNM first costs hours. NRP validation failures (`InvalidVMScaleSetNetworkInterfaceConfigurationIPConfigCount`) are the upstream cause of DNC exhaustion; DNC and RNM are downstream symptoms.

# Escalation

> ADR-001 requirement: alerts without a documented escalation path in their TSG will not be accepted into the ARO-RP routing lane.

| Symptom / signal | Escalate to | Channel | Evidence to attach |
|---|---|---|---|
| CNS pod failure, MTPNC never created, HCP config issue | Azure AKS team | IcM | `kubectl describe mtpnc`, CNS logs, `kubectl get nodeinfo` output |
| DNC NIC/IP exhaustion (ErrorCode 4/135) with NRP validation failures upstream | **Cloudnet/NRP** (not DNC, not RNM, not SdnPubSub) | IcM | CNS log snippet, `cx_pending_programming_ips_v2` graph, blast radius (subscriptions/clusters affected) |
| DNC PubSub context selector loss | Cloudnet/Container Networking AKS Swift Control Plane | IcM | Affected VNet GUIDs, subscription IDs |
| AKS RP regression / NIC delegation failure | Cloudnet/NRP and AKS RP team | IcM | Regional creation success rate comparison, NRP Kusto query results |
| DNC server errors (5xx, connection refused) | Azure AKS team (DNC) | IcM | DNC-RC Kusto log snippet |
| DNC auth failure (status 401) | AKS DRI | IcM | DNC-RC Kusto log snippet showing 401 |
| NMA unreachable (`sync host error`) | Cloudnet/Vnet (NMAgent team) | IcM | CNS log snippet |
| ARO-HCP component ownership unclear | [ARO-HCP Component Inventory + Ownership](https://docs.google.com/spreadsheets/d/1Z2uhI3ctbZCF0rOj8B4VsoYx1rR9x_1ZLsbOr6Eug-8/edit?gid=0#gid=0) | - | - |

> **From ITN-2026-00158:** Route directly to Cloudnet/NRP for NRP validation failures. Routing to SdnPubSub or RNM first costs hours. NRP validation failures (`InvalidVMScaleSetNetworkInterfaceConfigurationIPConfigCount`) are upstream of DNC exhaustion; DNC and RNM are downstream symptoms.

> **IcM filing process:** Red Hat SREs require an AME account to file IcM incidents against Microsoft teams. See the [ARO HCP Access Guide](https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/aro-hcp-access-guide) for account setup. Queue IDs for each team are listed in the escalation table above.

# Validation and Confirmation

- Re-run `kubectl get multitenantpodnetworkconfigs -A | grep -v provisioned`: should return no output
- Confirm router pods reach `Running`: `kubectl get pods -n <ocm-namespace> -l kubernetes.azure.com/pod-network-instance`
- Confirm `cx_pending_programming_ips_v2` returns to 0 in Grafana
- Confirm `userJourneySwift*` alerts have cleared
- Confirm affected HCP cluster's worker nodes return to `Ready`
- If a management cluster upgrade was stuck: confirm `az aks nodepool show ... --query provisioningState` returns `Succeeded`

# After Incident

## Postmortem Notes

- Document which step identified the root cause and how long triage took
- If the NRP or AKS RP signal was present, document the Kusto query results and the IcM filed
- If the MTPNC stuck deletion was the cause, note whether a node drain was required
- File a follow-up if the root cause required a workaround rather than a fix (e.g. force-reconcile rollout from AKS team)
- Update this TSG if new error strings, failure modes, or triage steps were discovered

# Appendix: CNS Prometheus Metrics Reference

These metrics are queryable in Grafana via the **management cluster Prometheus datasources** (`Managed_Prometheus_hcps-<region>-mgmt-<N>`). Source: [`azure-container-networking`](https://github.com/Azure/azure-container-networking) repo.

> The CNS PodMonitor is deployed at `observability/prometheus/deploy/templates/azure-cns.podmonitor.yaml`.

## SLI / Alerting

Metrics that map directly to SLOs and alert rules.

| Metric | Type | Labels | What it measures |
|--------|------|--------|-----------------|
| `ip_assignment_latency_seconds` | Histogram | - | End-to-end pod IP assignment latency. Primary latency SLI. |
| `http_request_latency_seconds` | Histogram | `url`, `verb`, `cns_return_code` | Per-endpoint request latency. Filter `url=~".*/requestipconfig.*"` and `cns_return_code!="0"` for error rate SLI. |
| `cx_pending_programming_ips_v2` | Gauge | - | IPs in `PendingProgramming`: reserved from DNC but secondary NIC not yet attached. Sustained non-zero is the primary proxy for stuck MTPNCs. |
| `cns_nnc_initialized` | Gauge | - | `1` once the initial NNC reconcile has completed. `0` = node not ready to serve IP requests. |
| `cns_nnc_reconciler_start_failures_total` | Counter | - | NNC reconciler failed to start. A positive rate = CNS actively failing to initialise. |
| `cns_nnc_init_failures_total` | Counter | - | Initial NNC reconcile failed. |

## IP Pool State

Node-level pool snapshot (`cx_*_v2`) and per-subnet breakdown (`cx_ipam_*`). Use for pool health dashboards and diagnosing IP provisioning issues.

| Metric | Type | Labels | What it measures |
|--------|------|--------|-----------------|
| `cx_allocated_ips_v2` | Gauge | - | Total IPs allocated to CNS by DNC |
| `cx_assigned_ips_v2` | Gauge | - | IPs currently assigned to router pods |
| `cx_available_ips_v2` | Gauge | - | IPs ready to assign; drops to 0 under exhaustion |
| `cx_pending_release_ips_v2` | Gauge | - | IPs no longer used but not yet returned to DNC |
| `cx_ipam_total_ips` | Gauge | subnet, subnet_cidr, podnet_arm_id | Total IP pool size allocated to CNS by DNC |
| `cx_ipam_requested_ips` | Gauge | subnet, subnet_cidr, podnet_arm_id | IPs requested by this node from DNC |
| `cx_ipam_pod_allocated_ips` | Gauge | subnet, subnet_cidr, podnet_arm_id | IPs currently in use by pods |
| `cx_ipam_available_ips` | Gauge | subnet, subnet_cidr, podnet_arm_id | IPs available for assignment |
| `cx_ipam_max_ips` | Gauge | subnet, subnet_cidr, podnet_arm_id | Hardware/SKU cap on secondary IPs for this node |
| `cx_ipam_pending_programming_ips` | Gauge | subnet, subnet_cidr, podnet_arm_id | IPs reserved but secondary NIC not yet attached |
| `cx_ipam_pending_release_ips` | Gauge | subnet, subnet_cidr, podnet_arm_id | IPs pending release back to DNC |
| `cx_ipam_subnet_exhaustion_state` | Gauge | subnet, subnet_cidr, podnet_arm_id | `1` = subnet exhausted, `0` = healthy |
| `cx_ipam_subnet_exhaustion_state_count_total` | Counter | subnet, subnet_cidr, podnet_arm_id | Cumulative exhaustion observations |

> Healthy state: `cx_ipam_total_ips` slightly exceeds `cx_ipam_requested_ips` (DNC pre-allocates a small buffer; `requested - total` of about -1 is normal). Alert on **positive** values: `cx_ipam_requested_ips - cx_ipam_total_ips > 0` means CNS asked for IPs that DNC has not delivered.

## Diagnostic

Metrics for investigating specific failure modes. Not primary SLI targets.

| Metric | Type | Labels | What it measures |
|--------|------|--------|-----------------|
| `ipconfigstatus_state_transition_seconds` | Histogram | `previous_state`, `next_state` | Time an IP config spends in each state transition. Useful for finding where IPs are getting stuck. |
| `ip_pool_inc_latency_seconds` | Histogram | `batch` | Round-trip time to increase the IP pool via NNC. Detects DNC slowness. |
| `ip_pool_dec_latency_seconds` | Histogram | `batch` | Round-trip time to decrease the IP pool via NNC. |
| `nnc_has_nodenetworkconfig` | Gauge | - | `1` if CNS has received its NNC from DNC; `0` if not. |
| `nnc_ncs` | Gauge | - | Number of NetworkContainers in the NNC. Should be >= 1 on a SWIFT-enabled node. |
| `sync_host_nc_version_total` | Counter | `ok` | Host NC sync attempts by success/failure. |
| `sync_host_nc_version_latency_seconds` | Histogram | `ok` | Host NC sync latency. Detects NMA communication issues. |
| `has_networkcontainer` | Gauge | - | NetworkContainers retrieved from NMA. |

# Appendix: Router Pod and SWIFT NIC Metrics (kube-state-metrics)

Raw kube-state-metrics signals underlying the `router:startup_latency:*` recording rules and the SWIFT NIC saturation metric. Useful when the aggregated recording rules are hiding per-pod or per-node detail.

> Datasource: management cluster Prometheus (`^.*-mgmt-\d+$`).

## Router pod scheduling

| Metric | Useful filter | What it shows |
|--------|--------------|---------------|
| `kube_pod_status_phase` | `namespace=~"ocm-.*", phase="Pending"` | Which router pods are currently in ContainerCreating/Pending |
| `kube_pod_created` | `namespace=~"ocm-.*"` | Pod creation timestamp; combine with `time()` to get age of any pending pod |
| `kube_pod_owner` | `owner_kind="ReplicaSet", owner_name=~"router-.*"` | Identifies which pods are router pods by their ReplicaSet owner |

To see the raw per-pod ContainerCreating durations:
```promql
(time() - kube_pod_created{namespace=~"ocm-.*"})
* on(namespace, pod) kube_pod_owner{owner_kind="ReplicaSet", owner_name=~"router-.*"}
* on(namespace, pod) (kube_pod_status_phase{phase="Pending"} == 1)
```

## SWIFT NIC capacity

| Metric | Useful filter | What it shows |
|--------|--------------|---------------|
| `kube_node_status_capacity` | `resource="aro.openshift.io/swift-nic"` | Total SWIFT NIC slots per node (7 in prod) |
| `kube_pod_container_resource_requests` | `resource="aro.openshift.io/swift-nic"` | NIC slots currently requested by scheduled router pods, per pod |

## CNS daemonset health

| Metric | Useful filter | What it shows |
|--------|--------------|---------------|
| `kube_daemonset_status_number_ready` | `daemonset="azure-cns", namespace="kube-system"` | How many CNS pods are currently ready |
| `kube_daemonset_status_desired_number_scheduled` | `daemonset="azure-cns", namespace="kube-system"` | How many CNS pods should be running |

## Recording rules reference

> **Tip:** Run alert queries with the threshold removed to see pre- and post-alert trends. E.g. graph `router:startup_latency:p99_avg_1h` without `> 300` to see the full history around an alert window.

| Recording rule | What it pre-computes |
|----------------|---------------------|
| `router:startup_latency:seconds` | Per-pod ContainerCreating duration for all pending router pods |
| `router:startup_latency:p99` | p99 of the above across all pending router pods |
| `router:startup_latency:p99_avg_5m` | 5-minute average of the p99 (short window for fast-burn alert) |
| `router:startup_latency:p99_avg_30m` | 30-minute average of the p99 (short window for medium-burn alert) |
| `router:startup_latency:p99_avg_1h` | 1-hour average of the p99 (long window for fast-burn alert) |
| `router:startup_latency:p99_avg_6h` | 6-hour average of the p99 (long window for medium-burn alert) |
