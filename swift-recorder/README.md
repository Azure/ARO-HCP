# SWIFT Recorder

Node-local SWIFT-v2 startup diagnostics on ARO HCP management clusters. The
recorder runs as a DaemonSet only on Linux nodes labeled
`kubernetes.azure.com/podnetwork-swiftv2-enabled=true`.

## Build And Test

```sh
make -C swift-recorder build
make -C swift-recorder image
make -C swift-recorder test-deploy
```

The image uses the same Microsoft Go / Azure Linux build pattern as mgmt-agent.
The repository root is the Docker build context. Go workspace tests and lint
include the module through `go.work`.

Deployment tests render the real pipeline and values with the default disabled
configuration, synthetic-image `slow` and `all` overlays, and both ephemeral CI
shards with a release-style mgmt-agent image override. They need Helm,
templatize, and yq, but no cluster or Azure login. To regenerate only these
fixtures, use `UPDATE=true make -C swift-recorder test-deploy`. The synthetic
digest in `testdata/` is deliberately not a runnable image.
The root `test-helm-fixtures` and `update-helm-fixtures` targets include these
tests even while the deployment pipeline is disabled.

## Deployment

`swiftRecorder.enabled` defaults to `false` and the dedicated image digest is
empty. Only ephemeral ARO-HCP CI environments `ci00` and `ci01` override this with
`enabled: true`, `captureMode: all`, and temporary `useMgmtAgentImage: true`.
They use the PR-built mgmt-agent image containing `/swift-recorder`, with the
resolved mgmt-agent image override respected by both mirroring and Helm.
Deployment is controlled solely by `swiftRecorder.enabled`, not the source
registry. The selected mgmt-agent image must contain `/swift-recorder`; an older
image will fail deployment rather than silently skip the recorder. Public,
shared/persistent and personal environments remain disabled.

Neither mirroring nor Helm deployment runs when disabled;
only the shared global identity lookup remains. The chart also renders no
resources when disabled. Setting an image override does not enable the recorder.
Configuration validation requires a nonempty dedicated digest when enabled
without the bootstrap switch; Helm always requires the selected image digest.

