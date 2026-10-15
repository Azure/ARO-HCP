# Real Azure Deployment Stack Errors

Operator-run characterization for PR #7536, captured 2026-10-14 with Azure CLI
2.90.0 and ARM API `2024-03-01`. The RG and successfully created managed identity
were in `westus3`. Azure returned operation-status URLs routed through `westus2`;
the probe followed the returned URLs, not a guessed regional endpoint.

## Classification Evidence

**An `InvalidTemplateDeployment` leaf is not proof of invalid intent.** The
missing-content-permission case returned that leaf with authorization failure
only in its message, without `details` or an `AuthorizationFailed` code. Adding
permission and submitting the identical body to the identical stack succeeded.
An allowlist that treats every `InvalidTemplateDeployment` leaf as invalid
intent misclassifies this observed operational failure. Checking for a nested
`AuthorizationFailed` code does not resolve this case.

All stack creations below returned HTTP **201**, with
`properties.provisioningState = initializing`, an `Azure-AsyncOperation` header,
and `Retry-After: 17`. Stack GET returned HTTP **200**, including on failure.
The error tree is in `body.properties.error`, not a top-level HTTP error envelope.
Operation-status GET also returned HTTP **200**, with `body.status = failed`
and the corresponding tree in `body.error`.

| Fixture | Input / outcome | Terminal stack error tree |
| --- | --- | --- |
| [01](01-malformed-template.json) | Template `resources` is a string; async failed | `DeploymentStackDeploymentFailed > InvalidRequestContent` |
| [02](02-template-validation.json) | Valid JSON/template structure, resource name references undeclared parameter; async failed | `DeploymentStackDeploymentFailed > InvalidTemplate` (also carries `additionalInfo`) |
| [03](03-missing-content-permission.json) | Fresh SP can submit stacks, cannot write managed identities; async failed | `DeploymentStackDeploymentFailed > InvalidTemplateDeployment`; authorization failure in message only |
| [04](04-same-intent-retry.json) | Add RG-scoped Managed Identity Contributor; identical stack/body; PUT **200**, initializing, then GET **200**, succeeded | No error |
| [05](05-resource-validation.json) | Invalid resource location; async failed | `DeploymentStackDeploymentFailed > InvalidResourceLocation` |
| [06](06-output-evaluation.json) | Output references undeclared parameter; async failed | `DeploymentStackDeploymentFailed > DeploymentOutputEvaluationFailed` |
| [07](07-provider-failure.json) | Federated identity credential with empty audience array; provider preflight rejects it | `DeploymentStackDeploymentFailed > InvalidTemplateDeployment > PreflightValidationFailed > InvalidFederatedIdentityCredentialsAudienceLength` |
| [08](08-direct-deployment-permission.json) | Direct deployment validate and PUT, same missing content permission | Synchronous **400**, `body.error.code = InvalidTemplateDeployment`; no `details`; deployment GET **404**, `DeploymentNotFound` |
| [09](09-nested-deployment-failure.json) | Nested deployment with invalid output; async failed | `DeploymentStackDeploymentFailed > DeploymentFailed > DeploymentOutputEvaluationFailed` |

`>` means an actual `details[]` child, not a message substring. **No structured
`AuthorizationFailed` code was observed**; do not invent one in an Azure-derived
fixture. The direct deployment response shows the top-level wrapper swallowing
the authorization diagnostic into its message.

The successful retry and original failed request share identical `requestSHA256`
values. The offline verifier checks body, target URL, and hash equality and
recomputes the hash from the recorded body.
The captured retry succeeded without changing intent. The probe allows repeated
identical submissions while the new permission propagates.

Stack GET and deployment GET can have different diagnostics. In case 09, the
underlying deployment GET has `DeploymentFailed > ResourceDeploymentFailure`,
whereas the stack includes `DeploymentOutputEvaluationFailed`. In case 06, the
underlying deployment GET has an extra output-specific child omitted by the stack.
Failure details can appear before the stack reaches terminal state. These are
observations at RG scope, not guarantees for every resource provider or scope.

