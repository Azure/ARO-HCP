# Azure Error Fixtures

Sanitized Azure responses captured on 2026-10-14 using Azure CLI 2.90.0 and
ARM API `2024-03-01`, with resource-group-scoped stacks in `westus3`.
Controller tests consume these files without Azure access.

## Classification Evidence

| Fixture | Scenario and Observed Error |
| --- | --- |
| [01](01-malformed-template.json) | Malformed template: `DeploymentStackDeploymentFailed > InvalidRequestContent` |
| [02](02-template-validation.json) | Undeclared parameter: `DeploymentStackDeploymentFailed > InvalidTemplate` |
| [03](03-missing-content-permission.json) | Missing permission to deploy contents: `DeploymentStackDeploymentFailed > InvalidTemplateDeployment` |
| [04](04-same-intent-retry.json) | Identical request succeeds after granting the missing permission |
| [05](05-resource-validation.json) | Invalid location: `DeploymentStackDeploymentFailed > InvalidResourceLocation` |
| [06](06-output-evaluation.json) | Invalid output: `DeploymentStackDeploymentFailed > DeploymentOutputEvaluationFailed` |
| [07](07-provider-failure.json) | Invalid federated credential: `DeploymentStackDeploymentFailed > InvalidTemplateDeployment > PreflightValidationFailed > InvalidFederatedIdentityCredentialsAudienceLength` |
| [08](08-direct-deployment-permission.json) | Direct deployment permission failure: HTTP 400 with `InvalidTemplateDeployment` |
| [09](09-nested-deployment-failure.json) | Nested deployment failure: `DeploymentStackDeploymentFailed > DeploymentFailed > DeploymentOutputEvaluationFailed` |

`>` denotes a structured `details[]` child. Stack submissions were accepted
asynchronously; subsequent stack GETs returned HTTP 200 even for failed operations.

**`InvalidTemplateDeployment` does not establish invalid intent.** In fixtures
03 and 08, authorization failure appears only in the message, without a structured
`AuthorizationFailed` child. Fixtures 03 and 04 retain matching request bodies and
hashes to demonstrate recovery without changing intent.

Environment identifiers, email addresses, generated deployment suffixes, and signed
URL parameters were replaced. Error structure, messages, HTTP statuses, and request
bodies otherwise remain intact. UUID placeholders do not preserve correlation-ID
identity across requests. These observations are evidence for the covered cases,
not guarantees for every resource provider or scope.
