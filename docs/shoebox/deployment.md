# Deployment

[Documentation index](README.md)

## Deployment system

The `sdp-pipelines` repository contains the Shoebox and Geneva deployment pipelines under `hcp/`.
See [sdp-pipelines/hcp/docs](https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/sdp-pipelines?path=/hcp/docs) for additional deployment documentation.
Before a rollout, select the target release branch, environment, and deployment phase.

## Select the change

| Required change | Source |
|---|---|
| Select or transform a log | ARO-HCP [ConfigMap](../../observability/arobit/deploy/templates/forwarder-configmap.yaml) |
| Change agent configuration | ARO-HCP [management values](../../observability/arobit/values-mgmt.yaml) and configuration overlays |
| Register a category | `sdp-pipelines`: `hcp/shoebox/hcpOpenShiftClusters.manifest.json` |

For an ARO-HCP configuration change, use the [configuration workflow](../configuration.md).
For public environments, follow the repository deployment process through `sdp-pipelines`.

## Update the Shoebox manifest

### Prerequisites

- Access to the current `sdp-pipelines` release configuration.
- The intended category name and source component.
- The target branch, operation, and deployment phase.
- Access to the Shoebox office hours Teams channel.

### Prepare the change

For a new category, confirm that the source produces logs and the [category mapping](record-format.md#category-mapping) supports it.
Change collection or transformation code if necessary.

1. Edit `hcp/shoebox/hcpOpenShiftClusters.manifest.json` in `sdp-pipelines`.
2. Promote the change to the applicable release branch.
3. Generate the rollout artifacts from that branch.
4. Inspect the generated manifest payload.

### Execute the manifest pipelines

1. Run `Shoebox.Manifest` to execute `ValidateManifestProd`.
2. Confirm that validation succeeds.
3. Run `Shoebox.CheckIn` to execute `CheckInManifestProd`.
4. Locate the PR that CheckIn creates in the manifest repository owned by the Shoebox team.
5. Post the PR link in the Shoebox office hours Teams channel and request a merge.
6. Wait for the Shoebox team to merge the PR.
7. Confirm that the PR is merged.
8. Run `Shoebox.CreateOrUpdate` to execute `CreateOrUpdateManifestInAllRegionsProd` for the required deployment phases.
9. Check each operation result.

CheckIn creates the manifest PR; it does not complete publication.
The PR must be merged before you run CreateOrUpdate.

### Verify the change

1. Confirm that the expected categories are available in diagnostic settings.
2. Enable the affected categories on a test HCP resource.
3. Verify actual records at the selected destination.

| Service group suffix | Operation | Main payload |
|---|---|---|
| `Shoebox.Manifest` | `ValidateManifestProd` | Manifest content |
| `Shoebox.CheckIn` | `CheckInManifestProd` | Manifest content |
| `Shoebox.CreateOrUpdate` | `CreateOrUpdateManifestInAllRegionsProd` | `deploymentPhase` |

These service groups are standalone pipelines that require manual execution.
A merge to `main` alone does not publish a manifest.

Validate and CheckIn use `file://` references.
Generation reads the file content and inserts it into the payload.
CreateOrUpdate receives `extraVars.shoebox.deploymentPhase` instead of manifest content.

**Important:** Stg and prod use the same production Geneva Actions backend.
Do not assume that a stg branch provides a separate test backend.

The selected EV2 region resolves a deployment phase.
Confirm the complete phase scope before execution.
Do not assume that the selected region is the only affected region.

## RunGenevaAction reference

`RunGenevaAction` invokes a Geneva Action from a pipeline step.
The step generates an EV2 `Microsoft.Geneva.Actions/OperationExecution` extension.
EV2 invokes the selected Geneva Action during rollout.

| Input | Handling |
|---|---|
| `file://` payload value | Read and insert file content during generation |
| Literal `"true"` or `"false"` | Emit a JSON boolean |
| Other payload values | Use EV2 scope bindings |
| Certificate configuration | Use Key Vault and DSTS authentication |

The reserved payload keys are `ga-extension-name`, `ga-operation-id`, and `ga-endpoint`.
Do not repeat these keys inside `payloadProperties`.

`Geneva.Actions` and `Geneva.ActionsBeta` publish ARO's action package.
Shoebox pipelines invoke another team's action.
`Geneva.Global` publishes logging configuration.
These operations have different purposes.
