# ARO-HCP-BACKEND

## Development Workflow

The Backend can be built and tested locally and in personal DEV environments using a set of Makefile targets.

- **make run:** runs the Backend binary locally
- **make deploy:** builds the Backend container image, uploads it to the DEV service ACR and deploys it to a personal DEV cluster

The `Makefile` has access to a set of environment variables representing configuration from the `config/config.yaml` file. The environment variables are made available via the `include ../setup-templatize-env.mk` directive in the `Makefile`, which processes and includes the [Env.mk](Env.mk) file. This is the file you need to modify to provide additional environment variables fueled by `config.yaml`.

### Local Run

Using the `make run` target, the Backend binary can be run locally.

### Personal DEV Environment deployment

The local code can also be deployed directly into a personal DEV environment by running `make deploy`. Understand that this requires such an environment to be created first via `make personal-dev-env` from the root of the repository.

`make deploy` builds a custom developer image from the local code and uploads it to the DEV service ACR (`arohcpsvcdev`) into a developer specific repository. This way developer images will not conflict with other developer images or CI built ones. The actual deployment is delegated to the pipeline/RP.Backend target in the root of the repository, providing a configuration override for `backend.image.repository` and `backend.image.digest` respectively.

## Deployment

The [pipeline.yaml](pipeline.yaml) file in this directory contains the pipeline definition for the Backend. It is integrated into the [topology.yaml](../topology.yaml) file and runs as part of the service cluster deployment.

## Image registry Storage Account policy preflight

`StorageAccountNetworkAccessValidation` implements the detect-and-report portion
of ARO-28903. Before dispatching cluster creation to Cluster Service, it checks
policy assignments at the managed resource group's scope, including inherited
assignments and initiative members. Confirmed conflicts with the image registry's
required public network access produce an actionable validation error. Private
endpoints and private registry connectivity (ARO-28814), and general provisioning
prevalidation (ARO-28901), are not implemented here.

The check is creation-only and is skipped when `properties.clusterImageRegistry.state`
is `Disabled`. Its error identifies the assignment and managed resource group,
and suggests a policy exemption/change or recreating the cluster with the image
registry disabled. The Cluster Service creation gate waits for a successful check;
the existing operation validation timeout determines when ARM reports failure.
This does not prevent independent controllers from creating preliminary resources,
including the managed resource group, before validation completes.

Supported checks include the known built-in public-network-access policy and
bounded custom-policy conditions using `allOf`, `anyOf`, `not`, `equals`,
`notEquals`, `in`, `notIn`, and `exists` on resource type, location, and public
network access. Simple parameter references and initiative parameter mappings are
resolved. Deny rules must specifically distinguish Enabled from Disabled access.
Simple unconditional Modify operations that set public network access to Disabled
are also identified; this is not a simulation of API-version-dependent mutation
behavior or of the ordering of multiple modifying policies.

Active exemptions are matched to assignments, scopes, and initiative member
references. Unknown fields/expressions, selectors, effect overrides, and unresolved
management-group exclusions are treated conservatively as inconclusive rather
than grounds to reject creation. Consequently, passing this check means no
supported conflict was confirmed, not that every possible Azure Policy permits
the final resource. Azure remains the authoritative policy evaluator.

Deploy the identity permissions before enabling this preflight: it requires
`Microsoft.Authorization/policyAssignments/read`, `policyDefinitions/read`,
`policySetDefinitions/read`, and `policyExemptions/read` at the applicable scopes.
Subscription roles do not grant permission to read custom definitions or
initiatives located in ancestor management groups; those reads require separate
authorization. The mock identity role includes the subscription permissions.
Exemptions use the `2022-07-01-preview` REST API through the authenticated ARM
pipeline because the current policy SDK has no exemptions client. Read failures
remain Unknown and retry; they are not interpreted as an absence of restrictions.
