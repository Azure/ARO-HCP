# HCPUpgradeNewAlert

Two alerts report alerts that start after HCP upgrades.

`HCPUpgradeNewAlert` reports a firing alert that started at or after an upgrade,
with no firing of that alert type in the 24 hours before upgrade start. It fires
once per HCP, exact version and new alert, carrying the triggering alert name in
`new_alertname`. It only goes to Kusto, where it records which HCPs and alerts
were affected.

`HCPUpgradeVersionNewAlerts` is the rollout signal. It counts HCPs with a firing
`HCPUpgradeNewAlert` per region, exact version and management cluster, and opens a
SEV 4 incident in the RP queue. Its correlation ID omits the management cluster, so
the clusters of a region share one incident per version while it stays open.

Detection waits for upgrade completion and stops 24 hours afterward. Upgrades lasting
more than 24 hours are excluded. Alerts that clear before completion are missed.
Problem identity is namespace + alert name: different pods or deployments are not
distinguished. Missing activation timestamps are excluded. Timing suggests a
connection to the upgrade; it does not prove one.

- [Scenarios](../../../observability/alerts/hcp-upgrade-new-alert-prometheusRule_test.html): 23 timelines.
- [Design](design.md): metrics, matching, routing and limits.
- [Testing](testing.md): local checks and E2E.
