# Maestro agent deployment

The agent chart's `enabled` value is `false` in both its defaults and the pipeline
values. No configuration-level toggle is added. The management pipeline still
upgrades the existing `maestro-agent` release, but the disabled chart omits its
agent-owned Deployment, RBAC, service accounts, Secrets, SecretProviderClass,
PodMonitor, and metrics-proxy resources.

Omitting previously release-managed resources requests their removal during a
Helm upgrade, rather than scaling the agent to zero or skipping the release.
Release ownership, finalizers, and external resource-policy overrides can affect
deletion; rendering tests do not prove removal on a running cluster. External Helm
values can override the chart default and must be reviewed before rollout.

The shared AppliedManifestWork CRD remains independently controlled by
`installAppliedManifestWorkCRD` and retains its `helm.sh/resource-policy: keep`
annotation. This chart change does not remove namespace or custom-resource instances,
ACM resources, or Key Vault credentials. The agent Secret contains MQTT
configuration; its SecretProviderClass references the certificate in Key Vault.
The separate service-cluster Maestro server release is unchanged, as are Fleet
registration and readiness gates.

The obsolete `MaestroAgentMetricsProxyDown` alert is also retired, removing its
exclusive `metrics-proxy` rule group from the generated monitoring deployment.
The monitoring ARMStack uses `actionOnUnmanage: delete`; removal of an existing
group still depends on its live stack ownership and a successful deployment.
The nine Maestro server alerts and all other alert groups remain unchanged.

## Rollout prerequisites

Keep this change draft until the prerequisites in the
[public rollout plan](https://github.com/Azure/ARO-HCP/pull/7722) are satisfied:

1. Merge the separate Maestro log-gathering and analysis cleanup in PR #7722.
2. Land the corresponding Clusters Service change and image bump.
3. Roll out this agent-disabling change after that image bump.
4. Handle full Maestro removal, including Fleet registration, separately.

Apart from that agent-only alert, this PR changes no alerts, images, log queries,
metric expectations, dashboards, or shared observability/reporting code.
PR #7722 is log cleanup, not a replacement
for agent-dependent metric or alert expectations. Those remaining expectations
must be assessed separately before rollout; this PR does not suppress them or
claim the prerequisites are already merged. Enabled/disabled chart tests and
shared-CRD checks are local validation, not proof of E2E success or safe live rollout.
