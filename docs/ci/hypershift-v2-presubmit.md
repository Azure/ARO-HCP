# HyperShift v2 checks in the ARO-HCP presubmit

The `hypershift-presubmit/parallel` scenario creates one hosted cluster and node
pool through the RP. After Console setup, version-rollout readiness, and the web
application check, it invokes the PR-built HyperShift v2 conditions check against
that same cluster. `TestContext` continues to own diagnostics, service deletion,
and identity cleanup.

The integration is enabled by `HYPERSHIFT_V2_BINARY`. Existing invocations with
that variable unset retain the ARO-HCP smoke checks. An enabled invocation checks
its executable and management access before creating customer resources.

## Inputs

- `HYPERSHIFT_V2_BINARY`: path to the upstream `test-e2e-v2` executable.
- `HYPERSHIFT_V2_MANAGEMENT_KUBECONFIG`: a separate management-cluster kubeconfig;
  the parent's service-cluster `KUBECONFIG` remains unchanged.
- `ARTIFACT_DIR`: the parent test runner's artifact destination. Each invocation
  creates a fresh directory under the scenario's `hypershift-v2` directory.

The executable must contain the upstream `hosted-cluster-conditions` label. The
adapter requires exactly one passing conditions testcase in native v2 JUnit;
missing reports, empty selections, skipped checks, assertion failures, and process
errors fail the parent scenario. The child is stopped when the scenario's context
is canceled or its execution deadline expires.

Cluster discovery matches the full Azure resource ID against the HostedCluster's
`azure.microsoft.com/hcp-cluster-azure-resource-id` annotation. It uses the actual
HostedCluster name and namespace, rather than guessing names or passing the
control-plane namespace. No matches or multiple matches fail the invocation.

## CI artifacts and rollout

The coordinated `openshift/release` change supplies the v2 executable from the
HyperShift PR's test image in the ARO-HCP test runner. It creates the management
kubeconfig outside the artifact directory and records the test runner image,
binary checksum, PR refs, and HO/CPO image digests in
`hypershift-v2-provenance.json`. `hypershift-v2-build-info.txt` records the binary's
Go build metadata. Native JUnit, child output, and non-secret target metadata are
retained in the scenario's invocation directory.

Merge the HyperShift label change first, then this ARO-HCP adapter and publish its
updated E2E image. Enable the CI wiring after both images contain the required
changes. Validate a real presubmit and inspect its individual conditions testcase
before closing [CNTRLPLANE-4969](https://redhat.atlassian.net/browse/CNTRLPLANE-4969).

Additional cluster variants and API-driven lifecycle scenarios are follow-up
work. They can invoke the same adapter while keeping cluster and identity
ownership in their own ARO-HCP scenarios.
