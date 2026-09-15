# Metrics-proxy alerts

This page describes `MaestroAgentMetricsProxyDown` in
`observability/alerts/metrics-proxy-prometheusRule.yaml`, registered in
`observability/alerts-dev-services.yaml`
([ARO-26256](https://redhat.atlassian.net/browse/ARO-26256)).
That file feeds the DEV IcM action group (`devServicesAlerts` in
`monitoring.bicep`), which is how this class of service alert is deployed in
int/stg/prod. Maestro is not on the MSFT allowlist in
`alerts-msft-services.yaml`.

Incident response for Cluster Monitoring lives in the
[Cluster Monitoring TSG](https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/user-journey/cluster-monitoring-tsg.html).

The generator maps severity `"3"` to IcM Sev 3 (pre-ceiling). Effective IcM
severity is `max(severity, alertSeverityCeiling)`.

## What this is (and is not)

ARO-HCP deploys an nginx **metrics-proxy sidecar** on the Maestro **agent**
pod (`maestro` namespace on each **management** cluster). The sidecar listens
on port `8080` (`endpoint="metrics"`) and proxies Prometheus scrapes to the
agent's HTTPS `/metrics` on `8443`. The PodMonitor is
`maestro/agent/deploy/templates/maestro-agent.podmonitor.yaml`.

This is **not**:

- Platform Prometheus agent health — already covered by
  `observability/alerts/prometheus-prometheusRule.yaml` (see [Prometheus.md](Prometheus.md)).
- HyperShift **control-plane** `metrics-proxy` / guest metrics-forwarding
  (`endpoint-resolver` + `metrics-proxy` in `ocm-*` namespaces). ARO-HCP does
  not enable that path today. Do not invent absence alerts for it.
- Customer data-plane CMO (`prometheus-k8s`, Alertmanager, thanos-querier).
  Those stay on [ARO-26272](https://redhat.atlassian.net/browse/ARO-26272)–[ARO-26274](https://redhat.atlassian.net/browse/ARO-26274).

## Alert coverage summary

| Alert | `for:` | Severity | What it indicates |
|---|---:|---|---|
| `MaestroAgentMetricsProxyDown` | 10m | `"3"` | A kube-state-metrics `up` series exists for the management cluster, but `up{namespace="maestro", endpoint="metrics", pod=~"maestro-agent-.*"} == 1` is missing. The sidecar is down, returning errors, or the PodMonitor target disappeared. The left-hand side is an existence signal (same as `MiseEnvoyScrapeDown`); a series with value `0` still counts. |

The expression is the same shape as `MiseEnvoyScrapeDown`: cluster existence
via kube-state-metrics, then `unless` a healthy scrape. Service clusters are
excluded (`cluster=~".*-mgmt(-[0-9]+)?$"`) because they run maestro-**server**,
not maestro-agent. `make -C observability alerts` rewrites aggregations to
`group by (cluster, region)` in the generated Bicep (same as `PrometheusJobUp`).

## Time to page

| Alert | Earliest firing | Why |
|---|---:|---|
| `MaestroAgentMetricsProxyDown` | ~10–15m | `for: 10m` after the latest healthy scrape is no longer selected. PromQL lookback of the last good `up==1` sample can add a short upper bound. |

## Failure modes captured

### Sidecar or scrape path dead

Primary alert: `MaestroAgentMetricsProxyDown`.

Typical causes: nginx sidecar crash loop, bad metrics-access token in the
nginx config, PodMonitor dropped, or the whole `maestro-agent` pod not
running. The sidecar has **no** HTTP readiness probe, so Kubernetes Ready can
stay true while `/metrics` fails — `up` is the signal that matters.

### Prometheus agent itself is down

Do **not** debug metrics-proxy first. Use [Prometheus.md](Prometheus.md) /
`PrometheusJobUp`. This rule needs a current kube-state-metrics `up` series
from the same scraper as its left-hand anchor. If PrometheusAgent is down,
that series disappears and `MaestroAgentMetricsProxyDown` will not become
pending.

### HyperShift CP metrics-proxy

Not deployed. If a future HostedCluster enables metrics-forwarding, add a
separate HCP-workspace rule; do not overload this alert.
