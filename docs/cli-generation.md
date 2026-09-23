# ARO HCP CLI Generation (AAZ)

This document captures the current workflow used to generate an Azure CLI extension from ARO HCP swagger.

## Purpose

Generate/update an extension (currently named `arohcp`) from swagger tag `package-2025-12-23-preview` using `azdev` + `aaz-dev`.

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

# ensure the local module is visible to azdev commands
azdev extension add arohcp

aaz-dev command-model generate-from-swagger \
  -a ~/workspace/aaz \
  --sm "$PWD/api/redhatopenshift/" \
  -m arohcp \
  --rp Microsoft.RedHatOpenShift \
  --swagger-tag package-2025-12-23-preview

aaz-dev cli generate-by-swagger-tag \
  -a ~/workspace/aaz \
  -e ~/workspace/azure-cli-extensions/ \
  --name arohcp \
  --sm "$PWD/api/redhatopenshift/" \
  --rp Microsoft.RedHatOpenShift \
  --tag package-2025-12-23-preview \
  --profile latest
```

## Post-generation lint compatibility patch

Current generated AAZ args can fail `azdev` rule `option_length_too_long` for:

- `--hcp-open-shift-cluster-name`
- `--node-drain-timeout-minutes`

Apply short aliases in generated AAZ files before linting:

```bash
cd ~/workspace/azure-cli-extensions

find src/arohcp/azext_arohcp/aaz/latest -type f -name '*.py' -print0 | \
  xargs -0 perl -0777 -pi -e 's/options=\["--hcp-open-shift-cluster-name"\]/options=["-c", "--cluster-name", "--hcp-open-shift-cluster-name"]/g; s/options=\["-n", "--name", "--hcp-open-shift-cluster-name"\]/options=["-n", "--name", "--cluster-name", "--hcp-open-shift-cluster-name"]/g; s/options=\["--node-drain-timeout-minutes"\]/options=["-d", "--drain-timeout", "--node-drain-timeout-minutes"]/g'
```

## Optional command root rewrite (`az arohcp`)

By default, generated commands are rooted at `az red-hat-open-shift`.

If you want `az arohcp`, rewrite command names in generated AAZ files:

```bash
cd ~/workspace/azure-cli-extensions/src/arohcp

find azext_arohcp/aaz/latest/red_hat_open_shift -type f -name '*.py' -print0 | \
  xargs -0 sed -i 's/red-hat-open-shift/arohcp/g'

find azext_arohcp/aaz/latest/red_hat_open_shift -type f -name '*.py' -print0 | \
  xargs -0 sed -i 's/Manage Red Hat Open Shift/Manage Red Hat OpenShift Hosted Control Plane Resources/g'
```

Then verify:

```bash
az arohcp -h
```

Quick check before/after rewrite:

```bash
# before rewrite (default generated root)
az red-hat-open-shift -h

# after rewrite + reinstall/reload extension
az arohcp -h
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

For MVP standalone testing, apply the same rewrite from
`red-hat-open-shift` to `arohcp` described in
Optional command root rewrite (`az arohcp`), but run it against:

- `tooling/arohcp-cli/azext_arohcp/aaz/latest/red_hat_open_shift`

## Validation

From `azure-cli-extensions` repo:

```bash
cd ~/workspace/azure-cli-extensions
azdev linter arohcp
azdev test arohcp --discover
```

Command root expectations:

- Without the optional rewrite, commands are available under `az red-hat-open-shift`.
- After applying the optional rewrite and reinstalling/reloading the extension, commands are available under `az arohcp`.

Notes from current generation run:

- `azdev linter arohcp` passes after generation when the local extension has been added via `azdev extension add arohcp`.
- Generated test scaffold currently contains no test methods (`azext_arohcp/tests/latest/test_arohcp.py` is a TODO template), so `azdev test arohcp --discover` may run with `0 items` until real test cases are added.

Manual smoke check after local extension install:

```bash
az extension add --source ~/workspace/azure-cli-extensions/src/arohcp/dist/*.whl -y
az red-hat-open-shift -h
az arohcp -h
```

Interpretation:

- If rewrite is **not** applied: `az red-hat-open-shift -h` should work, `az arohcp -h` is expected to fail.
- If rewrite **is** applied: `az arohcp -h` should work after reinstalling the extension wheel.

If `az arohcp -h` is missing, apply the optional command root rewrite section above and reinstall the extension wheel.

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

### `unrecognized modules: [ arohcp ]` during `azdev linter` or `azdev test`

Cause: the extension is generated on disk but is not registered in the current `azdev` dev environment.

Fix:

```bash
cd ~/workspace/azure-cli-extensions
azdev extension add arohcp
```

You can verify visibility with:

```bash
azdev extension list | rg -n '"name": "arohcp"'
```

### `extension(s): [ arohcp ] installed from a wheel may need --include-whl-extensions option`

Cause: `azdev linter` detected wheel-installed extension state in the current environment.

Fix:

```bash
cd ~/workspace/azure-cli-extensions
azdev linter arohcp --include-whl-extensions
```

### `... is not a valid git repository`

Ensure target repos are cloned and paths are correct:

```bash
ls -ld ~/workspace/azure-cli-extensions ~/workspace/aaz
```

> Note: use `~/workspace/aaz` (not `~/.workspace/aaz`).

### `Path '/absolute/path/to/ARO-HCP/api/redhatopenshift' does not exist`

Cause: the sample path is a placeholder and not a real directory on your machine.

Fix: run from repo root and use `$PWD` (or your full absolute path):

```bash
source .venv/bin/activate
aaz-dev command-model generate-from-swagger \
  -a ~/workspace/aaz \
  --sm "$PWD/api/redhatopenshift/" \
  -m arohcp \
  --rp Microsoft.RedHatOpenShift \
  --swagger-tag package-2025-12-23-preview
```
