# ARO HCP CLI Generation (AAZ)

This document describes how the Azure CLI for ARO HCP is generated, where code is published, and ARO HCP-specific considerations when working with the `aaz-dev` tooling.

For the general Azure CLI development workflow (setup, codespace, workspace editor, code generation, testing, and customization), see:

- [Hands-On Azure CLI Development in Codespace](https://github.com/Azure/azure-cli/blob/dev/doc/hands_on_codespace.md)
- [AAZ Dev Tools documentation](https://azure.github.io/aaz-dev-tools/)
- [AAZ Dev Tools Workspace Editor](https://azure.github.io/aaz-dev-tools/pages/usage/workspace-editor/)
- [AAZ Dev Tools Command Customization](https://azure.github.io/aaz-dev-tools/pages/usage/customization/)

Note that `aaz-dev` has both CLI-based code generation and GUI-based code generation, where you run a local Flask web app that you then open and use to generate the AAZ code. We use the GUI-based approach.

## Overview

The unified ARO CLI - Standard and HCP - is stored in `Azure/azure-cli-extensions`: https://github.com/Azure/azure-cli-extensions/tree/main/src/aro. The initial CLI code is generated from Swagger specs using `aaz-dev`, with customizations added on top of the generated code as needed. The code lives in upstream Azure repositories — not in the ARO-HCP repo. This means customers can install the extension directly from Azure without needing any Red Hat-specific tooling.

With Standard and HCP combined into a single CLI extension, the following directories are relevant to HCP work:

| Directory | Description |
| --- | --- |
| https://github.com/Azure/azure-cli-extensions/tree/main/src/aro/azext_aro/aaz/latest/aro | Where the generated HCP AAZ code lives |
| https://github.com/Azure/azure-cli-extensions/tree/main/src/aro/azext_aro/azext_aro_hcp | Where the HCP customizations live |
| https://github.com/Azure/azure-cli-extensions/tree/main/src/aro/azext_aro/tests/latest/hcp | Where the HCP tests live (unit and integration) |

### Workflow to develop changes to the extension without publishing a new extension to customers

Use this workflow to develop new features and make changes to the CLI without exposing them to customers yet.

1. API spec upstream to `Azure/azure-rest-api-specs`
2. aaz-dev: import Swagger, prune command tree, export command models to `Azure/aaz`
3. Pull generated AAZ code changes from `Azure/aaz` into https://github.com/Azure/azure-cli-extensions/tree/main/src/aro/azext_aro/aaz/latest/aro
4. Develop any necessary customizations, update tests accordingly, and PR to `Azure/azure-cli-extensions`, taking care that you **do not** change the version in [setup.py](https://github.com/Azure/azure-cli-extensions/blob/main/src/aro/setup.py)

### Workflow to push a new extension to customers

Use this workflow to expose a new feature to customers as a preview without updating the stable CLI command module.

The steps to develop and submit your changes are the same as above, except that this time you **should** update [setup.py](https://github.com/Azure/azure-cli-extensions/blob/main/src/aro/setup.py) with a new version number. When your PR merges, the Azure CLI team's automation will detect the new version number, build a new extension wheel, and update [index.json](https://github.com/Azure/azure-cli-extensions/blob/main/src/index.json) automatically. This exposes the new extension to customers through `az extension add --name aro`.

### Workflow to update the stable CLI command module

Use this workflow to include the extension's functionality in the next release of the stable CLI. Customers will then have access to the new functionality without having to run `az extension add`.

Copy the contents of [`Azure/azure-cli-extensions/src/aro`](https://github.com/Azure/azure-cli-extensions/tree/main/src/aro) to the corresponding directory in `Azure/azure-cli`. Adjust or remove metadata files as needed, and then open a PR with the changes.

### Where code lives

| Artifact | Repository | Path |
| --- | --- | --- |
| API specs (Swagger/TypeSpec) | `Azure/ARO-HCP` | `api/redhatopenshift/` |
| Command models | `Azure/aaz` | `Commands/`, `Resources/` |
| Extension code | `Azure/azure-cli-extensions` | `src/aro/` |
| Stable command module code | `Azure/azure-cli` | TBD |

In all cases, the command models live in the `Azure/aaz` repo, and the generated code plus customizations (`custom.py`, `commands.py`) live in the appropriate upstream Azure repo. The ARO-HCP repo only contains the Swagger/TypeSpec API specs and this workflow documentation.

## ARO HCP-specific considerations

### Swagger, not TypeSpec

When adding resources to the `aaz-dev` workspace, use **Swagger** as the source, not TypeSpec. The TypeSpec import path in `aaz-dev` has a bug where generic type names with angle brackets (e.g. `<RECORD>`) are emitted as-is into generated Python, producing invalid identifiers. See [aaz-dev-tools#562](https://github.com/Azure/aaz-dev-tools/issues/562).

### Pruning the command tree

When importing a new swagger resource into the command tree, remove the following commands at both the cluster and nodepool levels:

- `identity assign`
- `identity remove`
- `identity show`

ARO HCP does not support attaching or removing individual managed identities. Identities are managed as a set through the cluster and node pool create and update commands, not through separate identity assign and remove operations.

## Customizations

After code generation, customizations are applied in `custom.py` and `commands.py` in the extension directory. These files are **not overwritten** by regeneration — they survive across regeneration runs.

Customizations use the [AAZ inheritance pattern](https://azure.github.io/aaz-dev-tools/pages/usage/customization/): subclass the generated command in `custom.py`, override callbacks, and register the subclass in `commands.py`.

### Current customizations

- **`request-admin-credential`**: exposes the kubeconfig (hidden by default as a secret), replaces literal `\n` sequences with actual newlines, and adds `--file` to write the kubeconfig directly to a file. It also generates a private key and certificate signing request (CSR) on behalf of the user, submits the CSR as part of the API request, and inserts the private key into the resulting kubeconfig.
- **`cluster create`**: injects `identity.type = "UserAssigned"` into the request body. The generated code sets `userAssignedIdentities` but does not set the required ARM `identity.type` field. This customization also restructures the identity assignment arguments to align more closely with the Standard CLI's user experience.
- **`cluster update`**: restructures the identity assignment arguments in the same way as `cluster create`.
- **`get-versions`**: formats output nicely.

## Submitting PRs

After validating with `azdev linter` and `azdev test`, submit PRs to the upstream repos:

1. **`Azure/aaz`** — the exported command models from the workspace editor
2. **`Azure/azure-cli-extensions`** - the generated CLI code plus any `custom.py` / `commands.py` customizations
3. **`Azure/azure-cli`** - carry over changes made to `Azure/azure-cli-extensions` if/when it's time to GA a new feature and/or API version


## Updating to a new API version

When a new Swagger tag is available (for example, when moving from `2025-12-23-preview` to `2026-06-30-preview`):

1. Import the new Swagger resources in the `aaz-dev` workspace editor.
2. Use **Inherit modifications from exported command models** to carry forward pruning and customizations from the previous version ([documentation](https://azure.github.io/aaz-dev-tools/pages/usage/workspace-editor/#inherit-modifications-from-exported-command-models)).
3. If needed, update command and argument descriptions in the AAZ Dev Tools GUI. When you inherit previous work as described in step 2, the tool does not apply updates that the API spec has made to existing descriptions, so you must update them manually.
4. If needed, use the AAZ Dev Tools GUI to flatten JSON-formatted arguments into separate CLI arguments, making them more user-friendly.
5. Re-export the command models to `aaz`.
6. Verify that the existing `custom.py` customizations still work.
7. Run the linter and tests.
8. Submit the updated PRs.