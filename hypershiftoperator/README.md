# HyperShift Operator

To promote HyperShift Operator, patch the `hypershift.image.digest` parameter in the respective configuration file, e.g. [config/config.yaml](../config/config.yaml).

## Resource Requests

`hypershift.operatorResources.requests.cpu` and `.memory` are quantity strings
for the regular `operator` container in Deployment `hypershift/operator`.
Defaults remain `10m` and `150Mi`; no limits or init-container resources are added.
The resource installer renders upstream manifests, applies an exact Deployment
and container kustomize patch, then server-side applies using the upstream
`hypershift` field manager. The separate CRD installer is unchanged. The apply
container uses the existing `aksCommandRuntime.image` tooling image, as Velero does.
PriorityClasses use create-only with `AlreadyExists` tolerated, matching upstream;
they are never passed to server-side apply because their values are immutable.
Other create failures abort installation. The pinned command-runtime image
(`sha256:161971cdd3bfe66be6a9b96d67fabf8c4b54a88b841add1aaced26b0a00358b4`)
contains both `kubectl` and `jq`, used to split the rendered resources.

`hypershift.additionalMinimalResourceRequests` is an optional, default-empty map
whose keys are stable configuration identifiers. Each value has required string
fields `deploymentName` and `containerName`, and optional quantity strings `cpu`
and `memory`. Values expansion passes it to the identically named Helm value.
An entry without either resource emits nothing; omitting one resource leaves it
under upstream control. No request is inferred from missing metrics.

Entries extend only the `limitClusterSizes=true` `e2e_minimal` branch. The seven
existing literal entries stay in place, and duplicate component/container pairs
are rejected. Other size classes, including the unlimited branch's `e2e_minimal`,
are unchanged. The exact regular-container allowlist is
[`deploy/regular-resource-targets.yaml`](deploy/regular-resource-targets.yaml).
Despite the API field name `deploymentName`, the component framework also applies
these overrides to StatefulSets, including `etcd` and its sidecars.

The template adds exactly one action after the seven literal entries:

```gotemplate
{{ include "hypershift.additionalMinimalResourceRequests" . | indent 10 }}
```

The right-sizing template editor must explicitly recognize and skip that action
when editing the seven literal entries. New entries must be edited in config, not
by trying to parse the helper's Helm loop as literal YAML. Config/schema/catalog
integration is maintained separately from these chart and controller changes.

## Upstream Evidence

The configured HSO digest is annotated with source revision
`e2c566a4d792fd81fd85265e8ab858a5fc3e4044`. At that revision:

- [`cmd/install/install.go`](https://github.com/openshift/hypershift/blob/e2c566a4d792fd81fd85265e8ab858a5fc3e4044/cmd/install/install.go) registers the `install render` subcommand and uses server-side apply with field manager `hypershift` and force ownership.
- [`cmd/install/install_render.go`](https://github.com/openshift/hypershift/blob/e2c566a4d792fd81fd85265e8ab858a5fc3e4044/cmd/install/install_render.go) provides `--outputs=resources`, `--output-file`, and `--render-sensitive`. There is no `install --render` flag. Secrets must be included to preserve installation behavior; the installer does not log rendered manifests.
- [`cmd/install/assets/hypershift_operator.go`](https://github.com/openshift/hypershift/blob/e2c566a4d792fd81fd85265e8ab858a5fc3e4044/cmd/install/assets/hypershift_operator.go) supplies the operator's `10m`/`150Mi` requests.
- [`support/controlplane-component/defaults.go`](https://github.com/openshift/hypershift/blob/e2c566a4d792fd81fd85265e8ab858a5fc3e4044/support/controlplane-component/defaults.go) applies resource-request annotations after component defaults and sidecar injection, keyed by component/container, copying only supplied requests.
- [`hypershift-operator/controllers/sharedingress/router.go`](https://github.com/openshift/hypershift/blob/e2c566a4d792fd81fd85265e8ab858a5fc3e4044/hypershift-operator/controllers/sharedingress/router.go) owns the shared router Deployment spec. It is not covered by the component request override; patching that Deployment would not be durable.

The workload/regular-container allowlist is from the PR 7178 audit and framework
registration inspection at `badd40555328303ab0730ce65d28dfce43983f76`, not proof of
each observed CPO image revision. CPO overrides can select a different image from
HSO. Before tuning, verify the exact workload/container and active CPO revision.
The API matches both regular and init containers by name, with no role selector.
In particular, token minters and availability probers can move between those
lists across revisions. The chart adds no init-only targets or broad defaults;
automated init tuning needs a separate policy and revision check.

External CNO/CSI operands, shared ingress, recovery Jobs and unresolved workload
identities remain unsupported. The existing OVN entry is preserved but is not
evidence that CNO consumes this API. Guest `kube-state-metrics-hcp` is controlled
by mgmt-agent, not this sizing class.

Run the isolated rendering and kustomize tests without adding a Go module here:

```sh
go test resource_requests_test.go
```
