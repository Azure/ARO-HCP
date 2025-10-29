# Resource Cleaner

A modular script system for cleaning up leftover resources from E2E tests and other operations in the ARO-HCP environment.

## Overview

The resource cleaner orchestrates cleanup of various Azure and Kubernetes resources that may be left behind from testing, including:

- Cluster IDs (extracted from MI Key Vault secrets)
- Maestro bundles (via Maestro API)
- Azure resource groups (managed and customer, with `createdAt` tag filtering)
- Key Vault secrets (MI and CX vaults)
- Key Vault certificates (CX vault)
- Azure Container Registry tokens (format: `hc-ocp-pull-{cluster_id}`)

## Prerequisites

- `az` CLI configured with appropriate Azure credentials for the ARO-HCP Dev subscription
- `jq` installed for JSON parsing
- `curl` for Maestro API calls (if using Maestro bundle cleanup)
- `python3` for Key Vault secret processing (if using standalone Key Vault scripts)
- Execute permissions on all scripts (`chmod +x *.sh`)

## Usage

### Main Script

Run the main orchestrator script:

```bash
./resource-cleaner.sh [RETENTION_HOURS] [--dry-run] [--maestro-url <url>]
```

**Arguments:**
- `RETENTION_HOURS` - Hours to retain resources before cleanup (default: 3)
- `--dry-run` - Preview what would be deleted without actually deleting
- `--maestro-url <url>` - Maestro API URL (default: http://localhost:8002)

**Examples:**
```bash
# Use default 3-hour retention
./resource-cleaner.sh

# Use 6-hour retention
./resource-cleaner.sh 6

# Preview what would be deleted (dry-run mode)
./resource-cleaner.sh --dry-run

# Preview with 6-hour retention
./resource-cleaner.sh 6 --dry-run

# Preview with custom Maestro URL
./resource-cleaner.sh --dry-run --maestro-url http://maestro.example.com:8002
```

### Individual Cleanup Scripts

Each cleanup step can also be run independently. Note that most scripts require specific parameters:

```bash
# Retrieve cluster IDs from MI keyvault
./retrieve-cluster-ids.sh <cutoff_time>

# Delete Maestro bundles (requires Maestro URL and cutoff date)
./cleanup-bundles.sh <maestro_url> <cutoff_date> [dry_run]

# Delete resource groups by prefix
./delete-resource-groups.sh <cutoff_time> <prefix> [dry_run]

# Delete keyvault secrets (uses named parameters in order: vault-name, cutoff-time, dry-run)
./delete-keyvault-secrets.sh --vault-name <vault_name> --cutoff-time <epoch_timestamp> [--dry-run]

# Delete keyvault certificates (uses named parameters in order: vault-name, cutoff-time, dry-run)
./delete-keyvault-certificates.sh --vault-name <vault_name> --cutoff-time <epoch_timestamp> [--dry-run]

# Delete ACR tokens (reads from cluster IDs file)
./cleanup-acr-tokens.sh [dry_run]
```

## Script Structure

### Core Scripts

- **`resource-cleaner.sh`** - Main orchestrator that coordinates all cleanup steps
- **`common.sh`** - Shared configuration and utility functions

### Cleanup Scripts

1. **`retrieve-cluster-ids.sh`** - Retrieves cluster IDs from MI Key Vault secrets (pattern: `uamsi-{cluster_id}-*`)
   - Parameters: `<cutoff_time>`
   
2. **`cleanup-bundles.sh`** - Deletes old Maestro bundles via Maestro API
   - Parameters: `<maestro_url> <cutoff_date> [dry_run]`
   
3. **`delete-resource-groups.sh`** - Deletes resource groups with specified prefix using `createdAt` tag
   - Parameters: `<cutoff_time> <prefix> [dry_run]`
   - Called twice: once for managed RGs, once for customer RGs
   
4. **`delete-keyvault-secrets.sh`** - Deletes secrets from specified Key Vault
   - Parameters: `[--vault-name <name>] [--cutoff-time <epoch>] [--dry-run]`
   - Parameter order: vault-name, cutoff-time, dry-run
   - Uses Python for date parsing and processing
   - Called twice: once for MI vault, once for CX vault
   
5. **`delete-keyvault-certificates.sh`** - Deletes certificates from specified Key Vault
   - Parameters: `[--vault-name <name>] [--cutoff-time <epoch>] [--dry-run]`
   - Parameter order: vault-name, cutoff-time, dry-run
   - Uses Python for date parsing and processing
   - Called once for CX vault
   
6. **`cleanup-acr-tokens.sh`** - Deletes ACR tokens (format: `hc-ocp-pull-{cluster_id}`)
   - Parameters: `[dry_run]`
   - Reads cluster IDs from temporary file created by `retrieve-cluster-ids.sh`

## Configuration

Edit `common.sh` to modify default values:

```bash
RETENTION_HOURS=3
MAESTRO_URL="http://localhost:8002"
MI_KEYVAULT="ah-cspr-mi-usw3-1"
CX_KEYVAULT="ah-cspr-cx-usw3-1"
ACR_NAME="arohcpocpdev"
ARO_HCP_DEV_SUBSCRIPTION="1d3378d3-5a3f-4712-85a1-2485495dfc4b"
MANAGED_RG_PREFIX="e2e_tests_mrg_name"
CUSTOMER_RG_PREFIX="pr-check-e2e-tests-resource-group-"
```

## Execution Flow

The main script executes cleanup in the following order:

### Setup Phase
1. Parse command-line arguments (`RETENTION_HOURS`, `--dry-run`, `--maestro-url`)
2. Calculate `CUTOFF_TIME` (current time - retention hours)
3. **Set Azure subscription context** to ARO-HCP Dev subscription (exits if fails)

### Cleanup Steps
**Step 1:** Retrieve cluster IDs from MI Key Vault secrets (pattern: `uamsi-{cluster_id}-*`)
   - Filters by secrets older than `CUTOFF_TIME`
   - Saves unique cluster IDs to `.cluster_ids.tmp`

**Step 2:** Delete Maestro bundles
   - Uses Maestro API with converted cutoff date
   - Waits for bundle verification in non-dry-run mode

**Step 3:** Delete managed resource groups
   - Prefix: `e2e_tests_mrg_name`
   - Only deletes if `createdAt` tag exists and is older than `CUTOFF_TIME`

**Step 4:** Delete customer resource groups
   - Prefix: `pr-check-e2e-tests-resource-group-`
   - Only deletes if `createdAt` tag exists and is older than `CUTOFF_TIME`

**Step 5:** Clean up MI Key Vault secrets
   - Vault: `ah-cspr-mi-usw3-1`
   - Deletes secrets older than `CUTOFF_TIME`

**Step 6:** Clean up CX Key Vault secrets
   - Vault: `ah-cspr-cx-usw3-1`
   - Deletes secrets older than `CUTOFF_TIME`

**Step 7:** Clean up CX Key Vault certificates
   - Vault: `ah-cspr-cx-usw3-1`
   - Deletes certificates older than `CUTOFF_TIME`

**Step 8:** Delete ACR tokens
   - For each cluster ID from Step 1
   - Token format: `hc-ocp-pull-{cluster_id}`
   - Registry: `arohcpocpdev`

### Completion
- Clean up temporary files (`.cluster_ids.tmp`)
- Report summary of successful and failed steps

## Exit Codes

- `0` - All cleanup steps completed successfully
- `1` - One or more cleanup steps failed

## Logging

The scripts use colored output for different log levels:
- 🟢 **INFO** (Green) - Normal operation messages
- 🟡 **WARN** (Yellow) - Warnings about non-critical issues
- 🔴 **ERROR** (Red) - Critical errors
- 🔍 **DRY RUN** - Indicates preview mode when `--dry-run` is enabled

## Safety Features

- **Dry-run mode**: Preview all deletions before actually executing them
- Each script validates inputs before execution
- Graceful error handling with detailed logging
- Resources are only deleted if they exceed the retention period
- Failed cleanup steps are tracked and reported in the summary
- Temporary files are cleaned up automatically
- Azure subscription validation before any cleanup begins

## Examples

### Preview cleanup (dry-run mode)
```bash
# Preview what would be deleted with default 3-hour retention
./resource-cleaner.sh --dry-run

# Preview what would be deleted with 24-hour retention
./resource-cleaner.sh 24 --dry-run
```

### Clean up all resources older than 3 hours
```bash
./resource-cleaner.sh
```

### Clean up resources older than 24 hours
```bash
./resource-cleaner.sh 24

# With custom Maestro URL
./resource-cleaner.sh 24 --maestro-url http://maestro.example.com:8002
```

### Run only Key Vault secrets cleanup
```bash
# Calculate cutoff time (3 hours ago)
CUTOFF_TIME=$(date -d '3 hours ago' +%s)

# Preview what would be deleted from MI vault
./delete-keyvault-secrets.sh --vault-name ah-cspr-mi-usw3-1 --cutoff-time ${CUTOFF_TIME} --dry-run

# Actually delete secrets older than cutoff from MI vault
./delete-keyvault-secrets.sh --vault-name ah-cspr-mi-usw3-1 --cutoff-time ${CUTOFF_TIME}

# Delete secrets from CX vault
./delete-keyvault-secrets.sh --vault-name ah-cspr-cx-usw3-1 --cutoff-time ${CUTOFF_TIME}
```

### Run only Key Vault certificates cleanup
```bash
# Calculate cutoff time (3 hours ago)
CUTOFF_TIME=$(date -d '3 hours ago' +%s)

# Preview what would be deleted from CX vault
./delete-keyvault-certificates.sh --vault-name ah-cspr-cx-usw3-1 --cutoff-time ${CUTOFF_TIME} --dry-run

# Actually delete certificates older than cutoff from CX vault
./delete-keyvault-certificates.sh --vault-name ah-cspr-cx-usw3-1 --cutoff-time ${CUTOFF_TIME}
```

### Run only bundle cleanup (requires Maestro URL)
```bash
MAESTRO_URL="http://maestro.example.com:8002"
CUTOFF_DATE="2025-10-21T12:00:00.000000Z"

# Preview what would be deleted
./cleanup-bundles.sh "${MAESTRO_URL}" "${CUTOFF_DATE}" true

# Actually delete bundles
./cleanup-bundles.sh "${MAESTRO_URL}" "${CUTOFF_DATE}" false
```

### Run only resource group cleanup for a specific prefix
```bash
CUTOFF_TIME=$(($(date +%s) - 10800))  # 3 hours ago

# Preview what would be deleted
./delete-resource-groups.sh ${CUTOFF_TIME} "e2e_tests_mrg_name" true

# Actually delete resource groups
./delete-resource-groups.sh ${CUTOFF_TIME} "e2e_tests_mrg_name" false
```

## Troubleshooting

### Permission Issues

Ensure you have appropriate permissions:
```bash
# Verify Azure access and subscription
az account show

# Verify you can access the keyvaults
az keyvault secret list --vault-name ah-cspr-mi-usw3-1 --query "[0].name"

# Verify you can list resource groups
az group list --query "[0].name"

# Verify you can access the ACR
az acr token list --registry arohcpocpdev --query "[0].name"
```

### Script Not Executable

Make scripts executable:
```bash
chmod +x *.sh
```

### Missing Dependencies

Install required tools:
```bash
# Install jq
sudo apt-get install jq  # Debian/Ubuntu
sudo yum install jq      # RHEL/CentOS

# Install Azure CLI
curl -sL https://aka.ms/InstallAzureCLIDeb | sudo bash
```

## Adding New Cleanup Steps

To add a new cleanup step:

1. Create a new script following the pattern:
```bash
#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

# Accept parameters
CUTOFF_TIME=${1:-$(date +%s)}
DRY_RUN=${2:-false}

###############################################################################
# Your cleanup logic here
###############################################################################
log_info "Starting cleanup..."

if [[ "${DRY_RUN}" == "true" ]]; then
    log_info "[DRY RUN] Would delete resource..."
else
    # Actual deletion logic
    log_info "Deleting resource..."
fi
```

2. Add the script call to the `resource-cleaner.sh` execution flow:
```bash
if "${SCRIPT_DIR}/your-new-script.sh" "${CUTOFF_TIME}" "${DRY_RUN}"; then
    log_info "✓ Your cleanup completed"
else
    log_error "✗ Your cleanup failed"
    FAILED_SCRIPTS+=("your-new-script.sh")
fi
```

3. Make the script executable: `chmod +x your-new-script.sh`

4. Update this README to document the new cleanup step

