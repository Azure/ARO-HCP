Load all of CLAUDE.md for context

## Image Bumps

For all image bump related tasks, see [tooling/image-updater/AGENTS.md](tooling/image-updater/AGENTS.md).

## E2E Testing

### Prow Jobs

- **Periodic**: `periodic-ci-Azure-ARO-HCP-main-periodic-integration-e2e-parallel`
- **PR**: `pull-ci-Azure-ARO-HCP-main-integration-e2e-parallel`
- **Postsubmit**: `branch-ci-Azure-ARO-HCP-main-e2e-integration-e2e-parallel`

Results are stored in GCS at `https://storage.googleapis.com/test-platform-results/`:
- Periodic/postsubmit: `logs/<job-name>/<run-id>/`
- PR: `pr-logs/pull/Azure_ARO-HCP/<pr-number>/<job-name>/<run-id>/`

Each run has `finished.json` (with `"result"`) and `build-log.txt` (with per-test JSON results).

```bash
# Check if a run finished
curl -s "<gcs-base-url>/finished.json" | grep -o '"result":"[^"]*"'

# Count pass/fail/skip
curl -s "<gcs-base-url>/build-log.txt" | grep -E '"result": "(failed|passed|skipped)"' | sort | uniq -c

# Get failed test names
curl -s "<gcs-base-url>/build-log.txt" | grep -B5 '"result": "failed"' | grep '"name"'
```

E2E runs create ~19 parallel HCPs on MGMT, each running ~43 pods when fully provisioned.

## Cluster Troubleshooting

### Getting cluster access

Use `az aks get-credentials` to get kubeconfigs for SVC and MGMT clusters. Dev environments use the Red Hat tenant. See `config/config.yaml` for cluster names and resource groups per environment.

### Kubelet stats API

`kubectl top` only shows metrics-server data which can underreport. For accurate per-node and per-pod resource usage, use the kubelet stats API:

```bash
# Disk usage (used, capacity, image cache)
kubectl get --raw "/api/v1/nodes/<node>/proxy/stats/summary" | jq '{used: .node.fs.usedBytes, cap: .node.fs.capacityBytes, images: .node.runtime.imageFs.usedBytes}'

# Top CPU consumers per node (actual usage from cgroups)
kubectl get --raw "/api/v1/nodes/<node>/proxy/stats/summary" | jq -r '[.pods[] | {name: .podRef.name, ns: .podRef.namespace, cpu: (.containers // [] | map(.cpu.usageNanoCores // 0) | add)}] | sort_by(-.cpu) | .[:5][] | "\(.cpu/1000000 | floor)m \(.ns)/\(.name)"'
```

### MGMT cluster checklist

- HCP count and Available status: `kubectl get hostedcontrolplanes -A`
- Node conditions: DiskPressure, MemoryPressure, PIDPressure
- Disk usage and image cache size via kubelet stats on `userswft` worker nodes
- Problem pods: `kubectl get pods -A | grep -E "CrashLoopBackOff|Error|Pending|OOMKilled"`
- High restart counts: check for pods with restarts > 3
- Stuck PVs (`kubectl get pv | grep -v Bound`) and terminating namespaces (common after E2E cleanup)

### SVC cluster checklist

- Node scheduling status — nodes may be cordoned (`SchedulingDisabled`)
- Maestro health: `kubectl get pods -n maestro -o wide` — check restarts and node placement
- OOM events: `kubectl get events -A --field-selector reason=OOMKilling --sort-by='.lastTimestamp'`
- Resource overcommit: `kubectl describe node <node> | grep -A5 "Allocated resources"` — compare requests vs limits vs actual
- Disk on small nodes via kubelet stats API

### Key services and their resource impact on SVC

| Service | Typical CPU | Notes |
|---------|------------|-------|
| maestro | ~1000m per pod | #1 consumer. Config in `maestro/server/values.yaml` (hardcoded, not in config.yaml). Template at `maestro/server/deploy/templates/maestro.deployment.yaml` |
| arobit-forwarder | ~150m per pod | DaemonSet |
| azuresecuritylinuxagent | ~80m CPU + ~5Gi disk per node | Microsoft-managed DaemonSet, not in our control |
| cilium | ~65m per node | CNI |

### Common failure patterns

- **`GetAdminRESTConfigForHCPCluster` timeout**: Usually means HCP credentials never became available. Check maestro health on SVC — OOM-kills or restarts disrupt service-to-management communication. Increasing the timeout alone does not fix this if maestro is unhealthy.
- **Disk pressure on SVC**: Baseline is high due to `azuresecuritylinuxagent` (~5Gi) on small disk nodes. Check if image eraser ran successfully (`kubectl get pods -n kube-system | grep eraser`).
- **MGMT image cache growth**: Container images persist across E2E runs. Use kubelet stats to check image cache size. The eraser DaemonSet handles cleanup when running.
- **Klusterlet pods stuck in ContainerCreating**: Normal during HCP bootstrap — ACM agents pulling images. Only investigate if persists after HCP shows Available=True.
- **Stale pods in Error/Completed**: E2E cleanup doesn't always remove all pods. Check if a healthy replacement exists before investigating (e.g. `credential-refresher` may have a stale evicted pod alongside a running one).