## Reproduction

Requires Python 3 and Azure CLI. Explicit subscription, tenant, existing Azure CLI
profile directory, and region arguments are mandatory; there are no developer
defaults. Before creation, the profile's selected account must match the supplied
subscription and tenant, be enabled, and use AzureCloud. The probe does not change
the operator's selected subscription or roles or run configuration-setting commands.

**Manual human review recommended before merge and before execution:** these new
Python scripts include live resource creation, credential handling, role assignment,
and deletion. They are operator tools, not CI hooks. Review the chosen account and
permissions before supplying the opt-in flag. The following arguments are placeholders:

```bash
python3 backend/pkg/controllers/stackdesires/testdata/azure-errors/probe.py \
  --subscription "SUBSCRIPTION_ID" --tenant "TENANT_ID" \
  --azure-config-dir "/path/to/azure-cli-profile" --region "westus3" \
  --create-isolated-resources
```

The probe creates a random UUID-named, ownership-tagged RG, one application/SP,
and a one-hour SP secret. The secret is held in process memory and supplied to
the login subprocess, not printed or written into fixtures. Run only on a trusted
operator host: local privileged processes can inspect subprocess arguments.
The principal's CLI token cache is isolated under a mode-0700 temporary directory
and removed during cleanup. No existing identity receives new permissions.

Only built-in role assignments are created, both scoped to the new RG:
`Azure Deployment Stack Contributor` followed by `Managed Identity Contributor`.
No custom role definition, subscription-wide assignment, deny assignment,
provider registration, or shared resource is modified. The SP cannot assign roles.
The templates contain only managed identities, an invalid federated credential,
and nested deployments in this RG; they do not reference any existing resources.

The script uses `az rest --verbose` to capture actual HTTP status, selected
response headers, and response JSON without relying on CLI-flattened error text.
Progress output prints case/status/state and the private evidence path. Request
headers and raw CLI logs are never exported or written to disk by the probe.
Subscription/tenant/principal UUIDs, caller email addresses, generated deployment
suffixes, and signed operation-URL query parameters are replaced in fixtures.
Body structure, error codes/messages, HTTP statuses, state transitions, and
request bodies are otherwise preserved. UUID replacement is not suitable for
testing correlation-ID equality across different requests.

Export from the private run path printed by the probe (existing fixtures are
refused unless `--replace` is explicitly supplied):

```bash
python3 backend/pkg/controllers/stackdesires/testdata/azure-errors/export.py "/path/to/private-run/responses.sanitized.json"
python3 backend/pkg/controllers/stackdesires/testdata/azure-errors/verify.py
python3 -B backend/pkg/controllers/stackdesires/testdata/azure-errors/test_probe.py
```

The live probe is manual and not part of CI; controller tests can consume the
checked-in sanitized JSON without Azure access. Subprocesses use argument lists,
not generated shell commands. The mocked subprocess regression test checks that
timeout, launch, and decoding exceptions do not expose credentials in tracebacks.

## Cleanup

[cleanup.json](cleanup.json) records HTTP **404** verification for both role
assignments, the service principal, the application, and the RG. RG deletion
removed all stacks, deployment records, and the successfully deployed identity.
Application deletion uses the normal Graph delete semantics (directory soft
deletion), not a tenant-wide or permanent-deletion operation.

Cleanup is in `finally`, including SIGINT/SIGTERM handling. Deletions use only
this process's newly generated IDs and check ownership markers before deleting
the RG/application. No arbitrary RG/identity input or prefix-based deletion is
accepted. As with any live script, SIGKILL, machine loss, ambiguous create/network
failure, expired operator credentials, or Azure deletion failures can prevent
cleanup; failures are reported rather than hidden. Do not run without the ability
to monitor it through cleanup. Never use these instructions to clean up another
run's resources or a shared environment.