After a real dedicated image is available, enable it in the intended environment's
config overlay, set `useMgmtAgentImage: false`, and pin that image's digest under
`swiftRecorder.image`. This also applies to local tests of a dedicated image in a
CI environment.
Use `captureMode: slow` for normal operation;
`all` increases capture volume and is enabled by default only in ephemeral CI.
The temporary switch and dual-binary mgmt-agent packaging must be removed after
[stage 2 dedicated image onboarding](../docs/swift-recorder-deployment.md#stage-2-dedicated-image-onboarding).

For a personal environment, `make -C swift-recorder deploy DEPLOY_ENV=pers`
builds, pushes, records the actual digest, and explicitly enables the recorder for
that rollout. `record-override` and `record-latest-override` only select an image.
The root `build-services` / `record-services-override` targets include locally
built recorder images; `latest-services-override` intentionally does not require
this unpublished optional image. Merge a recorder-specific latest override when
opting in.

Disabling the pipeline after installation does **not** uninstall an existing
release. Stop it with `helm uninstall swift-recorder -n swift-recorder` on the
intended management cluster, and persist `enabled: false` to prevent redeployment.
Alternatively, apply the chart with `enabled=false` to remove its resources.

See [deployment and image onboarding](../docs/swift-recorder-deployment.md) for
external CI requirements, security review, and rollout checks.

## Runtime Contract

Startup follows mgmt-agent's Cobra validation/completion, structured slog/klog
logging and component-base metrics patterns, but does not use cluster-wide leader
election: every selected node runs its own named client-go workqueue. Readiness
waits for the local-Pod informer and the CNI input to initialize. Signal
cancellation stops and joins the controller, input, informer and HTTP server.

The recorder watches only Pods assigned to its node and reads the original local
Azure VNet `Processing ADD command` records to resolve Pod UID, sandbox ID and
namespace path. It extracts those fields without publishing the raw CNI arguments.
The active log is polled every 100 ms; existing contents are skipped at startup,
rotations are followed and incomplete/oversized records are bounded. Namespace
discovery is best-effort, not a synchronous pre-CNI hook. A missing namespace is
retried within the episode budget, not assumed to be a networking failure.

Each capture executes a short-lived invocation of this binary. It validates a
namespace FD, enters the namespace, dumps bounded rtnetlink state, and exits.
Namespace FDs and netlink sockets are never retained in the controller or its
buffers. Output includes namespace device/inode, links, MACs, master/parent
indices, up/running/carrier state, interface RX/TX/error/drop counters, addresses,
routes, rules and neighbors. These counters do not prove the selected netvsc
datapath, physical transmission, or host receipt. There is no packet capture,
ethtool-specific VF counter collection, host netlink event ring, or remediation.

In `slow` mode, captures are buffered in bounded per-Pod rings and published only
after unresolved startup exceeds the dwell. Fast startup buffers are discarded.
Missing/unknown sandbox conditions are labeled unknown, not diagnosed as SWIFT
failures. `all` publishes every observed eligible episode without relaxing any
budgets, and can inspect a newly successful sandbox during the post-success window.
Sampling continues for published slow episodes briefly after success. Capture
errors and budget drops are explicit; they do not count as successful snapshots.
All state is in memory and may be lost on process exit. One helper runs at a time;
the sampling interval is a minimum, not a per-Pod rate guarantee under load.

Standard `workqueue_*` metrics use queue name `swift-startup-recorder`; REST-client
metrics use the same component-base registry. Additional counters are
`swift_recorder_episodes_total`, `swift_recorder_captures_total`, and
`swift_recorder_overflows_total`, with bounded outcome/limit labels rather than Pod
identifiers. The Pod informer cache and Go process overhead are outside the
encoded-record byte budget.

See [the end-to-end smoke test](INTEGRATION.md) for a deterministic `all`-mode
test using a fake Kubernetes API and a real isolated namespace/helper process.

The controller receives the node name from the downward API, management cluster
name, region and environment from config, and these bounded capture settings:

| Flag | Value |
| --- | --- |
| `--health-address` | `:8091` |
| `--capture-mode` | `slow` (or explicitly `all`) |
| `--startup-dwell` | `30s` |
| `--post-success-capture` | `10s` |
| `--sample-interval` | `1s` |
| `--capture-timeout` | `500ms` |
| `--max-pods` | `32` |
| `--max-buffer-bytes` | `16777216` |
| `--max-record-bytes` | `65536` |
| `--episode-timeout` | `15m` |

The pod requests 20m CPU / 64Mi memory and has a 256Mi memory limit. It uses
`service-lifecycle-critical` priority and updates at most one node at a time.
Readiness, liveness and the metrics ServiceMonitor use port 8091.

RBAC permits only `get`, `list` and `watch` of pods across namespaces. No node,
secret, exec, lease or Azure identity access is granted. The pod uses host
networking and `ClusterFirstWithHostNet` DNS, but no host PID namespace or runtime
socket. It runs as root, drops all capabilities and adds only `SYS_ADMIN` for the
helper's `setns`, with `RuntimeDefault` seccomp and a read-only root filesystem.
`SYS_ADMIN` remains a powerful capability; this is not an unprivileged workload.

Read-only host mounts are `/var/log` at `/host/var/log`, `/var/run/netns` at the
same absolute path with `HostToContainer` propagation, and the host boot ID file
at `/host/boot-id`. The log path is `/host/var/log/azure-vnet.log`. The netns path
must already exist; `Directory`, not `DirectoryOrCreate`, avoids silently hiding
missing node prerequisites. Mount propagation makes newly created namespace
mounts visible without allowing propagation back to the host.

## Log Ingestion

Arobit decodes the `swift-recorder` container's JSON stdout in place under `log`
and retains its `kubernetes.*` routing to the existing service `containerLogs`
table. The existing `$.log.log` ingestion mapping receives an object, so queries
can access `log.record.event` and `log.record.snapshot.state` directly. All
recorder fields stay inside that object; outer Kubernetes and cluster metadata
are retained separately. Plain-text or malformed JSON remains a string. Other
containers' parsing is unchanged. There is no new Kusto table or dedicated
recorder ingestion identity.

Run the opt-in ingestion regression from the repository root using the Fluent Bit
image pinned in `config/config.yaml` (Podman by default; set
`CONTAINER_RUNTIME=docker` for Docker):

```sh
FLUENT_BIT_IMAGE=mcr.microsoft.com/oss/v2/fluent/fluent-bit@sha256:94cee54eb85d08891b179bd74be48632bf6e7968f046225abb6074b4655891d0 \
  go test ./tooling/helmtest/testrunner -run '^TestSwiftRecorderFluentBit$' -count=1 -v -timeout=3m
```

Without `FLUENT_BIT_IMAGE`, the test skips. It renders the current ConfigMap for
management and service clusters, feeds synthetic enriched container records
through the production routing and parsing filters, and substitutes JSON stdout
for Kusto output. Container networking is disabled; no Kubernetes or Azure calls
are made. It checks structured payloads, nested arrays/nulls, retained metadata,
plain-text/malformed fallback, and isolation from other containers. It does not
exercise CRI assembly, Kubernetes enrichment, or actual Kusto ingestion.
