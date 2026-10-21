# Unblocking a merge blocked by the merge gate

The **merge gate** is a CI check that blocks a PR when it touches a component that
has production alerts firing which have not been accounted for. Only components
whose image is built from this repository are gated. There are two ways to unblock a
merge: land only bug fixes, or declare each firing alert as fixed.

## Bug-fix PRs are never blocked

So that alerting never gets in the way of fixing bugs, a PR whose commits **all** use
the conventional-commits `fix:` prefix bypasses the gate entirely and is allowed
without any alert check. The type must be `fix` (optionally scoped and/or breaking):

```
fix: correct the retry backoff
fix(backend): stop leaking the lease on restart
fix!: change the default that was causing outages
```

If even one commit in the PR uses another type (`feat:`, `chore:`, a bare subject,
etc.), the normal gate applies. For a batch, every constituent PR's commits must be
`fix:` for the bypass to apply.

## Ameliorating an alert

When the gate blocks your PR it links to a self-contained Kusto (ADX) query whose
result rows are the alerts blocking you — each with its `region`, `component`,
`alertname`, `severity`, `firedDateTime`, and `labels`; the query's header comments
restate what it checked. Once you understand a firing alert — because your PR fixes
it, or because a fix has already merged — declare it fixed with an
`Ameliorates-Alert:` trailer in the commit that fixes it.

### Trailer syntax

Add one trailer per alert to the commit that contains (or represents) the fix:

```
Ameliorates-Alert: <alertname>
```

Optionally scope it to a specific firing with labels:

```
Ameliorates-Alert: <alertname>{key=value,key2=value2}
```

- `alertname` is the `alert:` name from the `PrometheusRule`
  (`alertContext.labels.alertname` on the firing alert).
- Labels are a **subset**: list only the labels that identify the specific firing you
  fixed; any others on the alert are ignored. Omit the braces to match **any** firing
  of that alertname.
- The trailer is **repeatable** — one line per alert a commit addresses.

### Adding trailers

You can simply append the trailers as literal lines to the end of your commit message,
or add these with `git commit --trailer` on versions of `git` newer than 2.32:

```console
$ git commit -m "Fix leader-election lease renewal on backend restart" \
    --trailer "Ameliorates-Alert: LeaderElectionLeaseStale" \
    --trailer "Ameliorates-Alert: BackendControllerQueueDepthHigh{name=operationphasemetrics}"
```

To add one to the commit you just made, amend it:

```console
$ git commit --amend --no-edit \
    --trailer "Ameliorates-Alert: KubeNodeNotReady{node=aks-nodepool1-0}"
```


### Example

```
Fix leader-election lease renewal on backend restart

Ameliorates-Alert: LeaderElectionLeaseStale
Ameliorates-Alert: BackendControllerQueueDepthHigh{name=operationphasemetrics}
```

`LeaderElectionLeaseStale` suppresses any firing of that alert for the touched
component; `BackendControllerQueueDepthHigh{name=operationphasemetrics}` suppresses
only firings whose `name` label is `operationphasemetrics`.

### Which commits count

A declaration in **your PR's own commits** counts, so a PR can merge on the strength
of its own fix before it is deployed. **Already-merged** declarations count per
region: a fix merged after what a region is running, but not yet deployed there,
suppresses the alert in that region; but if a region already runs the fix and the
alert is *still* firing there, it keeps blocking — the fix did not work.

## Mapping a directory to a component

The gate maps changed paths to components using
[`observability/component-map.yaml`](../../observability/component-map.yaml). If your
service is not yet mapped and you want it gated, add an entry mapping its path glob
to its alert `component` label; see the criteria in that file.

## See also

- [Writing Alerts](../alerts.md)
- [Alert Verification](../alert-verification.md)
