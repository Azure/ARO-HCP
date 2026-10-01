# KSM HCP Controller

The KSM HCP controller enables monitoring of customer worker nodes across Hosted Control Planes. In the HyperShift architecture, worker nodes register with the HCP's own API server — they are invisible to the management cluster's KSM. This controller deploys a [kube-state-metrics](https://github.com/kubernetes/kube-state-metrics) instance into each HCP's control plane namespace, scraping node metrics directly from the HCP API server and forwarding them to the HCP Azure Managed Prometheus workspace.

## How It Works

The controller runs inside mgmt-agent alongside the SwiftNIC controller under a single leader election. It watches `HostedControlPlane` CRs and, once the kube-apiserver is available, creates a KSM Deployment, Service, and ServiceMonitor in the HCP's control plane namespace. KSM connects to the HCP API server using the `service-network-admin-kubeconfig` secret.

### Collector mode: OSS vs AMA

The controller runs in both metrics-collection modes, selected per management cluster by `--monitoring-api-group`, and emits the ServiceMonitor accordingly:

- **OSS mode** (`monitoring.coreos.com`, the default): the in-cluster Prometheus agent scrapes the ServiceMonitor and remote-writes to the HCP Azure Monitor Workspace. Routing is done by `namespace` in the agent's remote-write filter, and the `region`/`environment` labels are supplied globally via the agent's Prometheus external labels. In this mode the ServiceMonitor is created in the `monitoring.coreos.com` group and its series carry the historical `microsoft_metrics_include_label` relabel (a harmless no-op, since routing is by namespace).

- **AMA mode** (`azmonitoring.coreos.com`): Azure Monitor's managed agent discovers the `azmonitoring.coreos.com` ServiceMonitor directly — there is no in-cluster Prometheus agent. Because nothing else supplies them, the controller stamps the series itself:
  - `microsoft_metrics_account=hcp`, the per-series routing label the HCP data collection rule's `labelIncludeFilter` keys on (note: `microsoft_metrics_include_label` is only the filter *key name*, not a series label);
  - `region` and `environment`, injected as set-if-absent relabelings from the `--metrics-region` / `--metrics-environment` flags (required in AMA mode), preserving parity with the labels the OSS agent would have added via external labels.

Switching a cluster's mode converges in a single rollout: the controller creates the ServiceMonitor in the new group and `deleteStaleServiceMonitor` removes the one in the old group.

Resources are owned by the HostedControlPlane CR and cleaned up automatically by Kubernetes garbage collection when the HCP is deleted.

Enabled via the `--ksm-image` flag. The collected metrics are controlled via `--metric-allowlist` in [`resources.go`](resources.go).
