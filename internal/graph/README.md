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
app, err := graphClient.CreateApplication(ctx, "my-app", []string{})
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