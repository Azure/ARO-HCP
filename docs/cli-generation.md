# ARO HCP CLI Generation (AAZ)

This document describes how the Azure CLI for ARO HCP is generated, where code is published, and ARO HCP-specific considerations when working with the `aaz-dev` tooling.

For the general Azure CLI development workflow (setup, codespace, workspace editor, code generation, testing, and customization), see:
- [Hands-On Azure CLI Development in Codespace](https://github.com/Azure/azure-cli/blob/dev/doc/hands_on_codespace.md)
- [AAZ Dev Tools documentation](https://azure.github.io/aaz-dev-tools/)
- [AAZ Dev Tools Workspace Editor](https://azure.github.io/aaz-dev-tools/pages/usage/workspace-editor/)
- [AAZ Dev Tools Command Customization](https://azure.github.io/aaz-dev-tools/pages/usage/customization/)

## Overview

The ARO HCP CLI is **separately maintained** from ARO Classic (`az aro`). All CLI code is generated from swagger specs using `aaz-dev` and lives in upstream Azure repositories — not in the ARO-HCP repo. This means customers can install the extension directly from Azure without needing any Red Hat-specific tooling.

### Workflow

```
┌─────────────────────────────────────┐
│  1. API spec upstreamed to          │
│     Azure/azure-rest-api-specs      │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│  2. aaz-dev: import swagger,        │
│     prune command tree, export      │
│     command models to Azure/aaz     │
└──────────────┬──────────────────────┘
               │
               ▼
       ┌───────┴────────┐
       │  API version?   │
       └───┬─────────┬──┘
           │         │
     Preview         GA
           │         │
           ▼         ▼
┌──────────────┐ ┌──────────────────┐
│ Generate     │ │ Generate         │
│ aro-hcp      │ │ az aro hcp       │
│ extension    │ │ command module   │
│              │ │                  │
│ PR to Azure/ │ │ PR to Azure/     │
│ azure-cli-   │ │ azure-cli        │
│ extensions   │ │                  │
└──────────────┘ └──────────────────┘
```

**Preview API versions** (e.g. `2025-12-23-preview`) produce an `aro-hcp` **extension** in `azure-cli-extensions`. Customers install it with `az extension add`.

**GA API versions** produce an `aro hcp` **command module** in the core `azure-cli` repo. This ships built-in with the Azure CLI — no extension install needed.

### Where code lives

| Artifact | Repository | Path |
|---|---|---|
| API specs (swagger/TypeSpec) | `Azure/ARO-HCP` | `api/redhatopenshift/` |
| Command models | `Azure/aaz` | `Commands/`, `Resources/` |
| Extension code (preview) | `Azure/azure-cli-extensions` | `src/aro-hcp/` |
| Command module code (GA) | `Azure/azure-cli` | TBD |

In all cases, the command models live in the `Azure/aaz` repo, and the generated code plus customizations (`custom.py`, `commands.py`) live in the appropriate upstream Azure repo. The ARO-HCP repo only contains the swagger/TypeSpec API specs and this workflow documentation.

## ARO HCP-specific considerations

### Swagger, not TypeSpec

When adding resources to the `aaz-dev` workspace, use **Swagger** as the source, not TypeSpec. The TypeSpec import path in `aaz-dev` has a bug where generic type names with angle brackets (e.g. `<RECORD>`) are emitted as-is into generated Python, producing invalid identifiers. See [aaz-dev-tools#562](https://github.com/Azure/aaz-dev-tools/issues/562).

### Pruning the command tree

When importing a new swagger resource into the command tree, remove the following commands at both the cluster and nodepool levels:

- `identity assign`
- `identity remove`
- `identity show`

ARO HCP does not support individually attaching or removing managed identities. Identities are managed as a set through the cluster and nodepool create/update commands, not through separate identity assign/remove operations.

## Customizations

After code generation, customizations are applied in `custom.py` and `commands.py` in the extension directory. These files are **not overwritten** by regeneration — they survive across regeneration runs.

Customizations use the [AAZ inheritance pattern](https://azure.github.io/aaz-dev-tools/pages/usage/customization/): subclass the generated command in `custom.py`, override callbacks, and register the subclass in `commands.py`.

### Current customizations

- **`request-admin-credential`**: exposes the kubeconfig (hidden by default as a secret), replaces literal `\n` with real newlines, and adds `--file` to write the kubeconfig directly to a file.
- **`cluster create`**: injects `identity.type = "UserAssigned"` into the request body. The generated code sets `userAssignedIdentities` but doesn't set the required ARM `identity.type` field.

Known issues and planned improvements are tracked in [ARO-28510](https://redhat.atlassian.net/browse/ARO-28510) and [ARO-28511](https://redhat.atlassian.net/browse/ARO-28511).

## Submitting PRs

After validating with `azdev linter` and `azdev test`, submit PRs to the upstream repos:

1. **`Azure/aaz`** — the exported command models from the workspace editor
2. **`Azure/azure-cli-extensions`** (preview) or **`Azure/azure-cli`** (GA) — the generated CLI code plus any `custom.py` / `commands.py` customizations

Both PRs should be submitted together so reviewers can see the full picture.

## Updating to a new API version

When a new swagger tag is available (e.g. moving from `2025-12-23-preview` to `2026-06-30-preview`):

1. Import the new swagger resources in the `aaz-dev` workspace editor
2. Use **Inherit modifications from exported command models** to carry forward pruning and customizations from the previous version ([docs](https://azure.github.io/aaz-dev-tools/pages/usage/workspace-editor/#inherit-modifications-from-exported-command-models))
3. Re-export the command models to `aaz`
4. Regenerate the CLI code
5. Verify existing `custom.py` customizations still work
6. Run linter and tests
7. Submit updated PRs
