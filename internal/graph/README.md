# Graph API Package

This directory contains both a utility package (`./util`) that wraps a minimalistic, kiota-generated Microsoft Graph SDK (`./graphsdk`) to provide a simplified, higher-level API for common Graph operations.

Likely - you want to prefer `./util` over direct calls to `./graphsdk`, but I'll leave that as an exercise to the engineer to determine what makes sense, there's likely cases for both. However, we should always prefer writing new functions in `./util` for complex flows that can be re-used.

## Package Structure

- **`./graphsdk`** - generated output of `make generate-kiota`, do not edit
- **`./util`** - helper functions, the prefered API for Microsoft Graph calls (with distinct files per openapi spec path in the official SDK)
    - **`client.go`** - Core client with authentication and common utilities
    - **`applications.go`** - Application management operations (create, read, update, delete, password management)
    - **`groups.go`** - Group management operations (security group creation)
    - **`organization.go`** - Organization operations (tenant information)
    - **`me.go`** - User operations (current user information)

## Usage

Again, always prefer `./util` to `./graphsdk`:

```go
import graph "github.com/Azure/ARO-HCP/internal/graph/util"

// Create client with automatic authentication
graphClient, err := graph.NewClient(ctx)
if err != nil {
    return err
}

// Create an application
app, err := graphClient.CreateApplication(ctx, "my-app", "AzureADMyOrg", []string{})
if err != nil {
    return err
}

// Add a password to the application
passwordCred, err := graphClient.AddPassword(ctx, app.ID, "my-secret", startTime, endTime)
if err != nil {
    return err
}

// Create a security group
group, err := graphClient.CreateSecurityGroup(ctx, "my-group", "description")
if err != nil {
    return err
}
```

## Adding New Operations

To add new operations:

1. **Identify the Upstream Graph API path** (e.g., `/applications`, `/groups`, `/users`)
    - If the path doesn't exist in `./graphsdk`, see `/tooling/kiota` for instructions to add it and re-generate the SDK.
1. **Add a useful wrapper in Util**
    - **Add to the appropriate file** (named the same as the graph path)
    - **Use the generated SDK** (`../graphsdk`) for the underlying implementation
    - **Provide a simplified interface** that hides complexity
    - **Include proper error handling and godocs**

## Integration Tests

⚠️ **WARNING: Integration tests create and modify Azure Entra resources. They require high-privilege access to your Azure tenant.**

### Running Tests

```bash
# Set up required environment variables
export INTEGRATION_TEST_CONSENT=true
export TEST_AZURE_TENANT_ID="your-test-tenant-id"
export AZURE_TENANT_ID="your-test-tenant-id"
export TEST_RESOURCE_PREFIX="my-test-prefix"
export AZURE_CLIENT_ID="your-client-id"
export AZURE_CLIENT_SECRET="your-client-secret"

# Run tests
cd internal/graph
make test-integration-dry-run        # Safe validation (no resources modified)
make test-integration                # Full integration tests
```

### Required Environment Variables

| Variable | Required | Purpose |
|----------|----------|---------|
| `INTEGRATION_TEST_CONSENT` | Yes | Explicit consent to run tests |
| `TEST_AZURE_TENANT_ID` | Yes | Test tenant ID for safety |
| `TEST_RESOURCE_PREFIX` | Yes | Prefix for test resources |
| `AZURE_TENANT_ID` | Yes | Must match TEST_AZURE_TENANT_ID |
| `AZURE_CLIENT_ID` | Yes* | Service principal client ID |
| `AZURE_CLIENT_SECRET` | Yes* | Service principal secret |
| `ALLOW_AZ_CLI_FALLBACK` | No | Allow Azure CLI authentication |
| `INTEGRATION_TEST_DRY_RUN` | No | Enable dry-run mode |

*Required unless using Azure CLI authentication
