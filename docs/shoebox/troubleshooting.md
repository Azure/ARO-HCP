# Troubleshooting

[Documentation index](README.md)

Use the [Shoebox troubleshooting guide](https://docs.google.com/document/d/1NawOAYovBze3zfzlszAM9VtC2rGV66KnFUUBywa3NGY/edit?tab=t.0#heading=h.y7pelyfh662h) for the Geneva queries and escalation procedure.
Start with the Geneva ingestion check below.
Use the management cluster checks to investigate collection or agent failures.

## Prerequisites

- Access to the Geneva dgrep tool.
- The customer HCP ARM resource ID.
- The affected category, destination, and time interval.
- The creation time of the cluster diagnostic setting.
- An authenticated management cluster kubeconfig for cluster inspection.
- The control plane namespace and arobit namespace.

The following commands inspect resources only.
Replace each placeholder before execution.
Do not include certificate secret contents in incident evidence.

## Check Geneva ingestion

First, check whether management cluster logs reach the service storage account through Geneva.
This check is separate from delivery to the customer's destination storage account.

1. Open the [sample dgrep query](https://portal.microsoftgeneva.com/s/58162C58).
2. Set the time range to the affected interval.
3. Set the resource ID filter to the customer's HCP ARM resource ID.
4. Execute the query.
5. Inspect the returned log entries, categories, locations, and blob paths.
6. Record the `blobpath` value for the next check.

Expected result: the query returns records for the affected resource and interval.

If no records appear, confirm the query filters and source log production.
If records remain absent, investigate management cluster logging and resource identity.
Escalate the failure to the Service Lifecycle team.

Check the resource ID annotation on the HostedCluster resource and the control plane namespace.
The current Lua function reads the namespace annotation.
See [resource identity inspection](#inspect-resource-identity) for this requirement.

## Check OBO delivery

Do this check after the Geneva ingestion query returns records.

1. Open the OBO dgrep query from the [Shoebox troubleshooting guide](https://docs.google.com/document/d/1NawOAYovBze3zfzlszAM9VtC2rGV66KnFUUBywa3NGY/edit?tab=t.0#heading=h.y7pelyfh662h).
2. Set the blob path filter to the `blobpath` from the ingestion check.
3. Set the correct time range.
4. Execute the query.
5. Check the `destination` and `result` columns.

Expected result: `destination` is `OBO` and `result` is `succeeded`.

If this result is absent, check the query filters before escalation.
The failure can indicate an MA agent configuration problem.
Escalate the failure to the Service Lifecycle team with the blob path and query results.

## Escalate a customer storage delivery failure

Use this procedure when Geneva ingestion and OBO delivery both succeed.

1. Check the creation time of the diagnostic setting on the cluster.
2. Check whether logs appear in the customer's storage account within 45 minutes of that time.
3. If logs remain absent after 45 minutes, escalate to the Service Lifecycle team.
4. Include the ingestion result, blob path, OBO result, and diagnostic setting creation time.

Further investigation requires access to internal Shoebox Kusto tables.
This access requires elevated permissions and approval from the Shoebox core team.
The 45-minute interval is an escalation threshold, not a delivery guarantee.

## Inspect log collection

1. List the forwarder pods.

   ```sh
   kubectl -n <arobit-namespace> get pods \
     -l app.kubernetes.io/name=arobit-forwarder
   ```

   Expected result: forwarder pods exist on the applicable management cluster nodes.

2. Inspect the affected pod.

   ```sh
   kubectl -n <arobit-namespace> describe pod <pod>
   ```

   Check the container state, restart count, environment, and volume events.
   Expected result: the pod contains `fluentbit` and `mdsd-clusterlogs`.

3. Read the Fluent Bit logs.

   ```sh
   kubectl -n <arobit-namespace> logs <pod> -c fluentbit --since=15m
   ```

   Check for filter errors and output connection errors.
   Fluent Bit service health alone does not prove customer delivery.

The [DaemonSet](../../observability/arobit/deploy/templates/forwarder-daemonset.yaml) defines these container names.

## Inspect resource identity

1. Inspect the control plane namespace.

   ```sh
   kubectl get namespace <control-plane-namespace> -o yaml
   ```

2. Locate `azure.microsoft.com/hcp-cluster-azure-resource-id` in the namespace annotations.
3. Compare its value with the customer HCP ARM resource ID.
4. Inspect the deployed Fluent Bit ConfigMap.

   ```sh
   kubectl -n <arobit-namespace> get configmap arobit-forwarder -o yaml
   ```

5. Confirm that the metadata filter includes namespace annotations.
6. Confirm that the identity function precedes the Shoebox router.

Expected result: the namespace annotation contains the intended customer resource ID.
An annotation on the HostedCluster object alone does not satisfy this lookup.
See the [identity function](../../observability/arobit/deploy/templates/forwarder-configmap.yaml#L617).

## Inspect mdsd

1. Read the mdsd logs.

   ```sh
   kubectl -n <arobit-namespace> logs <pod> -c mdsd-clusterlogs --since=15m
   ```

2. Check for certificate, authentication, and configuration errors.
3. Compare the pod's configuration version with the verified GCS version.
4. Compare the account, namespace, and environment with the intended values.
5. If certificate startup fails, read the watchdog logs.

   ```sh
   kubectl -n <arobit-namespace> logs <pod> -c mdsd-cert-watchdog --since=15m
   ```

Expected result: the agent starts with the intended configuration and no certificate access error.
The [startup wrapper](../../observability/arobit/deploy/templates/forwarder-daemonset.yaml#L128) and [watchdog](../../observability/arobit/deploy/templates/forwarder-daemonset.yaml#L236) define the recovery behavior.

## Select the next check

| Observation | Next check |
|---|---|
| Category is absent from diagnostic settings | Manifest validation, CheckIn PR merge, CreateOrUpdate result, and publication phase |
| Category exists but no source logs exist | Component state and log production |
| Operational logs exist but customer logs are absent | Resource identity, category mapping, and Lua rejection conditions |
| mdsd cannot receive records | Sidecar state, port `5002`, and Fluent Bit output errors |
| mdsd waits for its certificate | CSI volume events, SecretProviderClass, and watchdog logs |
| New XML has no effect | Published XML version and running `MONITORING_CONFIG_VERSION` |
| Ingestion succeeds but the destination is empty | Diagnostic settings, enabled category, destination configuration, and permissions |

## Verify the destination

1. Inspect diagnostic settings on the exact HCP ARM resource.
2. Confirm that the affected category is enabled.
3. Confirm that the destination is correct.
4. Select a component that produces logs during the inspection interval.
5. Inspect a destination record from that interval.
6. Compare its resource ID, category, timestamp, and properties with the expected values.

Expected result: a recent record has the intended customer identity and category.
A Storage container name alone does not establish this result.
See [verification limits](verification.md).

## Incident evidence

Record the following information:

- HCP ARM resource ID and category.
- Inspection interval and destination.
- Diagnostic setting creation time.
- Geneva ingestion query results and `blobpath`.
- OBO `destination` and `result` values.
- Pod name and container state.
- Deployed configuration version and relevant source revision.
- Last stage with a confirmed record.
- First stage with an error or absent record.

The inspection procedures do not authorize resource deletion or configuration changes.
Use the applicable operational procedure for corrective actions.
