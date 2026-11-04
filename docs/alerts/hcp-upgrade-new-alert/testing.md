# Testing

## Local checks

Run promtool tests and regenerate Azure rules:

```bash
make -C tooling/prometheus-rules run-recording-rules-hcps run-rp-hcps
```

[Alert tests](../../../observability/alerts/hcp-upgrade-new-alert-prometheusRule_test.yaml)
and [upgrade tests](../../../observability/recording-rules/hcp-upgrade-recordingRule_test.yaml)
cover the 23 [scenario timelines](../../../observability/alerts/hcp-upgrade-new-alert-prometheusRule_test.html).
They include recurrence, pending alerts, missing timestamps, overlapping objects,
separate attempt expiry and the 72-hour history boundary. Ownership has its own
[tests](../../../observability/recording-rules/hcp-alert-identity-recordingRule_test.yaml).

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
selects patch releases. Three isolated synthetic alert groups fire before, during
and after the upgrade. The test checks:

1. Only the pre-upgrade alert fires before upgrade.
2. After completion, all three actual alerts and HCPUpgradeNewAlert fire.
3. Removing the post-upgrade alert leaves HCPUpgradeNewAlert firing.
4. Removing the during-upgrade alert clears HCPUpgradeNewAlert while the pre-upgrade
   alert remains.

Azure Alerts Management provides the observed state. `ALERTS` metrics have been
absent even while Azure reported Fired, so they are not used to judge E2E success.
Queries match HCP and alert name; errors never count as absence. The single-upgrade
E2E checks integration; promtool checks per-attempt attribution and timing limits.

Logs distinguish API operation completion from actual upgrade completion. The
post-upgrade alert requires completed version history. State changes include Azure
instance IDs and timestamps; waits and rule deletions include elapsed time.

The runner needs rule-group write/delete and workspace alert-read permissions,
plus normal cluster-test access. Temporary groups have no notification actions
and are cleaned up before customer resources, including after failure.

## Validation limits

Historical AMW checks covered eight upgrades and verified timestamp filtering and
`@ end()`. They establish query feasibility, not fleet-scale cost. Local tests and
historical queries do not replace a passing E2E of the deployed rules.

AMA collection remains unverified. Before claiming support, check both history
metrics in AMW through AMA, including labels and attempt identity across history
slot shifts. Carry KSM history extraction into any replacement standalone chart.

When replacing the episode-based prototype, remove its `hcp-alert-episodes` Azure
rule group; incremental deployment does not remove obsolete groups.

When deploying a renamed Azure rule group, remove the previous group to avoid
duplicate alerts. Incremental deployment does not delete it automatically.
