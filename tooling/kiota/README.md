# Kiota Generated SDKs

This directory contains generation scripts for creating minimal, type-safe API clients from openapi specs using [Kiota](https://github.com/microsoft/kiota).

## Tool Usage

To use this tool, install Kiota CLI manually from: https://learn.microsoft.com/en-us/openapi/kiota/install, then:

```
make generate-kiota
```

## What is Kiota?

Kiota is Microsoft's OpenAPI-to-code generator that creates type-safe, maintainable API clients. Instead of using large, monolithic SDKs that include every possible API endpoint, Kiota allows us to generate minimal clients with only the endpoints we actually need, and the only input is an openapi.yaml file.

But why not use the provided SDKs directly?

Traditional API SDKs (like `github.com/microsoftgraph/msgraph-sdk-go`) are massive - they include every possible endpoint, model, and feature. This leads to:
- **Large binary sizes** - Unnecessary bloat
- **Slow build times** - Compiling unused code
- **Dependency bloat** - Many indirect dependencies
- **Maintenance overhead** - Updates can break things we don't use

Some SDKs are actually so large that they interfere with local builds and CI jobs due to massive resource consumption for things like linting/etc.

Kiota's approach generates minimal, focused clients that only include the endpoints we actually need.

## Generated SDKs

- Microsoft Graph SDK: Application registration, group management, user information
