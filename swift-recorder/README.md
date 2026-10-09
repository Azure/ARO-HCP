# SWIFT Recorder

Node-local SWIFT-v2 diagnostics on ARO HCP management clusters. A DaemonSet runs
on Linux nodes labeled `kubernetes.azure.com/podnetwork-swiftv2-enabled=true`
and emits three independent diagnostic streams:

- Startup captures: bounded namespace snapshots for slow Pod startup, or every
  observed eligible episode in `all` mode.
- Root-namespace monitoring: synthetic NIC and VF link changes and heartbeats.
- Continuous router checks: Ignition, KAS, DNS, and worker-outbound reachability,
  independently of startup capture mode. No packet capture or remediation.

## Build And Test

Run from the repository root:

```sh
make -C swift-recorder build
make -C swift-recorder image
go test ./swift-recorder/...
make test-helm-fixtures
```

The image follows mgmt-agent's Microsoft Go / Azure Linux build pattern, with
repository-root Docker context. Workspace tests and lint include this module.
After config or chart changes, `make -C config materialize` regenerates the
standard `tooling/helmtest` fixtures. See the [integration smoke test](INTEGRATION.md)
for deterministic `all`-mode validation with a fake API and real namespace/helper.

## Deployment

`swiftRecorder.enabled` defaults to `true` in every environment; router checks
always run with it. Startup capture defaults to `slow`, except ephemeral
`ci00`/`ci01` use `all`. Source-image selection and pins are unchanged; deployment
requires a repo-built image override. Defaults alone do not deploy anything.

After reviewing the [permissions and rollout prerequisites](../docs/swift-recorder-deployment.md),
build, push, record the actual digest, and deploy to a personal environment:

```sh
make -C swift-recorder deploy DEPLOY_ENV=pers
```

See the deployment guide for [image selection](../docs/swift-recorder-deployment.md#ci-image-bootstrap),
[dedicated image onboarding](../docs/swift-recorder-deployment.md#stage-2-dedicated-image-onboarding),
and [removal](../docs/swift-recorder-deployment.md#rollout-checks).
Skipping deployment does not uninstall an existing release. This workload has
powerful namespace, runtime-socket, and CA signing-key access; it is not unprivileged.

## Runtime Contract

[LOG_RECORDS.md](LOG_RECORDS.md) is the runtime and emitted-schema reference:
[startup](LOG_RECORDS.md#startup-captures), [root-node state](LOG_RECORDS.md#root-node-state-records),
and [router checks](LOG_RECORDS.md#runtime-router-check-records), including probe
roles, TLS identities, cadence, failure summaries, and incomplete-coverage rules.
All streams use the existing service `containerLogs` table. The
[E2E telemetry verifier](../test/e2e/README.md#swift-telemetry-coverage) checks fresh,
complete router summaries for an exact customer resource.
