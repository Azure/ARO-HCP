# Prometheus Rules

Alerts are defined as `Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01` bicep resources.

To make usage easier, there is `tooling/prometheus-rules` it can be used to convert prometheus rules into azure managed prometheus rules groups.

## Configure rules folder

In order to add rules for a service a rules folder for this service must be configured. 

Open `observability/observability.yaml` and edit it:

```yaml
prometheusRules:
  rulesFolders:
  - ../cluster-service/alerts
```

Add a new entry to the list accordingly.

## Add rules

Alerts follow the `PrometheusRule` crd definition, example:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: kubernetes-monitoring-rules
  namespace: monitoring
spec:
  groups:
  - name: InstancesDownV1
    rules:
    - alert: InstancesDownV1
      expr: sum(up{job="app"}) == 0
      labels:
        severity: critical
      annotations:
        summary: "All instances of the App are down"
        description: "All instances of the App are down"
```

All rules must come with a test file, this test uses promtool test definition, [documentation](https://prometheus.io/docs/prometheus/latest/configuration/unit_testing_rules/):

```yaml
rule_files:
- testing-prometheusRule.yaml
evaluation_interval: 1m
tests:
- interval: 1m
  input_series:
  - series: 'up{job="app", instance="app-1:2223"}'
    # 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
    values: "0x14"
  - series: 'up{job="app", instance="app-2:2223"}'
    # 1 1 1 1 1 0 0 0 0 0 0 0 0 0 0 1 1 1 1 1
    values: "1x4 0x9 1x4"
  alert_rule_test:
  - eval_time: 4m
    alertname: InstancesDownV1
  - eval_time: 5m
    alertname: InstancesDownV1
    exp_alerts:
    - exp_labels:
        severity: critical
      exp_annotations:
        summary: "All instances of the App are down"
        description: "All instances of the App are down"
  - eval_time: 15m
    alertname: InstancesDownV1
```

## Dev notifications

In non MSFT environments (i.e. integrated dev) you can add e-mail alerts by adding your email to the `devAlertingEmails` key.

Example:

```yaml
monitoring:
  devAlertingEmails: "aro-hcp-service-lifecycle-team@redhat.com,user@redhat.com"
```

Simply add an e-mail address by append the comma separated string.  

Be aware when using google groups, they need to be configured to accept emails from outside of the organization.

To test the notifications, you can use the `test` functionality. Example using CLI:

```bash
az monitor action-group test-notifications create \
   --action-group aro-hcp-service-lifecycle-team@redhat.com \
   --resource-group hcp-underlay-dev \
   -a email test aro-hcp-service-lifecycle-team@redhat.com usecommonalertsChema \
   --alert-type budget
```

Reponse:

```json
{
  "actionDetails": [
    {
      "MechanismType": "Email",
      "Name": "test",
      "SendTime": "2025-05-27T08:22:38.9760615+00:00",
      "Status": "Succeeded"
    }
  ],
  "completedTime": "2025-05-27T08:24:48.766338+00:00",
  "context": {
    "notificationSource": "Microsoft.Insights/TestNotification"
  },
  "createdTime": "2025-05-27T08:22:38.6135898+00:00",
  "state": "Complete"
}
```
