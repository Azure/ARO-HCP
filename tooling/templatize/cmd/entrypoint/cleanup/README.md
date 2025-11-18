# Resource Group Cleanup

This package provides ordered resource deletion for Azure resource groups, implementing the same cleanup logic as the original `delete.sh` script but in pure Go using Azure SDK.

## Features

- **Ordered Deletion**: Deletes resources in dependency order to avoid conflicts
- **Dry-Run Mode**: Preview what would be deleted without making changes
- **Lock Detection**: Automatically skips resources with management locks
- **DNS Delegation Cleanup**: Removes NS delegation records from parent zones (cross-subscription)
- **Key Vault Purging**: Purges soft-deleted Key Vaults after resource group deletion
- **Retry Logic**: Automatically retries failed deletions with configurable retry counts
- **Parallel Execution**: Optional parallel deletion for faster cleanup
- **Detailed Logging**: Structured logging with per-step summaries

## Usage

### Environment Variables

- `CLEANUP_DRY_RUN`: Enable dry-run mode (default: `true`)
  - `true`: Preview deletions without making changes
  - `false`: Actually delete resources
- `CLEANUP_WAIT`: Wait for deletions to complete (default: `true`)
  - `true`: Wait for each deletion to complete before proceeding
  - `false`: Fire and forget (faster but less reliable)

### Examples

#### Dry-run mode (preview only)
```bash
make cleanup-entrypoint/Region CLEANUP_DRY_RUN=true CLEANUP_WAIT=true
```

#### Production deletion
```bash
make cleanup-entrypoint/Region CLEANUP_DRY_RUN=false CLEANUP_WAIT=true
```

#### Fast deletion (fire and forget)
```bash
make cleanup-entrypoint/Region CLEANUP_DRY_RUN=false CLEANUP_WAIT=false
```

## Deletion Order

Resources are deleted in the following order to respect dependencies:

1. **Network Security Perimeters (NSP)**
2. **Private Networking** (in order):
   - Private DNS Zone Groups
   - Private Endpoint Connections
   - Private Endpoints
   - Private DNS Zone Virtual Network Links
   - Private Link Services
   - Private DNS Zones
3. **Public DNS Zones** (with delegation cleanup)
4. **Application Resources** (excluding VNETs, NSGs, DCRs, DCEs, Container Instances)
5. **Monitoring Resources**:
   - Data Collection Rules
   - Data Collection Endpoints
6. **Core Networking**:
   - Virtual Networks
   - Network Security Groups
7. **Resource Group** (entire group)
8. **Key Vaults** (purge soft-deleted vaults)

## API Versions

All API versions are set to the latest stable (non-preview) versions as of 2025-11-18:

### Specific Resource Types
| Resource Type | API Version |
|--------------|-------------|
| Microsoft.Network/virtualNetworks | 2025-05-01 |
| Microsoft.Network/networkSecurityGroups | 2025-05-01 |
| Microsoft.Network/privateEndpoints | 2025-05-01 |
| Microsoft.Network/privateLinkServices | 2025-05-01 |
| Microsoft.Network/privateDnsZones | 2024-06-01 |
| Microsoft.Network/dnszones | 2018-05-01 |
| Microsoft.Network/networkSecurityPerimeters | 2025-05-01 |
| Microsoft.Network/privateEndpointConnections | 2025-05-01 |
| Microsoft.Network/privateEndpoints/privateDnsZoneGroups | 2025-05-01 |
| Microsoft.Network/privateDnsZones/virtualNetworkLinks | 2020-06-01 |
| Microsoft.Insights/dataCollectionRules | 2024-03-11 |
| Microsoft.Insights/dataCollectionEndpoints | 2024-03-11 |
| Microsoft.ContainerService/managedClusters | 2025-10-01 |
| Microsoft.Compute/virtualMachines | 2025-04-01 |
| Microsoft.Storage/storageAccounts | 2025-06-01 |

### Provider Defaults (Fallback)
| Provider | Default API Version |
|----------|---------------------|
| Microsoft.Network | 2025-05-01 |
| Microsoft.Compute | 2025-04-01 |
| Microsoft.Storage | 2025-06-01 |
| Microsoft.Insights | 2024-03-11 |
| Microsoft.ContainerService | 2025-10-01 |
| Microsoft.KeyVault | 2025-05-01 |
| Microsoft.Authorization | 2022-04-01 |

**Universal Fallback**: `2023-04-01` (for unknown resource types)

## Retry Configuration

- **Default retries**: 3 attempts for most resources
- **DNS zones**: 3 attempts (higher due to delegation dependencies)
- **VNETs/NSGs**: 1 attempt (fast fail, usually succeed on first try)
- **Retry delay**: 10 seconds between attempts

## Error Handling

- **Locked resources**: Automatically skipped with warning
- **Failed deletions**: Logged but don't stop cleanup process
- **Missing resources**: Silently skipped (already deleted)
- **API errors**: Detailed error messages with retry logic

