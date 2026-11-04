# Design

## Detection

For each eligible completed upgrade, report a currently firing alert when:

- its activation timestamp is at or after upgrade start;
- its namespace + alert name had no observed firing in
  `[upgrade start - 24h, upgrade start)`.

Activation includes time spent pending. Pending before upgrade means excluded,
even if firing starts afterward. Missing activation excludes that instance until
its timestamp arrives. Match activation to the firing instance before grouping.

The baseline stays fixed. A new problem can clear and trigger again. An alert
type present in the baseline stays excluded for that attempt. Different objects
with the same namespace + alert name share this baseline.

Detection starts only after completion. It ends when qualifying alerts clear,
completion becomes 24 hours old, or required upgrade data disappears. Problems
that clear during an upgrade and upgrades that never complete are not reported.

## Metrics

[Ownership rules](../../../observability/recording-rules/hcp-alert-identity-recordingRule.yaml)
preserve actual alert labels and values:

- `hcp:alerts`: alert states and firing history.
- `hcp:alerts_for_state`: activation timestamps.

They preserve existing HCP resource IDs or add ownership from the Namespace
annotation. Namespace ownership must be immutable and namespaces fleet-unique.
Signals carrying `hostedcontrolplane` are excluded as guest signals; source rules
must retain that label. The detector excludes missing namespaces, missing ownership
and `hcp_upgrade_new_alert="true"`.

[KSM](../../../observability/prometheus/values-mgmt.yaml) exports the latest five
`HostedCluster.status.controlPlaneVersion.history` entries through:

- `hostedClusterAPI_control_plane_version_history_started_timestamp_seconds`
- `hostedClusterAPI_control_plane_version_history_completed_timestamp_seconds`

Values are Unix timestamps. Labels include `resource_id`, `version` and
`attempt_started_at` (the original RFC3339 start). History positions are not
identities. Missing entries or completion times produce no sample.

[The upgrade rule](../../../observability/recording-rules/hcp-upgrade-recordingRule.yaml)
records `hcp:upgrade_started_timestamp_seconds` for each eligible completed attempt.
Its value is the start time. It requires an earlier retained completion to exclude
installation, completion newer than 24 hours ago, and duration between 0 and 24
hours inclusive. Source completion timestamps are trusted. Start and completion
join by HCP and attempt; collector duplicates collapse. Attempts outside the five
retained entries cannot qualify.

## Query

[The alert](../../../observability/alerts/hcp-upgrade-new-alert-prometheusRule.yaml)
scans 72 hours at one-minute steps: 24 hours of baseline, up to 24 hours upgrading,
and up to 24 hours after completion. It filters recording sample timestamps into
the baseline; alert values themselves are just 1.

Candidate selectors use `@ end()` so historical steps see today's candidate
attempts. Five branches select one attempt per HCP each with
`topk(k) unless topk(k-1)`, avoiding many-to-many joins. Rank is not identity.

Final labels retain `resource_id`, `version` and `attempt_started_at`; generated
Azure rules also retain `region`. The summary and correlation ID identify the
attempt. Same-version retries remain separate, and later upgrades do not hide
earlier qualifying attempts.

No firing samples does not prove healthy collection. Fresh installs, collection
gaps and missing history limit detection. Ingestion and evaluation add delay.
