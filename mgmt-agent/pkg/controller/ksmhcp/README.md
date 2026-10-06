# KSM HCP Controller

The KSM HCP controller enables monitoring of customer worker nodes and selected
guest-cluster custom resources across Hosted Control Planes. In the HyperShift
architecture, these resources are registered with the HCP's own API server and
are invisible to the management cluster's KSM. This controller deploys a
[kube-state-metrics](https://github.com/kubernetes/kube-state-metrics) instance
into each HCP's control plane namespace, scraping metrics directly from the HCP
API server and forwarding them to the HCP Azure Managed Prometheus workspace.

## How It Works

The controller runs inside mgmt-agent alongside the SwiftNIC controller under a single leader election. It watches `HostedControlPlane` CRs and, once the kube-apiserver is available, creates a KSM Deployment, Service, and ServiceMonitor in the HCP's control plane namespace. KSM connects to the HCP API server using the `service-network-admin-kubeconfig` secret. The ServiceMonitor injects the `namespace` label so metrics route to the HCP Azure Monitor Workspace via the existing remote write filter. The `region` and `environment` labels are provided globally via Prometheus external labels.

Resources are owned by the HostedControlPlane CR and cleaned up automatically by Kubernetes garbage collection when the HCP is deleted.

Enabled via the `--ksm-image` flag. The collected metrics are controlled via `--metric-allowlist` in [`resources.go`](resources.go).

## Collected Karpenter State

The per-HCP KSM custom-resource configuration collects:

| Resource | Metrics | Retention |
|---|---|---|
| `karpenter.sh/v1` `NodePool` | Unhealthy or unresolved conditions and managed node count | Only `False` and `Unknown` conditions are retained |
| `karpenter.sh/v1` `NodeClaim` | Conditions requiring SRE attention | `False` and `Unknown`, plus `InstanceTerminating=True` |
| `karpenter.azure.com/v1beta1` `AKSNodeClass` | Unhealthy or unresolved conditions | Only `False` and `Unknown` conditions are retained |

The exported metrics are:

- `karpenter_nodepool_status_condition_transition_time_seconds`
- `karpenter_nodepool_status_nodes`
- `karpenter_nodeclaim_status_condition_transition_time_seconds`
- `karpenter_aksnodeclass_status_condition_transition_time_seconds`

### Namespace scope

Do not add `--namespaces=openshift-ingress-operator` to this KSM deployment.
KSM applies its global namespace selection to custom-resource-state clients as
well as native Kubernetes resources. The Karpenter `NodePool`, `NodeClaim`, and
`AKSNodeClass` resources are cluster-scoped, so forcing a namespace would make
KSM attempt namespaced API requests and their metrics would not be collected.

When `--namespaces` is unset, KSM uses its all-namespaces default. The
namespaced `IngressController` in `openshift-ingress-operator` therefore remains
visible. This does not enable unrelated Kubernetes metrics: `--resources=nodes`,
the metric allowlist, and the explicit custom-resource-state configuration still
limit exported metrics to Nodes, IngressController, and the selected Karpenter
resources.

Condition metrics contain the Unix timestamp from `lastTransitionTime` and
include only the resource `name`, `condition`, `status`, and `reason`, plus the
standard KSM and scrape-target labels. Dashboards subtract this timestamp from
the current time to show how long each condition has remained unchanged.
NodeClaim provider IDs, node names, UIDs, and arbitrary labels or annotations
are intentionally not exported.

Kubernetes conditions use three status values. `False` means the condition is
explicitly not satisfied, while `Unknown` means the controller cannot currently
determine whether it is satisfied. These states are retained because they may
indicate a provisioning, readiness, or deletion problem. They are not
automatically incidents: a new or deleting resource may be `Unknown` during
normal reconciliation, so dashboards also show how long the condition has
remained unchanged.

Before Prometheus ingestion, the ServiceMonitor drops every Karpenter condition
with `status=True` for NodePools and AKSNodeClasses. NodeClaims follow the same
rule except that `InstanceTerminating=True` is retained so the dashboard can
identify claims that remain in termination for too long. Other `True`-valued
NodeClaim signals, including `Ready`, `Drifted`, `Consolidatable`, and
`DisruptionReason`, remain filtered.

Provisioning that fails or does not finish is tracked through
`Launched=False/Unknown`, followed by `Registered=False/Unknown` and
`Initialized=False/Unknown`. Their `lastTransitionTime` values show how long
each stage has remained unresolved. `Launched=True` is filtered because launch
completed successfully.

NodeClaims are the highest-cardinality resource, so filtering completed and
healthy conditions prevents them from dominating ingestion. The terminating
exception adds at most one series per actively terminating NodeClaim.
Dashboards treat 250,000 retained NodeClaim condition series per management
cluster as the design ceiling.

100 HCPs × 500 affected NodeClaims × 5 retained conditions = 250,000 series

To retain another `True`-valued NodeClaim condition in the future, add its
condition type to the alternation in `retainedTrueNodeClaimConditionRE` in
[`resources.go`](resources.go). Keep the generic unmarked-`True` drop rule and
temporary-label cleanup in place, then add the new condition to
`TestBuildServiceMonitorFiltersKarpenterConditions` and document why it requires
SRE attention. Conditions such as `Ready`, `Drifted`, `Consolidatable`, and
`DisruptionReason` should remain filtered unless their operational requirement
changes.

KSM reports current API state only. When a Karpenter resource is deleted, its
series becomes stale and disappears. Use Karpenter events, controller metrics,
and logs when historical provisioning or deletion evidence is required.

Do not add guest-cluster Karpenter resources to
`observability/prometheus/values-mgmt.yaml`. That KSM instance connects to the
management-cluster API and cannot observe these resources.