## Differences from delete.sh

### Improvements
✅ **Pure Go**: No shell dependencies, type-safe Azure SDK
✅ **Better error handling**: Structured errors with context
✅ **Dry-run support**: Preview changes before deletion
✅ **Lock detection**: Automatically detects and skips locked resources
✅ **Final statistics**: Summary with remaining/locked resource counts

### Maintained Features
✅ All 26 features from original delete.sh
✅ Same deletion order and dependencies
✅ Cross-subscription DNS delegation cleanup
✅ Key Vault soft-delete purging
✅ Container Instance exclusion
✅ Continue on individual failures

### Code Quality
✅ **Go best practices**: Named struct types throughout codebase
✅ **Type safety**: Strongly-typed deletion configuration and statistics
✅ **Clean code**: No anonymous inline structs
✅ **Better testability**: Structs can be easily tested and mocked
✅ **Maintainable**: Clear type definitions make code self-documenting

## Architecture

### Core Structs

```go
// deletionStep defines a resource type to be deleted with retry configuration
type deletionStep struct {
    resourceType string
    description  string
    retries      int
}

// deletionStats tracks the outcome of resource deletion operations
type deletionStats struct {
    deleted int
    skipped int
    failed  int
}

// resourceGroupDeleter handles ordered deletion of resources
type resourceGroupDeleter struct {
    resourceGroupName string
    subscriptionID    string
    credential        azcore.TokenCredential
    logger            logr.Logger
    dryRun            bool
}
```

### Function Overview

```
resourceGroupDeleter
├── execute()                     # Main orchestration
├── deleteResourcesByType()       # Type-specific deletion (uses deletionStats)
├── deleteNonNetworkingResources() # Bulk app resource deletion (uses deletionStats)
├── deletePublicDNSZones()        # DNS with delegation cleanup (uses deletionStats)
├── deleteResourceWithRetries()   # Deletion with retry logic
├── hasLocks()                    # Management lock detection
├── removeNSDelegation()          # Cross-subscription NS cleanup
├── purgeSoftDeletedKeyVaults()   # Key Vault purging (uses deletionStats)
├── listAllResources()            # Dry-run resource listing
├── logFinalSummary()             # Final statistics
└── getAPIVersionForResourceType() # API version resolver (3-tier fallback)
```

### Design Principles

- **Type Safety**: Named structs (`deletionStep`, `deletionStats`) instead of anonymous types
- **Idiomatic Go**: Follows Go best practices with proper struct definitions
- **Reusability**: `deletionStats` tracks outcomes consistently across all deletion functions
- **Testability**: Functions return structs for easier unit testing
- **Maintainability**: Clear separation of concerns with well-defined types

## Dependencies

Required Azure SDK packages:
- `github.com/Azure/azure-sdk-for-go/sdk/azcore`
- `github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns` v1.2.0
- `github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault` v1.5.0
- `github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armlocks` v1.2.0
- `github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources` v1.2.0
- `github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions` v1.3.0

## Logging

Uses structured logging with the following levels:
- **INFO**: Normal operations, progress updates, summaries
- **ERROR**: Failed operations (with stack traces when applicable)

Example log output:
```
INFO    Starting ordered resource deletion
INFO    Step: Deleting resources    type=Microsoft.Network/virtualNetworks description="virtual networks"
INFO    Found resources to delete   count=2 type=Microsoft.Network/virtualNetworks
INFO    Successfully deleted resource   name=vnet-1 type=Microsoft.Network/virtualNetworks
INFO    Deletion summary    type=Microsoft.Network/virtualNetworks deleted=2 skipped=0 failed=0
INFO    ✓ Cleanup completed successfully   resourceGroup=my-rg status="Resource group and all resources deleted"
```

## Testing

The cleanup process can be tested using dry-run mode:

```bash
# Preview what would be deleted
CLEANUP_DRY_RUN=true make cleanup-entrypoint/Region

# Check the logs for:
# - [DRY RUN] Would delete resource messages
# - List of all resources that would be deleted
# - Final summary with resource counts
```

## Troubleshooting

### Resources not deleting
- Check for management locks: `az lock list --resource-group <rg-name>`
- Check for dependencies: Some resources may have hidden dependencies
- Enable wait mode: `CLEANUP_WAIT=true` for better error messages

### API version errors
- Update API versions in `getAPIVersionForResourceType()` if Azure releases breaking changes
- Check supported versions: `az provider show --namespace <provider> --query "resourceTypes[?resourceType=='<type>'].apiVersions"`

### DNS delegation cleanup fails
- Verify cross-subscription permissions
- Check parent zone exists in listed subscriptions
- NS record may already be deleted

### Key Vault purging fails
- Soft-delete purge requires special permissions
- Vaults from other resource groups are skipped
- Already-purged vaults return 404 (handled gracefully)
