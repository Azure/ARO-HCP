# Selected HCP Request Increases

These changes address a priority subset of underrequested HCP containers, not
all candidates or all 50 groups. They affect only the `limitClusterSizes=true`
minimal sizing branch and the dev-cloud `additionalMinimalResourceRequests` map.
The global map remains empty; other size classes and the unlimited branch are
unchanged. CPU and memory limits and reserve rules are not changed.

## Evidence

The per-container right-sizing reports pool these five CI runs:

| Key | Run ID |
| --- | --- |
| A | `2103929857506283520` |
| B | `2103981635216084992` |
| C | `2104275998722756608` |
| D | `2104335322849480704` |
| E | `2104639037800189952` |

CPU is the maximum observed 10-minute rate in mCPU. Memory is the sampled
working-set peak in MiB, not a 10-minute average. Maxima pool all five runs;
they are per-container observations, not simultaneous aggregate demand.
`*` marks a peak from a row failing strict sample coverage. Known maxima with
incomplete coverage are accepted for increases, not as proof of full coverage
or sufficient requests for every lifecycle peak. No decreases, rounding-only
increases, or inference from missing measurements are applied. Every changed
dimension increases its latest observed request; new overrides have consistent
observed baselines.

## Requests

Values below are CPU/memory (`m`/`Mi`). `-` means no configured override: retain
the operator default. Baselines are latest observed requests, not necessarily
explicit configuration. Peak columns include the source run key.

| Workload/container | Observed baseline | CPU peak | Memory peak | Configured target |
| --- | --- | --- | --- | --- |
| kube-apiserver/kube-apiserver | 100/100 | 292.10 E | 1594.51 E* | 300/1600 |
| openshift-apiserver/openshift-apiserver | 30/100 | 140.66 E* | 349.07 E* | 150/350 |
| openshift-controller-manager/openshift-controller-manager | 20/100 | 77.22 E* | 174.00 E* | 80/180 |
| kube-controller-manager/kube-controller-manager | 30/100 | 48.88 E* | 245.67 C* | 50/250 |
| cluster-policy-controller/cluster-policy-controller | 10/100 | 45.05 E | 147.70 E* | 50/150 |
| etcd/etcd | 100/100 | 228.36 E* | 358.80 B* | 230/360 |
| azure-cloud-controller-manager/cloud-controller-manager | 10/60 | 37.43 E | 132.89 E* | 40/140 |
| konnectivity-agent/konnectivity-agent | 10/50 | 17.47 E | 83.46 D | 20/90 |
| kube-scheduler/kube-scheduler | 10/150 | 37.70 E | 94.38 D | 40/- |
| etcd/etcd-metrics | 20/200 | 43.03 E* | 36.11 E | 50/- |
| control-plane-operator/control-plane-operator | 10/80 | 554.43 E | 227.81 E* | 560/230 |
| ignition-server/ignition-server | 10/40 | 154.33 D | 261.66 E | 160/270 |
| packageserver/packageserver | 10/250 | 84.44 E | 583.29 E | 90/590 |
| catalog-operator/catalog-operator | 10/80 | 18.12 E | 131.22 D | 20/140 |
| certified-operators-catalog/registry | 10/160 | 124.87 E | 1107.04 E | 130/1110 |
| community-operators-catalog/registry | 10/160 | 199.05 E | 847.27 E | 200/850 |
| redhat-operators-catalog/registry | 10/420 | 97.48 E | 546.61 E | 100/550 |
| redhat-marketplace-catalog/registry | 10/340 | 122.44 E | 163.08 E | 130/- |
| router/router (held) | 10/40 | 3.65 A | 29.54 C | 10/- |
| ovnkube-control-plane/ovnkube-control-plane (held) | 10/200 | 15.79 E | 200.11 E | 100/100 |

OVN stays at the existing `100m`/`100Mi` template values because its observed
`10m`/`200Mi` baseline disagrees with that template; this is not a new decrease.
Router is unchanged. Scheduler, etcd-metrics, and marketplace memory remain
unconfigured, retaining their observed `150Mi`, `200Mi`, and `340Mi` defaults.
Unsupported CSI operands are not configured. The CSI snapshot controller
operator and route controller manager are also excluded because their override
annotation suffixes exceed Kubernetes' 63-character limit.

New map identifiers follow the existing `workload-container` convention and
exact identities in the [regular-container catalog](../../hypershiftoperator/deploy/regular-resource-targets.yaml).
Management worker pool sizing remains unchanged. Live scheduling validation is
still needed for these requests.
