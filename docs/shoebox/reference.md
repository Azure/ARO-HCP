# Terms and sources

[Documentation index](README.md)

## Shoebox connector reference

See [Geneva Shoebox Logs documentation](https://eng.ms/docs/products/geneva/connectors/shoeboxlogs/summary.html) for the Shoebox connector reference.

## Technical terms

Use each term with the meaning in this table.
Exact identifiers and product names retain their source spelling.

| Term | Meaning in these documents |
|---|---|
| ARM resource ID | Azure Resource Manager identifier for the customer's HCP resource |
| Category | Diagnostic log classification available through diagnostic settings |
| Configuration version | Namespace configuration version requested by mdsd |
| DaemonSet | Kubernetes workload that places pods on eligible nodes |
| Diagnostic settings | Azure configuration that selects categories and destinations |
| EV2 | Deployment system that executes the generated rollout |
| Fluent Bit | Log collection and processing program in the forwarder pod |
| GCS | Geneva Configuration Service |
| HCP | Hosted Control Plane |
| Lua | Language used by the record processing functions |
| Manifest | Resource-type document that declares diagnostic categories |
| mdsd | Geneva logging agent in the forwarder pod |
| Namespace XML | XML document that configures sources, events, and routes |
| Record | One structured log item |
| SAN | Subject Alternative Name used for certificate identity |
| Shoebox copy | Record emitted with the `shoeboxlogs` tag |
| Sidecar | Additional container in the same pod |
| Tag | Fluent Bit value used to select processing and output rules |

## Source map

| Source | Relevant code or configuration |
|---|---|
| [ConfigMap](../../observability/arobit/deploy/templates/forwarder-configmap.yaml) | Collection, metadata, routing, Lua functions, and output |
| [DaemonSet](../../observability/arobit/deploy/templates/forwarder-daemonset.yaml) | Containers, environment, startup wrapper, and volumes |
| [Management values](../../observability/arobit/values-mgmt.yaml) | Configuration references and watchdog intervals |
| [SecretProviderClass](../../observability/arobit/deploy/templates/forwarder-secretprovider.yaml) | Certificate secret mount |
| [Watchdog RBAC](../../observability/arobit/deploy/templates/forwarder-watchdog-rbac.yaml) | Pod deletion permission |
| [E2E test](../../test/e2e/shoebox_logs.go) | Destination creation and diagnostic settings |
| [Storage verifier](../../test/util/verifiers/shoebox.go) | Container existence check |
| [Event Hub verifier](../../test/util/verifiers/shoebox_eventhub.go) | Event receiver and record predicate |

Code links use repository-relative paths.
Use the function names if subsequent changes move the referenced lines.

## Deployment source files

The following paths are relative to the `sdp-pipelines` repository root.
Use the applicable release branch when you inspect or change these files.

| Path | Purpose |
|---|---|
| `hcp/shoebox/hcpOpenShiftClusters.manifest.json` | Diagnostic categories and manifest ownership |
| `hcp/shoebox/manifest/pipeline.yaml` | Manifest validation |
| `hcp/shoebox/checkin/pipeline.yaml` | Creates a PR in the Shoebox team's manifest repository |
| `hcp/shoebox/createorupdate/pipeline.yaml` | Publishes the manifest after the PR is merged |
| `hcp/geneva/global/pipeline.yaml` | Namespace configuration publication |
| `hcp/geneva/global/cluster/HCPCustomerLogs.int.xml` | Integration namespace configuration |
| `hcp/geneva/global/cluster/HCPCustomerLogs.prod.xml` | Stage and production namespace configuration |
| `tooling/pkg/types/pipeline/run_geneva_action.go` | Geneva Action rollout generation |

See [deployment](deployment.md) for the operation sequence and payload handling.
See [mdsd configuration](mdsd-configuration.md#namespace-xml) for the XML fields.
