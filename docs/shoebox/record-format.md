# Record format

[Documentation index](README.md)

The Lua functions are embedded in the [Fluent Bit ConfigMap](../../observability/arobit/deploy/templates/forwarder-configmap.yaml).
They are not separate Lua source files in this chart.

## Resource identity

`set_cluster_id_from_annotation` reads this namespace annotation:

```text
azure.microsoft.com/hcp-cluster-azure-resource-id
```

`get_namespace_annotation` checks these locations in order:

1. `kubernetes_namespace.annotations`.
2. `kubernetes.namespace_annotations`.

The function supports these two layouts because the enriched metadata layout can differ between Fluent Bit versions.
The management cluster filter enables `Namespace_annotations` and disables `Use_Kubelet`.

The identity function removes surrounding whitespace from the annotation value.
It converts the value to uppercase and assigns the result to `cluster_id`.
It also extracts the resource group and assigns its uppercase value to `resource_group`.

After successful assignment, `strip_namespace_annotations` removes the annotation maps from the record.
The function retains the other Kubernetes metadata.

If the annotation is absent or blank, the identity function leaves the record unchanged.
The later Shoebox transformation rejects a record with an absent or empty `cluster_id`.

Sources: [metadata filter](../../observability/arobit/deploy/templates/forwarder-configmap.yaml#L259), [annotation lookup](../../observability/arobit/deploy/templates/forwarder-configmap.yaml#L548), and [identity function](../../observability/arobit/deploy/templates/forwarder-configmap.yaml#L617).

This implementation reads the namespace annotation directly.
It does not read an ACM `/shoebox-config` file.

## Record selection

The [Shoebox router](../../observability/arobit/deploy/templates/forwarder-configmap.yaml#L398) receives `ocm.logs` records.
It selects specified container names or pod names that start with `capi-provider`.
The rewrite rules create a `shoeboxlogs` copy and retain the original record.

A selected pod does not guarantee an accepted category.
For example, a container other than `manager` in a `capi-provider` pod can produce an unknown category.

## Category mapping

The [get_category function](../../observability/arobit/deploy/templates/forwarder-configmap.yaml#L445) uses the following rules.

| Container | Additional condition | Category |
|---|---|---|
| `kube-apiserver` | None | `kube-apiserver` |
| `kube-controller-manager` | None | `kube-controller-manager` |
| `kube-scheduler` | None | `kube-scheduler` |
| `cloud-controller-manager` | None | `cloud-controller-manager` |
| `cluster-autoscaler` | None | `cluster-autoscaler` |
| `snapshot-controller` | None | `csi-snapshot-controller` |
| `csi-driver` | Pod name contains `azure-disk-csi-driver` | `csi-azuredisk-controller` |
| `csi-driver` | Pod name contains `azure-file-csi-driver` | `csi-azurefile-controller` |
| `manager` | Pod name starts with `capi-provider` | `capi-provider` |
| `audit-logs` | `log.raw` matches a `level` field with value `RequestResponse` | `kube-audit-admin` |
| `audit-logs` | The preceding condition is false | `kube-audit` |

The audit rule uses a string pattern with optional whitespace.
It does not parse the complete audit JSON object.
It does not test the user's administrator role.

## Transformation

The [transform_to_shoebox function](../../observability/arobit/deploy/templates/forwarder-configmap.yaml#L481) performs the following operations:

1. Read the container and pod names.
2. Read `cluster_id`.
3. Reject the Shoebox copy if `cluster_id` is absent or empty.
4. Convert a non-table log value to a table with a `raw` field.
5. Determine the category.
6. Reject the Shoebox copy if the category is `unknown`.
7. Create the output record.

The successful return value is `1, timestamp, shoebox`.
The rejection return value is `-1, 0, 0`.
These rejection conditions apply to the Shoebox copy.

| Output field | Value |
|---|---|
| `time` | `record.time` |
| `resourceId` | `record.cluster_id` |
| `location` | `record.region` |
| `operationName` | Result of `get_operation_name` |
| `category` | Result of `get_category` |
| `level` | Constant `Informational` |
| `properties` | The normalized log value |

The transformation does not derive `level` from the log severity.
It does not validate all output fields.

Both audit categories use this operation name:

```text
Microsoft.RedHatOpenshift/hcpOpenShiftClusters/kube-apiserver/audit
```

Other categories use `Microsoft.RedHatOpenshift/hcpOpenShiftClusters/` followed by the category name.
This spelling is the exact value in [get_operation_name](../../observability/arobit/deploy/templates/forwarder-configmap.yaml#L473).

## Example

The following record is illustrative.
`SUB`, `RG`, and `DEMO` are placeholders.

```json
{
  "time": "2026-10-12T10:00:00Z",
  "resourceId": "/SUBSCRIPTIONS/SUB/RESOURCEGROUPS/RG/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/DEMO",
  "location": "uksouth",
  "operationName": "Microsoft.RedHatOpenshift/hcpOpenShiftClusters/kube-apiserver",
  "category": "kube-apiserver",
  "level": "Informational",
  "properties": {"raw": "Example API server log message"}
}
```

The [forward output](../../observability/arobit/deploy/templates/forwarder-configmap.yaml#L418) sends the record to mdsd with tag `shoeboxlogs`.
The output uses the Fluent forward protocol on port `5002`.
