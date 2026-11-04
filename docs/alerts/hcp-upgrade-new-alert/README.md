# HCPUpgradeNewAlert

`HCPUpgradeNewAlert` reports a firing alert that started at or after an upgrade,
with no firing of that alert type in the 24 hours before upgrade start.

It waits for completion and stops checking 24 hours afterward. Upgrades lasting
more than 24 hours are excluded. Alerts that clear before completion are missed.

Each HCP, version and upgrade attempt gets its own HCPUpgradeNewAlert. Problem identity
is namespace + alert name: different pods or deployments are not distinguished.
Missing activation timestamps are excluded. Timing suggests a connection to the
upgrade; it does not prove one.

- [Scenarios](../../../observability/alerts/hcp-upgrade-new-alert-prometheusRule_test.html): 23 timelines.
- [Design](design.md): metrics, matching and limits.
- [Testing](testing.md): local checks and E2E.
