# Kiota Generated SDKs

This directory contains generation scripts for creating minimal, type-safe API clients from openapi specs using [Kiota](https://github.com/microsoft/kiota).

## Tool Usage

To use this tool, install Kiota CLI manually from: https://learn.microsoft.com/en-us/openapi/kiota/install, then:

```
make generate-kiota
```

> Note: run this from the **repository root** — the target is defined in the root `Makefile` (there is no Makefile inside `tooling/kiota/` itself).

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

- Microsoft Graph SDK: application registration, group management, user information — generated into and checked in at `internal/graph/graphsdk` in this repository

## OpenAPI Spec Maintenance

The `openapi.yaml` file in this directory is a manually curated subset of the official Microsoft Graph v1.0 OpenAPI spec:

https://raw.githubusercontent.com/microsoftgraph/msgraph-metadata/master/openapi/v1.0/openapi.yaml

When adding new API endpoints:

1. Find the path definition in the upstream spec linked above
2. Copy the path, parameters, and any referenced schemas/responses into `openapi.yaml`
3. Reuse existing `$ref` entries (e.g. `#/components/parameters/filter`) where possible
4. Run `make generate-kiota` to regenerate the SDK
