# API Documentation

This README file provides documentation for the HCP RP API outline, the technologies used to build it, and the steps required to get started with the API.

## Overview

The API is designed using [typespec](https://typespec.io/) that is used to generate the swagger definition.
It also utilizes the [Microsoft typespec libraries](https://azure.github.io/typespec-azure/).

The goal is to create the HCP RP API definition in a Microsoft compliant way.


## Setup

When used from within this project with [VSCode remote extensions](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.vscode-remote-extensionpack),
there is no need to setup anything. The whole environment is already bootstrapped in a container.

The container includes Go 1.22 to allow development of the RP as well as
nodejs, which is required for typespec. The container also has typespec with all required libraries installed. The definition can be found in `.devcontainer/postCreate.sh`, where the libraries are pinned to the latest working versions. Please note, when upgrading version of one, the other might need to be upgraded as well because they tend to break.

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

Going with the guide, the typespec service is stored in the `redhatopenshift/HcpCluster` folder. The `redhatopenshift` is the folder
that holds the current Openshift cluster API and both definitions will need to coexist in the same folder. According to the guide,
the generation is placed in created `resource-manager` folder. Finally to allow the proper swagger inspection, the `common-types` are copied from the `azure-rest-api-specs/specification` repository, without these the swagger preview would not work properly.


## How to use typespec

The typespec configuration is stored in the `tspconfig.yaml` file. The swagger API definition needs to be generated.
To do so, open terminal, switch to api directory and call the following command in a API directory with the `main.tsp`:

```bash
cd 
tsp compile ./api/redhatopenshift/HcpCluster/
```

Or you can use the submitted build task, that does exactly the same. The default shortcut is `Ctrl+Shift+B` or `Cmd+Shift+B`.

## Swagger example generation

The devcontainer comes with bundled [Azure/oav](https://github.com/Azure/oav) which lets you both
validate the swagger and generate the example requests and responses.

To generate the example requests and responses, you can use the following command:

```bash
export API_VERSION=2024-06-10-preview
cd api/redhatopenshift/HcpCluster/examples/$API_VERSION
oav generate-examples ../../../resource-manager/Microsoft.RedHatOpenshift/preview/$API_VERSION/openapi.json
```

## Generating the api client

The API client can be generated using the [autorest](https://github.com/Azure/autorest).
the devcontainer comes with the autorest installed. The usage is straightforward:

```bash
autorest api/autorest-config.yaml
```

The generated clients are stored in `api/generated`.

**IMPORTANT**: When the new examples are generated, all files are changed. Please make sure to review the changes before comitting them
and commit only the changed parts. Otherwise it will result is a lot of unnecessary changes in the PR.