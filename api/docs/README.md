# API Documentation

This README file provides documentation for the HCP RP API outline, the technologies used to build it, and the steps required to get started with the API.

## Overview

The API is designed using [typespec](https://typespec.io/) that is used to generate the swagger definition.
It also utilizes the [Microsoft typespec libraries](https://azure.github.io/typespec-azure/).

The goal is to create the HCP RP API definition in a Microsoft compliant way.


## Setup

When used from within this project with [VSCode remote extensions](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.vscode-remote-extensionpack),
there is no need to setup anything. The whole environment is already bootstrapped in a container.

The container includes Go and Node.js, which is required for typespec. Node is provisioned via the devcontainer feature, and TypeSpec library versions are pinned in `api/package.json` and `api/package-lock.json`. Please note, when upgrading one library, the others might need to be upgraded as well because they tend to break.

If you have a fresh container, you need to run `tsp code install` to enable the VSCode typespec extension. This will give you autocomplete and linting.


## Typespec and azure libraries basics

Typespec is a tool that is used to generate the swagger definition from the typespec service definition. It was introduced by Microsoft to make writing api definitions easier and add type definitions to the API definitions.

You can learn more about typespec in Microsoft documentation.
Whereas the basics of typespec can be found in the [typespec document ation](https://typespec.io/docs/getting-started).
Additional information on the use with Azure libraries in in the [Microsoft typespec libraries](https://azure.github.io/typespec-azure/docs/getstarted/createproject).

Samples of the typespec usage can be found in the [Azure/typespec-azure](https://github.com/Azure/typespec-azure/tree/main/packages/samples/specs/resource-manager).


## Azure repository for Submitting the API definition

There are two repositories where the API spec is going to be published:

- https://github.com/azure/azure-rest-api-specs, where all upstream API definitions are stored
- https://github.com/azure/azure-rest-api-specs-pr, where all preview and testing API definitions are stored (this one requires you to be part of the Azure organization)

For the reference, the API definition of ARO-RP is here https://github.com/Azure/azure-rest-api-specs/tree/main/specification/redhatopenshift


### API folder structure

The tsp generation is setup to generate the right project structure for the azure-rest-api-specs. The
repository structure for typespecs projects is explained here https://github.com/Azure/azure-rest-api-specs/blob/main/documentation/typespec-structure-guidelines.md.

The folder structure matches the upstream `azure-rest-api-specs` repository layout. The typespec service definition is stored in `redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpopenshiftclusters/`. TypeSpec source examples are in `examples/<version>/` (configured via `examples-dir` in `tspconfig.yaml`). The generated OpenAPI specs are placed in `preview/<version>/` subdirectories, and they reference emitted examples in `preview/<version>/examples/`. To allow proper swagger inspection, the `common-types` are copied from the `azure-rest-api-specs/specification` repository, without these the swagger preview would not work properly.


## How to use typespec

The typespec configuration is stored in the `tspconfig.yaml` file. The swagger API definition needs to be generated.
To do so, open terminal, switch to the `api` directory and run:

```bash
tsp compile redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpopenshiftclusters --warn-as-error
```

Or use the npm script:

```bash
npm run compile
```

Or you can use the submitted build task, that does exactly the same. The default shortcut is `Ctrl+Shift+B` or `Cmd+Shift+B`.

## Swagger example generation

The devcontainer comes with bundled [Azure/oav](https://github.com/Azure/oav) which lets you both
validate the swagger and generate the example requests and responses.

To generate the example requests and responses, you can use the following command:

```bash
export API_VERSION=2024-06-10-preview
cd api/redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpopenshiftclusters/preview/$API_VERSION
oav generate-examples openapi.json
```

## Generating the api client

The API client can be generated using the [autorest](https://github.com/Azure/autorest).
the devcontainer comes with the autorest installed. The usage is straightforward:

```bash
autorest api/readme.md --tag=v20251223preview
```

The autorest configuration is in the top-level `readme.md` file, which defines tags for each API version.

**IMPORTANT**: When the new examples are generated, all files are changed. Please make sure to review the changes before committing them
and commit only the changed parts. Otherwise it will result is a lot of unnecessary changes in the PR.