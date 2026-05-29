# Pre-Merge Node Rollout Check

## Motivation

On April 28, 2026, a production rollout to EASTUS2 triggered an unexpected
rolling replacement of customer worker nodes across hosted clusters
([ARO-26610](https://redhat.atlassian.net/browse/ARO-26610)). The root cause
was a combination of changes to `hypershift install` flags (`--registry-overrides`
and `IMAGE_SHARED_INGRESS_HAPROXY`) that altered the rendered MachineConfig hash,
causing Kubernetes to roll all MachineDeployments.

The pre-merge node rollout check (`TestNodeRolloutConfig`) was introduced to
prevent this class of incident by making node-affecting flag changes visible
during code review.

## How the Check Works

The test lives in
[`tooling/helmtest/testrunner/node_rollout_config.go`](../tooling/helmtest/testrunner/node_rollout_config.go)
and runs as part of `make test` in the `tooling/helmtest` module.

1. **Render** the HyperShift operator Helm chart for each management cluster in
   the topology.
2. **Extract** the shell script from the rendered `install-hypershift` Job
   ([`hypershiftoperator/deploy/templates/installer.job.yaml`](../hypershiftoperator/deploy/templates/installer.job.yaml)).
3. **Parse** every flag and `--additional-operator-env-vars` value from the
   `hypershift install` command.
4. **Classify** each flag/env var against the registries in
   `flagCategories` and `envVarCategories`.
5. **Write** a `NodeRolloutConfig` YAML struct containing the node-affecting
   values and any unclassified flags to a golden fixture file
   (`zz_fixture_TestNodeRolloutConfig_*.yaml`).
6. **Compare** the output to the committed fixture. Any diff fails the test.

### Flag Classification

Every flag is placed into one of three buckets:

| Bucket | Effect | Example |
|--------|--------|---------|
| **`flagSafe`** | Operator-level only; no impact on worker nodes | `--enable-conversion-webhook`, `--platform-monitoring` |
| **`flagNodeAffecting`** | Changes propagate into MachineConfig/MachineDeployment hashes and trigger worker node replacement | `--registry-overrides`, `IMAGE_SHARED_INGRESS_HAPROXY` |
| **Unclassified** | Flag is not in the registry; surfaces in `additionalInstallArgs` in the fixture, forcing explicit review | Any new flag |

The "unclassified" default is intentionally the safest: unknown flags block the
PR until someone classifies them.

## How Initial Values Were Gathered

The initial flag classification was determined by tracing the HyperShift
operator source code from each `hypershift install` flag down to the
MachineDeployment template hash computation. The key question for each flag:
**does changing this value alter the hash that names the bootstrap
DataSecret, which in turn triggers a MachineDeployment rolling update?**

### The HAProxy / Registry-Overrides Chain (root cause of ARO-26610)

The investigation ([ARO-26610 comment](https://redhat.atlassian.net/browse/ARO-26610?focusedCommentId=16862616))
traced the following path through HyperShift:

1. `--registry-overrides` flag is parsed in `hypershift-operator/main.go`
2. `RegistryOverrides` is applied to **all** ImageStream tags via string
   replacement (`support/releaseinfo/registry_mirror_provider.go`)
3. The HAProxy image is extracted with overrides already applied
   (`controllers/nodepool/nodepool_controller.go`)
4. The image URL is embedded in a static pod spec
   (`controllers/nodepool/apiserver-haproxy/haproxy.go`)
5. The pod spec is serialized into a MachineConfig (`haproxyRawConfig`)
6. `haproxyRawConfig` is included in `mcoRawConfig`, which is hashed
   (`controllers/nodepool/config.go`)
7. The hash determines the bootstrap Secret name
   (`controllers/nodepool/token.go`)
8. A Secret name change causes a MachineDeployment `.spec.template` update,
   triggering a rolling replacement
   (`controllers/nodepool/capi.go`)

**Why HAProxy but not other data-plane images?** HAProxy is deployed as a
static pod baked into the ignition config (it must run without API server
access for bootstrapping). Other data-plane images (kube-proxy, OVN, etc.) use
IDMS applied by CRI-O at runtime, so registry override changes do not alter
ignition content.

### Documented Rollout Triggers in HyperShift API

The HyperShift API
([`api/hypershift/v1beta1/hostedcluster_types.go`](https://github.com/openshift/hypershift/blob/7aa62352dc5c63ebeebff1b83ce92cd6df748e43/api/hypershift/v1beta1/hostedcluster_types.go#L753))
marks fields that trigger node rollouts with a `+rollout` tag. At least 6
`HostedCluster.Spec` fields are explicitly documented:

| Field | Why It Triggers Rollout |
|-------|----------------------|
| `PullSecret` | Name change alters ignition secret references |
| `SSHKey` | Baked into ignition MachineConfig |
| `ImageContentSources` | Becomes IDMS in ignition payload |
| `Configuration.Proxy` | Alters MachineConfig proxy settings |
| `Configuration.Image` | Affects image references in ignition |
| `AdditionalTrustBundle` | Injected into ignition trust store |

### Full List of Known Rollout Triggers

Combining the `+rollout`-tagged API fields with the code-traced operator-level
flags, there are **9 confirmed triggers**:

**Operator-level (3):**
- `--registry-overrides` -- when HAProxy config is managed (the default unless
  CPO has a skip label)
- `--control-plane-operator-image` -- when HTTP proxy is configured
- `IMAGE_SHARED_INGRESS_HAPROXY` -- when shared ingress is enabled

**HostedCluster.Spec (6):**
- `ImageContentSources`
- `Configuration.Proxy`
- `Configuration.Image`
- `AdditionalTrustBundle`
- `PullSecret` (name change)
- `Release.Image`

**Requires further analysis (2):**
- `--feature-gates` and `HYPERSHIFT_FEATURESET` -- impact depends on which
  specific gate is toggled; per-gate analysis is needed.

## Maintaining the Check

### When a new flag is added to `hypershift install`

1. The test will **fail** because the new flag appears in
   `additionalInstallArgs` in the golden fixture.
2. Determine whether the flag is safe or node-affecting:
   - Trace the flag through the HyperShift operator source to see if it
     reaches the MachineDeployment template hash.
   - Check whether the flag's value is embedded in any MachineConfig,
     ignition payload, or bootstrap secret.
   - If the flag only affects operator-level behavior (webhooks, CRD
     management, monitoring), it is safe.
3. Add the flag to `flagCategories` (or `envVarCategories` for env vars) in
   `node_rollout_config.go` with the appropriate classification.
4. If the flag is `flagNodeAffecting`, add a corresponding field to
   `NodeRolloutConfig` and extraction logic, so the value is tracked
   explicitly in the fixture.
5. Update the fixture:
   ```
   UPDATE_NODE_ROLLOUT_FIXTURE=true go test -run TestNodeRolloutConfig -count=1 ./...
   ```
6. Document the impact analysis in the PR description.

### When a node-affecting value changes

For example, adding a new `--registry-overrides` entry:

1. The test will **fail** because the fixture diff shows the changed value.
2. Document in the PR description:
   - What changed and why.
   - Expected impact: rolling replacement of all worker nodes across all
     hosted clusters in affected regions.
   - Rollout coordination plan with SRE.
3. Update the fixture:
   ```
   UPDATE_NODE_ROLLOUT_FIXTURE=true go test -run TestNodeRolloutConfig -count=1 ./...
   ```

### When the upstream HyperShift API adds new `+rollout` fields

Periodically review the upstream
[`hostedcluster_types.go`](https://github.com/openshift/hypershift/blob/main/api/hypershift/v1beta1/hostedcluster_types.go)
for new `+rollout`-tagged fields. These indicate new HostedCluster spec
changes that will trigger node rollouts. While this pre-merge check focuses on
`hypershift install` flags (operator-level), awareness of the full rollout
surface area helps inform rollout scheduling decisions.

### If uncertain about a flag

**Do not classify it.** Leaving a flag unclassified causes the test to surface
it in `additionalInstallArgs`, which is the safer default. The PR reviewer can
then make a classification decision with full context.
