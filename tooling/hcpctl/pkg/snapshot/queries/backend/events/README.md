# backend / events

## Summary

Lists Kubernetes events for backend pods in the aro-hcp namespace during the time window.

Rows with `countSemantics=max-cumulative-per-event` retain one event identity
(`cluster`, involved-object `objectUID`, and `eventUID` from `log.metadata.uid`).
Their `count` is the maximum cumulative count among exports selected by `timestamp`,
not a sum of exports. Distinct Event UIDs and recreated objects remain separate;
only sum these per-event maxima when a cumulative summary is needed.

If any identity field is missing, every selected observation remains a separate row
marked `undeduplicated-cumulative-observation`. Its `count` is an exported cumulative
value, not an independent event total; do not include it in an exact deduplicated total.

`firstSeen` and `lastSeen` are the earliest/latest reported event times (falling back
to `log.event_time`); `firstObserved` and `lastObserved` bound the selected export
timestamps. Neither pair proves continuous observation, continuous failure, or recovery.
Cumulative counts may include occurrences before the window. Counting increments
within the window requires suitable boundary observations. Exported rows, event
occurrences, and actual operation attempts are different denominators.

## What to Look For

Any anomalous events that indicate the component was crashing, failing health-checks, etc.

## Where to Go Next

Review events for other components on the service cluster to see if instability was widespread.
