# ARO HCP CLI Generation (AAZ)

This document captures the current workflow used to generate an Azure CLI extension from ARO HCP swagger.

## Purpose

Generate/update an extension (currently named `arohcp`) from swagger tag `package-2024-06-10-preview` using `azdev` + `aaz-dev`.

## Prerequisites

- Python virtual environment activated (recommended: repo-local `.venv`)
- `azdev` installed
- `aaz-dev` installed
- Local clones:
  - `~/workspace/azure-cli-extensions`
  - `~/workspace/aaz`
- Swagger module path available in this repo:
  - `api/redhatopenshift/`

## One-time setup

```bash
cd /path/to/ARO-HCP
python3 -m venv .venv
source .venv/bin/activate
pip install -U pip
pip install azdev aaz-dev
```

Clone required repos:

```bash
mkdir -p ~/workspace
git clone https://github.com/Azure/azure-cli-extensions.git ~/workspace/azure-cli-extensions
git clone https://github.com/Azure/aaz.git ~/workspace/aaz
```

## Generation commands

Run from this repository root:

```bash
source .venv/bin/activate

azdev setup -r ~/workspace/azure-cli-extensions/

aaz-dev command-model generate-from-swagger \
  -a ~/workspace/aaz \
  --sm /absolute/path/to/ARO-HCP/api/redhatopenshift/ \
  -m arohcp \
  --rp Microsoft.RedHatOpenShift \
  --swagger-tag package-2025-12-23-preview

aaz-dev cli generate-by-swagger-tag \
  -a ~/workspace/aaz \
  -e ~/workspace/azure-cli-extensions/ \
  --name arohcp \
  --sm /absolute/path/to/ARO-HCP/api/redhatopenshift/ \
  --rp Microsoft.RedHatOpenShift \
  --tag package-2025-12-23-preview \
  --profile latest
```

## How to find the current API version/tag

Use the HCP swagger readme files in this repo:

- `api/redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpclusters/preview/readme.md`
- `api/readme.md`

Find latest package tag:

```bash
rg -n "package-[0-9]{4}-[0-9]{2}-[0-9]{2}-preview" \
  api/redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpclusters/preview/readme.md
```

Find server model tags:

```bash
rg -n "Tag v20[0-9]{6}preview" api/readme.md
```

As of this update, latest HCP preview package tag is:

- `package-2025-12-23-preview`

## Where to plug in the version/tag

Replace the tag in both generation steps:

```bash
# command model step
--swagger-tag package-2025-12-23-preview

# CLI codegen step
--tag package-2025-12-23-preview
```

If you are generating server models via `api/readme.md`, use the matching `vYYYYMMDDpreview` tag there (for example `v20251223preview`).

## Expected output locations

- Generated extension code:
  - `~/workspace/azure-cli-extensions/src/arohcp`
- MVP vendored copy in this repo (for standalone testing):
  - `tooling/arohcp-cli`
- Generated/updated AAZ command model artifacts:
  - `~/workspace/aaz/Commands/...`
  - `~/workspace/aaz/Resources/...`

To refresh the vendored MVP extension in this repo:

```bash
mkdir -p tooling/arohcp-cli
rsync -a --delete \
  ~/workspace/azure-cli-extensions/src/arohcp/ \
  tooling/arohcp-cli/
```

For MVP standalone testing, rewrite generated command root from
`red-hat-open-shift` to `arohcp`:

```bash
python3 - <<'PY'
from pathlib import Path
root = Path("tooling/arohcp-cli/azext_arohcp/aaz/latest/red_hat_open_shift")
for p in root.rglob("*.py"):
    s = p.read_text()
    s = s.replace("red-hat-open-shift", "arohcp")
    s = s.replace(
        '"""Manage Red Hat Open Shift',
        '"""Manage Red Hat OpenShift Hosted Control Plane Resources'
    )
    p.write_text(s)
print("updated command roots for MVP")
PY
```

## Validation

From `azure-cli-extensions` repo:

```bash
cd ~/workspace/azure-cli-extensions
azdev linter arohcp
azdev test arohcp
```

Manual smoke check after local extension install:

```bash
az extension add --source ~/workspace/azure-cli-extensions/src/arohcp/dist/*.whl -y
az red-hat-open-shift -h
```

Or build/install from the vendored copy:

```bash
cd tooling/arohcp-cli
python -m pip install -U build
python -m build --wheel
az extension add --source dist/*.whl -y
az arohcp -h
```

## Known warnings seen during generation

- Read-only property requirement warnings (for example: `url`, `issuerUrl`) can appear.
- Wait-command support warnings for non-standard operations (for example credential request/revoke paths) can appear.

These were non-fatal in generation and did not stop artifact creation.

## Troubleshooting

### `azdev setup` crash with `get_env_path() ... NoneType`

Cause: `azdev` expects a virtual environment marker.

Fix:

```bash
source .venv/bin/activate
azdev setup -r ~/workspace/azure-cli-extensions/
```

### `aaz-dev: command not found`

Install into the active venv:

```bash
pip install aaz-dev
```

### `... is not a valid git repository`

Ensure target repos are cloned and paths are correct:

```bash
ls -ld ~/workspace/azure-cli-extensions ~/workspace/aaz
```

> Note: use `~/workspace/aaz` (not `~/.workspace/aaz`).
