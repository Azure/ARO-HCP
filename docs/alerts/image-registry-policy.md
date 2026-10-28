# Image Registry Policy Alerts

These alerts detect violations of the `image-registry-allowlist-policy`
ValidatingAdmissionPolicy (VAP), which restricts pod container images on
ARO-HCP service and management clusters to a set of allowed registries. The
policy and its binding are deployed by the `image-registry-policy` Helm release
to every service and management cluster in every environment; the allowed
registry list lives in the `image-registry-allowlist-config` ConfigMap in the
`image-registry-policy` namespace.

All environments currently run the policy in `Deny` mode. The global default
in `config/config.yaml` is `Audit` (so any newly onboarded environment starts
in audit mode), and `dev` plus the Microsoft cloud overlay (int/stg/prod)
override it to `Deny`.

## The alerts at a glance

- **`ImageRegistryPolicyDenied`** (warning / Sev 3, `for: 1m`) — fires when the
  policy has **denied** pod admissions in the last 15 minutes. Pods with images
  from non-approved registries were blocked from running. Customer-visible
  impact is possible if the blocked pods belong to a hosted control plane.
- **`ImageRegistryPolicyAuditViolation`** (warning / Sev 3, `for: 5m`) — fires
  when the policy has logged **audit** violations in the last 15 minutes. Pods
  with images from non-approved registries are running unblocked, which means
  the policy is in `Audit` mode on that cluster (rollback, or a new
  environment inheriting the default) or a violation slipped through
  enforcement. Audit-mode hits are leading indicators of future enforcement
  failures and should be investigated before the policy is flipped to `Deny`.

Both alerts aggregate `increase(apiserver_validating_admission_policy_check_total{policy="image-registry-allowlist-policy", ...}[15m])`
per `cluster` and fire on any non-zero rate.

## Metric

`apiserver_validating_admission_policy_check_total` is exposed by each
cluster's kube-apiserver and collected by Azure Managed Prometheus. The
`enforcement_action` label distinguishes the outcome (`deny`, `audit`, `warn`,
`allow`); `error_type` describes the CEL evaluation result (`invalid_error`
for ordinary policy violations).

The metric is **not** part of AMA's minimal ingestion profile. It is collected
only because `service-lifecycle` adds it to the `apiserver` keep-list in the
`ama-metrics-settings-configmap`. If the metric disappears from Grafana, check
that keep-list entry first — without it neither alert can fire.

## Triage

### 1. Identify the cluster

The alert's `cluster` label is the AKS cluster name (for example
`int-westus3-mgmt-1`). The policy runs on both `-svc-` and `-mgmt-` clusters.

### 2. Find the offending pods and images

Query `kubeAudit` in the environment's `ServiceLogs` Kusto database.

Denied admissions (HTTP 403 responses carry the full policy message,
including the offending images and the currently deployed allowlist):

```kusto
kubeAudit
| where timestamp > ago(1d)
| where responseStatus.message contains "image-registry-allowlist-policy"
| project timestamp, cluster = tostring(split(resourceId, "/")[-1]),
          verb, requestURI, message = responseStatus.message
| order by timestamp desc
```

Audit-mode violations (the kube-apiserver annotates the audited request; the
annotation value is a JSON array with `message`, `policy`, `binding`, and the
offending `validationActions`):

```kusto
kubeAudit
| where timestamp > ago(1d)
| where annotations has "validation.policy.admission.k8s.io/validation_failure"
| project timestamp, cluster = tostring(split(resourceId, "/")[-1]),
          verb, requestURI, annotations
| order by timestamp desc
```

To inspect the metric itself in Grafana (services data source of the
environment):

```promql
sum by (cluster, enforcement_action) (
  increase(apiserver_validating_admission_policy_check_total{
    policy="image-registry-allowlist-policy"
  }[15m])
)
```

### 3. Decide on a fix

- **A workload legitimately needs a new registry** — add it to
  `imageRegistryPolicy.extraAllowedRegistries` in `config/config.yaml`
  (global defaults or the environment-specific overlay), run
  `cd config && make materialize`, and let the pipelines roll the
  `image-registry-policy` release out. The deny message lists the exact
  registries currently allowed.
- **An unexpected image is running (audit hit)** — identify the deploying
  workload and fix it at the source before the policy enforces. Audit hits in
  a brand-new environment are expected until its overlay sets
  `validationActions: Deny`.
- **E2E test noise** — nonlocal e2e runs create hosted clusters whose
  payload images (for example digest-pinned `quay.io/openshift-release-dev/ocp-release`)
  may not be in the allowlist deployed to that environment yet, producing
  short deny bursts in `ocm-*` namespaces on management clusters. Verify the
  deployed `image-registry-allowlist-config` ConfigMap on the cluster before
  treating a burst as a regression.

### 4. Verify

After a config change reaches the cluster, confirm the allowlist in the
ConfigMap (`kubectl -n image-registry-policy get configmap
image-registry-allowlist-config -o jsonpath='{.data.allowedRegistries}'`).
Use the 15-minute `increase(...)` query above to check for new violations;
the cumulative `_check_total` counter does not reset when the allowlist changes.
Once violations stop, the 15-minute increase falls to zero as earlier hits
age out of the window.

## Alert delivery

The rules are defined in
[`observability/alerts/imageRegistryPolicy-prometheusRule.yaml`](../../observability/alerts/imageRegistryPolicy-prometheusRule.yaml),
generated by `tooling/prometheus-rules` into
`generatedDevServicesPrometheusAlertingRules.bicep`, and evaluated against
the services Azure Monitor workspace in every environment.
