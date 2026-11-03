# Capacity report consumption

Fleet reads CapacityReport observations through ReadDesire. `Successful=True`
permits decoding the last observed payload unless `Degraded=True`. A missing
Degraded condition is normal for the reader. Failed observations cannot use
retained content. Missing or unobserved desires, observed absence, and malformed
payloads have distinct diagnostics. Successful observation does not establish
freshness: refresh frequency is not contractual and there is no new age cutoff.

`CapacityDataCurrent` requires a usable observation, source `ReportCurrent=True`,
and CPU, memory and Swift NIC entries in both all-HCP usage and requests. Explicit
zeros are valid; absent measurements are not zero. Failure updates conditions but
retains previous observations, timestamps, identity lists and pending reservations.
Recovery resumes observation-based reservation cleanup. Producer legacy dual
writes and backend placement behavior are unchanged. Retention of data does
**not** restore scheduling eligibility when `CapacityDataCurrent` is not True.
`ObservedResources.lastReportedAt` remains the producer's collection timestamp.

`ScalingDataCurrent` means a successfully computed usable ceiling exists,
including explicit zero; it does not mean Azure was recently verified. A prior
True condition plus nonnegative measurements for scheduling resources establishes
retention eligibility, not an arbitrary document, positive numbers, or timestamp
alone. Failed refreshes retain that ceiling and the entire condition unchanged,
without a Cosmos write. Diagnostics go to logs. Without a usable prior ceiling,
the condition is Unknown. A successful computation replaces earlier values,
including reductions and zero. Other placement gates remain unchanged.

Only a successfully observed, source-current report establishing no ready-node
sample for the relevant SKU permits theoretical CPU/memory and configured-NIC
bootstrap. Missing, failed, malformed, or noncurrent reports do not establish
absence. Missing individual dimensions in an existing ready sample never trigger
hardware substitution. Observed inputs avoid SKU lookups. A successful Azure
listing with no worker pools publishes explicit zero for all scheduling resources
without needing a report or SKU lookup; uninterpretable pool records are not
evidence of zero. Existing worker-role selection is unchanged.

Available observed contributions are summed even when another contribution is
unavailable. Partial sums are conservative relative to the observed inputs;
an entirely unknown dimension remains omitted, not a known zero. Such a partial
ceiling can be current for its known dimensions; it is not proof that every
dimension is known. Bootstrap estimates lack the same lower-bound guarantee and
are identified by `BootstrapEstimate` (or `BootstrapEstimatePartial`). Partial
observations are identified by `DataCollectedPartial`. On a failed Azure SKU
refresh, the retention rule applies rather than publishing a new estimate.
The existing secondary-NIC convention remains: an absent tag contributes no NIC
entry, explicit zero remains an entry, and malformed tags are diagnosed without
discarding other available contributions.

`ScaleCeiling.lastReportedAt` records publication of changed capacity/condition
state, not a heartbeat or expiry. Comparison precedes timestamp assignment.
Unchanged successes and retained failed refreshes do not wake downstream readers.

Requirements `SamplesAvailable` describes eligible ready-HCP samples, not
recency or fleet completeness. The default uses overall ready totals and ready
identities; named configurations use their own ready totals and summed ready
counts. Invalid unrelated overall totals do not invalidate a valid named bucket.
No samples clears both averages and sets `sampleSize` to zero, including retired
named configurations, with `SamplesAvailable=False`, reason `NoSamples`.
Reconciliation removes the legacy `DataCurrent` condition.
The JSON field `lastUpdatedAt` replaces `lastReportedAt` for requirements only;
it advances when averages, sample count or conditions change, not on every run.
Readers must migrate to the new field and condition. Scheduling and producer
timestamps keep their existing names.
Every configuration update is attempted before contextual persistence errors
are returned together. A retry does not rewrite configurations already matching
their desired averages, sample count and conditions.

The separate reservation assessment must examine the ETag-protected placement
snapshot and pending-assignment write in
`backend/pkg/controllers/cluster/placement/placement_controller.go`, followed by
the ready/not-ready membership cleanup in Fleet's capacity reporting controller.
NIC reservations do not protect against CPU/memory growth, increasing consumption
by Ready HCPs, or unobserved ceiling reductions. No eligibility or zero-NIC policy
is changed here, and this document does not claim that handoff is proven resilient.

These conditions do not verify deployment readiness, persisted Kubernetes SSA
transitions, or runtime freshness. This change does not deploy any component.
