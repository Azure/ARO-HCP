# Testing

## Local checks

Run promtool tests and regenerate Azure rules:

```bash
make -C tooling/prometheus-rules run-recording-rules-hcps run-kusto-hcps run-rp-hcps
```

[Alert tests](../../../observability/alerts/hcp-upgrade-new-alert-prometheusRule_test.yaml)
and [upgrade tests](../../../observability/recording-rules/hcp-upgrade-recordingRule_test.yaml)
cover the 23 [scenario timelines](../../../observability/alerts/hcp-upgrade-new-alert-prometheusRule_test.html).
They include recurrence, pending alerts, missing timestamps, overlapping objects,
one instance per new alert, merged same-version attempts and the 72-hour history
boundary. [Region alert tests](../../../observability/alerts/hcp-upgrade-version-new-alerts-prometheusRule_test.yaml)
cover counting distinct HCPs per region, version and management cluster. Ownership
has its own [tests](../../../observability/recording-rules/hcp-alert-identity-recordingRule_test.yaml).

Verify KSM extraction through Helm fixtures:

```bash
go test -C tooling/helmtest -run 'TestHelmTemplate/(dev-westus3-mgmt-1-arohcp-monitor|helmtest-mgmt-resources|helmtest-mgmt-resources-unset)$' -count=1
```

## E2E

Deploy the production rules first, then run:

```bash
TEST_NAME="Engineering should detect new alerts during and after a control plane upgrade" make e2e-local/run-test
```

The test creates an HCP on candidate 4.21 and upgrades to candidate 4.22; the RP
selects patch releases. Three synthetic alerts in one rule group fire before, during
and after the upgrade. The test checks:

1. Only the pre-upgrade alert fires before upgrade, and no HCPUpgradeNewAlert fires
   for the HCP.
2. After completion, all three actual alerts fire, and HCPUpgradeNewAlert fires for
   the during-upgrade and post-upgrade alerts but not the pre-upgrade alert. The
   post-upgrade alert has no `resource_id` and fires in the control-plane namespace,
   so its HCPUpgradeNewAlert proves ownership through the Namespace annotation.
3. HCPUpgradeNewAlert carries the management cluster of its triggering alert, and
   HCPUpgradeVersionNewAlerts fires for the region, that cluster and the exact version
   reported by HCPUpgradeNewAlert. It is not tied to the test HCP: another HCP upgraded
   to the same version on that cluster could also make it fire.

The post-upgrade wait allows the remaining upgrade time plus the observation time,
since the API operation can finish before the control plane does.

The test does not cover HCPUpgradeNewAlert resolving. Deleting a rule group does not
resolve its Azure alert instance, so removing a synthetic alert cannot show it clearing.

Azure Alerts Management provides the observed state. AMW alert rules do write
`ALERTS`, and both alerts rely on it, but it has lagged behind Azure state, so it is
not used to judge E2E success. Queries match alerts by label selectors; errors never
count as absence. The single-upgrade E2E checks integration; promtool checks
per-attempt attribution and timing limits.

Personal-dev environments do not feed the shared Kusto cluster, so the E2E does not
check the `alertEvents` rows. IcM incidents and merging across management clusters
are not exercised either.

Logs distinguish API operation completion from actual upgrade completion. The
post-upgrade alert requires completed version history. State changes include Azure
instance IDs and timestamps; waits include elapsed time.

The runner needs rule-group write/delete and workspace alert-read permissions,
plus normal cluster-test access. The temporary group has no notification actions
and is cleaned up before customer resources, including after failure. Its alert
instances stay Fired in Azure after cleanup.

## Validation limits

Historical AMW checks covered eight upgrades and verified timestamp filtering and
`@ end()`. They establish query feasibility, not fleet-scale cost. In a personal-dev
workspace with two HCPs (547 `hcp:alerts` series over 72 hours), one HCPUpgradeNewAlert
evaluation took 2–4 seconds. The rule evaluates every minute, so measure cost with
fleet-sized data before production rollout. Local tests and
historical queries do not replace a passing E2E of the deployed rules.

AMA collection remains unverified. Before claiming support, check both history
metrics in AMW through AMA, including labels and attempt identity across history
slot shifts. Carry KSM history extraction into any replacement standalone chart.

When replacing the episode-based prototype, remove its `hcp-alert-episodes` Azure
rule group; incremental deployment does not remove obsolete groups.

When deploying a renamed Azure rule group, remove the previous group to avoid
duplicate alerts. Incremental deployment does not delete it automatically.
