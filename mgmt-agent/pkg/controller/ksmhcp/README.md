# KSM HCP Controller

The KSM HCP controller enables monitoring of customer worker nodes across Hosted Control Planes. In the HyperShift architecture, worker nodes register with the HCP's own API server — they are invisible to the management cluster's KSM. This controller deploys a [kube-state-metrics](https://github.com/kubernetes/kube-state-metrics) instance into each HCP's control plane namespace, scraping node metrics directly from the HCP API server and forwarding them to the HCP Azure Managed Prometheus workspace.

## How It Works

The controller runs inside mgmt-agent alongside the SwiftNIC controller under a single leader election. It watches `HostedControlPlane` CRs and, once the kube-apiserver is available, creates a KSM Deployment, Service, and ServiceMonitor in the HCP's control plane namespace. KSM connects to the HCP API server using the `service-network-admin-kubeconfig` secret. The ServiceMonitor injects the `namespace` label so metrics route to the HCP Azure Monitor Workspace via the existing remote write filter. The `region` and `environment` labels are provided globally via Prometheus external labels.

Resources are owned by the HostedControlPlane CR and cleaned up automatically by Kubernetes garbage collection when the HCP is deleted.

Enabled via the `--ksm-image` flag. The collected metrics are controlled via `--metric-allowlist` in [`resources.go`](resources.go).

## Resource Configuration

The mgmt-agent controller accepts `--hcp-kube-state-metrics-cpu-request`,
`--hcp-kube-state-metrics-memory-request`, and
`--hcp-kube-state-metrics-memory-limit`. Defaults remain `10m`, `64Mi`, and
`256Mi`, respectively. Values must be valid positive Kubernetes quantities; the
memory request must not exceed the limit. There is no CPU limit or init-container
resource override.

These settings apply to the regular `kube-state-metrics` container in every
controller-owned `kube-state-metrics-hcp` Deployment. They are passed through the
controller constructor and used on every server-side apply, so manual workload
edits are not durable. Configuration integration uses
`mgmtAgent.guestKSMResources.requests.{cpu,memory}` and
`mgmtAgent.guestKSMResources.limits.memory` to populate these flags.
