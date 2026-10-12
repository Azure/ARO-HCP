# mdsd configuration

[Documentation index](README.md)

## Agent

The [DaemonSet](../../observability/arobit/deploy/templates/forwarder-daemonset.yaml#L111) contains the `mdsd-clusterlogs` sidecar.
The sidecar receives records from Fluent Bit in the same pod.
Its `FLUENTD_PORT` value is `5002`.

The chart sets `GCS_AUTOMATIC_CONFIGURATION=1`.
The agent requests namespace configuration from Geneva Configuration Service (GCS).
The chart supplies the account, namespace, environment, region, and version.

## Configuration references

[values-mgmt.yaml](../../observability/arobit/values-mgmt.yaml#L38) maps repository configuration to Helm values.
The [agent environment](../../observability/arobit/deploy/templates/forwarder-daemonset.yaml#L146) maps Helm values to container settings.

| Repository setting | Agent variable |
|---|---|
| `geneva.logs.cluster.accountName` | `MONITORING_GCS_ACCOUNT` |
| `geneva.logs.cluster.namespace` | `MONITORING_GCS_NAMESPACE` |
| `geneva.logs.cluster.configVersion` | `MONITORING_CONFIG_VERSION` |
| `geneva.logs.cluster.san` | `MONITORING_GCS_AUTH_ID` |
| `geneva.logs.environment` | `MONITORING_GCS_ENVIRONMENT` |
| `region` | `MONITORING_GCS_REGION` |

The version specifies which namespace configuration the agent requests.
A repository value does not prove that a running pod uses that value.
Inspect the pod after deployment.

## Certificate access

The [SecretProviderClass](../../observability/arobit/deploy/templates/forwarder-secretprovider.yaml#L1) references the configured Key Vault and managed identity client ID.
It reads the cluster logs secret and assigns the file alias `gcscert.pem`.
The [volume configuration](../../observability/arobit/deploy/templates/forwarder-daemonset.yaml#L338) connects this resource to the pod.

The agent uses `/geneva/geneva_auth` as the certificate store.
`MONITORING_GCS_AUTH_ID_TYPE` is `AuthKeyVault`.

## Certificate startup check

The [startup wrapper](../../observability/arobit/deploy/templates/forwarder-daemonset.yaml#L128) waits for a non-empty certificate file.
It then starts `/start_mdsd.sh`.
The wrapper does not validate the certificate identity.
mdsd performs certificate and SAN validation.

If the wait period expires, the wrapper exits with an error.
The [certificate watchdog](../../observability/arobit/deploy/templates/forwarder-daemonset.yaml#L236) also checks the file.
On timeout, the watchdog requests deletion of its own pod.
The DaemonSet creates a replacement pod with a new CSI mount attempt.

The [management values](../../observability/arobit/values-mgmt.yaml#L69) specify a 300-second timeout and a 15-second interval.
After the file appears, the watchdog sleeps.
It does not continuously check certificate expiration.

The [watchdog Role](../../observability/arobit/deploy/templates/forwarder-watchdog-rbac.yaml) permits pod deletion in the namespace.
The script supplies its own pod name to the delete command.

## Namespace XML

The `sdp-pipelines` repository stores the namespace XML in `hcp/geneva/global/cluster/`.
The files are `HCPCustomerLogs.int.xml` and `HCPCustomerLogs.prod.xml`.
The production file supplies the stg and prod configuration.

| XML item | Value | Purpose |
|---|---|---|
| Namespace | `HCPCustomerLogs` | Identifies the configuration namespace |
| Source | `shoeboxlogs` | Matches the incoming tag |
| Event | `ShoeboxLogs` | Identifies the configured event |
| Service identity | `AROHCPCustomerData` | Identifies the configured service |
| `onBehalfFields` | `resourceId,category` | Supplies resource and category attribution |
| `primaryPartitionField` | `resourceId` | Selects the partition field |
| `containerSuffix` | `$category` | Selects the category suffix |
| `validJsonColumns` | `properties` | Identifies the JSON field |

The Lua output and XML configuration must use compatible names and fields.
The namespace retention setting is separate from customer destination retention.
