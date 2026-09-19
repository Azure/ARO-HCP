# SWIFT Recorder Deployment

The [recorder README](../swift-recorder/README.md) documents its runtime and
security contract. Only the ephemeral ARO-HCP CI environments `ci00` and `ci01`
enable capture by default, using the selected mgmt-agent image. Public environments and persistent/shared or personal
environments (`dev`, `cspr`, `pers`, `perf`) remain disabled with `captureMode: slow`
and an empty dedicated-image digest.

## Stage 1: Temporary ARO-HCP CI Bootstrap

The existing mgmt-agent image temporarily contains `/swift-recorder` as well as
the mgmt-agent binary. The recorder DaemonSet explicitly executes
`/swift-recorder controller`; mgmt-agent's normal entrypoint is unchanged. This
allows the current release PR e2e build/deploy flow to run the recorder without
first adding a dedicated image to `openshift/release`.

In `config/config.yaml`, only `clouds.dev.environments.ci00` and `ci01` set
`enabled: true`, `captureMode: all`, and `useMgmtAgentImage: true`. Both are PR e2e
shards with management cluster names derived from the per-job `BUILD_ID` in
`tooling/templatize/settings.yaml`, not persistent CI management clusters.

The temporary switch makes the pipeline mirror the **resolved**
`mgmtAgent.image.registry`, `repository`, and `digest`, and makes Helm use that
repository and digest in the destination service ACR. Release-provided
mgmt-agent overrides therefore select the PR-built dual-binary image, without
sourcing `hack/ci/build-config-override.sh` or requiring `SWIFT_RECORDER_IMAGE`.
Direct `{{ .mgmtAgent.image.* }}` references in config do not work: raw config
templating happens before environment layers are resolved. The switch is consumed
later by the values/pipeline templates instead.

Deployment is controlled solely by `swiftRecorder.enabled`; the registry is not
a proxy for whether an image contains the binary. The selected mgmt-agent image
must include `/swift-recorder`. The old default pin and published-main fallback
images from before the bootstrap change do not: deploying them while enabled
will fail visibly. Select a current dual-binary image or explicitly disable the
recorder when testing older images. Images from both CI build-cluster registries
and ACR are accepted and mirrored into the destination service ACR.
ACR image resolution must not require the dedicated recorder repository while
`useMgmtAgentImage` is true. Do not enable this bootstrap in public or persistent
environments, or reuse an arbitrary component digest to bypass validation.

`make -C swift-recorder test-deploy` verifies both CI shards using a synthetic,
release-style mgmt-agent override. It checks per-job names, schema validation,
mirror references, the final DaemonSet image/command and `--capture-mode=all`,
CI build-cluster and ACR source registries, and isolation from non-CI defaults. It performs no
deployment or image pulls.

For a local test with a dedicated recorder image, explicitly set
`swiftRecorder.useMgmtAgentImage: false`, `swiftRecorder.enabled: true`, and the
dedicated `swiftRecorder.image` registry/repository/digest in the environment
overlay. The dedicated-image path is not restricted to the CI registry; its
repository remains `swift-recorder` for stage 2 builds and deployments.

## Stage 2: Dedicated Image Onboarding

These changes require a separate PR to `openshift/release`; this repository does
not define the external image build and promotion jobs.

1. Add a `swift-recorder` image to the Azure/ARO-HCP ci-operator configuration,
   using repository-root context and `swift-recorder/Dockerfile`. Match
   mgmt-agent's builder inputs, build arguments, promotion and image tagging.
2. Register the image in the ARO-HCP images-push step so postsubmits publish
   `swift-recorder` to the service ACR with the same commit tags as other services.
3. Add the `SWIFT_RECORDER_IMAGE` dependency/environment mapping to provisioning
   jobs that need a PR-built image. `hack/ci/build-config-override.sh` understands
   this optional digest-based reference but does not implicitly enable capture.
4. Confirm the image exists, resolves by digest, builds the controller and helper,
   and passes image/security scanning before pinning a real digest here.
5. Switch CI to the dedicated image, then remove `useMgmtAgentImage` from config,
   schema, values/pipeline templates and tests. Remove the extra recorder build
   and binary from the mgmt-agent Dockerfile. Update the bootstrap regression to
   use the dedicated release image override instead.
6. Enable any additional intended environment/region through a config overlay,
   initially with `captureMode: slow`. Keep `all` scoped to ephemeral CI; keep
   public environments disabled until their image mirroring and security approval
   are complete. Regenerate config and Helm fixtures as part of the integrating
   change.

Public INT, STG and PROD deployments additionally require an `sdp-pipelines` PR
and the corresponding EV2 rollout; see <https://aka.ms/arohcp-pipelines>. Include
the new `Microsoft.Azure.ARO.HCP.SwiftRecorder` service group when packaging the
updated topology. Optional recorder diagnostics follow mgmt-agent and do not
gate Fleet registration of the management cluster.

## Rollout Checks

Before enabling, verify that the node OS and container runtime permit `setns`
under `RuntimeDefault` with root and `SYS_ADMIN`, and that admission policies
allow this reviewed host-network/hostPath workload. No `NET_ADMIN`, `NET_RAW`,
hostPID, runtime socket or privileged container should be added as a workaround.
Verify `/var/run/netns` exists and newly created namespace mounts propagate into
the pod. Read-only hostPath mounts do not make `SYS_ADMIN` harmless; restrict who
can update this chart and service account.

```sh
kubectl get nodes -l kubernetes.azure.com/podnetwork-swiftv2-enabled=true
kubectl -n swift-recorder rollout status daemonset/swift-recorder
kubectl -n swift-recorder get pods -o wide
kubectl -n swift-recorder get service,servicemonitor
kubectl -n swift-recorder logs daemonset/swift-recorder --tail=50
```

Check `/healthz`, `/readyz` and `/metrics` on port 8091, Prometheus target discovery,
and structured records in the service `containerLogs` table. Check memory usage
against the 256Mi limit and confirm the DaemonSet is absent on non-SWIFT nodes.
Outside ephemeral CI, use `slow`; any temporary `all` diagnostic rollout requires
explicit scoping and a return to `slow`. Host networking exposes port 8091 on each
selected node; confirm there is no port conflict and that existing network
controls permit only intended monitoring access.

To stop capture, uninstall the release on the selected management cluster and
persist `swiftRecorder.enabled: false`. Merely skipping a subsequent deployment
does not remove a previously installed DaemonSet.
