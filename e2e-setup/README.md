# ARO HCP E2E Setup

This set of scripts implements **setup of ARO HCP clusters in all major
configurations** for the needs of [ARO HCP E2E Tests](/test/e2e) as well as
development and demonstration purposes of ARO HCP project.

## Who can use ARO HCP E2E Setup and how?

- ARO HCP subteams to create ARO HCP clusters for development or testing
  purposes
- ARO HCP QE subteam as a test setup for ARO HCP E2E Test suite runs
- ARO HCP QE, Doc and other subteams to define or learn about existing
  supported cluster configurations

## In which environments should ARO HCP E2E Setup work?

We require the setup to work in:

- [ARO HCP Dev enviroment](https://github.com/Azure/ARO-HCP/blob/main/dev-infrastructure/docs/development-setup.md)
  for the needs of ARO HCP teams in Red Hat (no matter if a shared integrated
  DEV environment or personal dev environment is used)
- Internal integration and stage environments of ARO HCP team
- Azure Production (in other words, one should be able to use it with
  ARO HCP production service as any customer)

## Structure and Design Choices

### Setup Steps

The setup itself is implemented as a set of minimal bash scripts with numeric
prefix (eg. `10-infra-setup.sh`):

- plain sequence of azure cli (or aro curl wrapper) commands
- no branching or extra logic implemented in the scripts, it should be viable
  to compare it with example from documentation
- no queries on existing resources if possible, everything should be identified
  via name directly

### Cluster configurations

Cluster configurations are implemented in scripts with `setup.` prefix. These
scripts:

- can define or update env variables (to control cluster configuration)
- executes the setup steps (minimal shell scripts) mentioned above
- produce json file `${CLUSTER_NAME}.e2e-setup.json` which describes the
  configuration and created resources

### Structure of the e2e-setup json file

Constraints of the design of e2e-setup.json file:

- to pass information about the cluster configuration
- don't include information about the environment (location, subscription ...)
  again
- keep the structure simple as possible (unfortunately we can't avoid some
  nesting)
- high level description of the configuration should be available in tags so
  that from there both the e2e test code as well as someone reading the test
  report should be able to quickly understand the type of cluster created
- all details will be provided in the cluster/nodepool json data 
- when cluster or a node pool is not part of the setup, it's section will be
  empty

Example:

```
{
  "e2e_setup": {
    "name": "cluster-beta",  # name of the setup
    "tags": []  # high level description of the configuration
  },
  "customer_env": {  # resources created in customer's rg
    "customer_rg_name": "example-aro-hcp",
    "customer_vnet_name": "aro-hcp-vnet",
    "customer_nsg_name": "aro-hcp-nsg",
    "uamis": contnent of uamis.json,
    "identity_uamis": content of identity_uamis.json
  },
  "cluster": {
    "name": "example-aro",
    "armdata": content of cluster.json
  },
  "nodepools": [
    {
    "name": "pool-one",
    "armdata": content of nodepool.json
    }
  ]
}
```

This file is used by E2E tests to:

- identify the cluster to be used for testing
- understand what configuration was used during the setup

### ARO Helper Scripts

These scripts are exception from rule of using minimal script approach
mentioned above:

- ARO curl wrapper (`arocurl.sh`): The script is used to send REST requests to
  ARO HCP RP API endpoint in a dev environment, implementing HTTP headers
  generation and standardize way how to send queries to ARO HCP RP for
  debugging purposes. It is used in the setup and can be used for manual
  debugging as well.
- Script `aro-curl-wait.sh` waits for a resource in given path to
  reach given state.
- Script `aro-setup-metadata.sh` is used in setup scripts to create 
  e2e-setup json file.

### Why the ARO curl wrapper?

By implementing such wrapper script, we:

- keep client code related to sending requests to ARO HCP RP in a single place
- have a way to send requests to ARO HCP RP in setup step scripts
- can use it during early development phase, when the RP is not yet ready for
  integration enviroment or when the functionality is not present in az aro
  cli

Right now, all setup step scripts are using it, but later we will be switching
to az aro cli when available.

### Conventions

The scripts uses `xtrace` bash option which prints each executed command to the
stderr on lines starting with `+ ` prefix. The setup scripts and
`arocurl.sh` wrapper are written in a way so that one can:

- understand fully what the setup script is doing
- copy paste given line to execute it again

## Usage

### Requirements

- Recent version of GNU Bash, GNU Make and GNU Coreutils (if you use macOS,
  you will need to install more recent versions of these tools)
- [jq](https://jqlang.github.io/jq/)
- [Azure CLI](https://github.com/Azure/azure-cli)
- [curl](https://curl.se)

### Assumptions

You have:

- Access to Azure subscription
- ARO HCP registered in the subscription.
- Access to ARO HCP RP for your environment

For the convenience of users of personal dev environments, there is
`00-registration.sh` script which handles this step and can be run manually
before using the setup.

### Creating a cluster

First of all, you need to make sure that all required environment variables have
a proper value. One can do that by copying the example env file and filling up
the values listed there:

```
$ cp env.example env.foo
$ vim env.foo
$ source env.foo
```

Note that there is another enviroment file called `env.defaults`, where a few
more enviroment variables are defined based on values from `env.foo`. File
`env.defaults` is sourced by the setup scripts, so you don't need to touch it
or use directly.

Then one can execute a shell script for particular known configuration. The
script will report what it's doing:

```
$ ./setup.cluster-demo.sh
+ az group create --name mbukatov-aro-hcp-default --location westus3
{
  "id": "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/mbukatov-aro-hcp-default",
  "location": "westus3",
  "managedBy": null,
  "name": "mbukatov-aro-hcp-default",
  "properties": {
    "provisioningState": "Succeeded"
  },
  "tags": {
    "createdAt": "2025-02-16T14:16:23.3945914Z"
  },
  "type": "Microsoft.Resources/resourceGroups"
}
+ az network vnet create --resource-group mbukatov-aro-hcp-default --name aro-hcp-vnet --address-prefixes 10.0.0.0/16
 | Running ..
```

If one fails to define all the variables, the script will immediately fail
when an unknown variable is used:

```
$ ./setup.cluster-demo.sh
./10-infra-setup.sh: line 16: CUSTOMER_RG_NAME: unbound variable
```

### Using aro curl wrapper script to send ARO RP requests

This wrapper implements all details for sending a request to ARO HCP RP so that
it doesn't need to be implemented on multiple places.

```
$ ./arocurl.sh -h
Usage: arocurl.sh [options] METHOD LOCATION

Options:
    -H      define additional HTTP header
    -c      this is create request, add X-Ms-Arm-Resource-System-Data header
    -t      test mode, just show headers for given request
    -v      verbose mode (for manual debugging and CI runs)
    -h      this message
```

Sending a GET request to the ARO HCP RP to list existing clusters:

```
$ ./arocurl.sh GET "/subscriptions/${CUSTOMER_SUBSCRIPTION}/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters?api-version=2024-06-10-preview"
```

If you are interested what HTTP headers the wrapper uses, you can check it via
a test mode, which just shows the headers and exits:

```
$ ./arocurl.sh -t GET "/subscriptions/${CUSTOMER_SUBSCRIPTION}/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters?api-version=2024-06-10-preview"
X-Ms-Correlation-Request-Id: f3d1c329-9aea-4267-8514-0b606b7ee70d
X-Ms-Client-Request-Id: 788481fd-bde3-4b5e-ba42-9a1af2bbc4da
X-Ms-Return-Client-Request-Id: true
X-Ms-Identity-Url: https://dummyhost.identity.azure.net
```

One can add a custom header if needed:

```
$ ./arocurl.sh -t -H "X-Demo: true" GET "/subscriptions/${CUSTOMER_SUBSCRIPTION}/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters?api-version=2024-06-10-preview"
X-Demo: true
X-Ms-Correlation-Request-Id: cf5bf8d9-c7f6-4ef2-aeda-79b3493a3dd6
X-Ms-Client-Request-Id: b5f1d576-88bf-4c14-b3a3-ecb46ad913fc
X-Ms-Return-Client-Request-Id: true
X-Ms-Identity-Url: https://dummyhost.identity.azure.net
```

When you rerun the script with `-v` option, you enable `xtrace` bash debugging
and make curl provide HTTP headers of the reply, providing full context of the
request:

```
$ ./arocurl.sh -v GET "/subscriptions/${CUSTOMER_SUBSCRIPTION}/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters?api-version=2024-06-10-preview"
+ print_headers
+ printf '%s\n' 'X-Ms-Correlation-Request-Id: 963d810d-7607-4f46-834a-6baa89f7426c' 'X-Ms-Client-Request-Id: 957d24e4-eb22-401e-9078-9800143271fa' 'X-Ms-Return-Client-Request-Id: true' 'X-Ms-Identity-Url: https://dummyhost.identity.azure.net'
+ curl --include -H @- --request GET 'localhost:8443/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters?api-version=2024-06-10-preview'
HTTP/1.1 200 OK
Content-Type: application/json
X-Ms-Client-Request-Id: 957d24e4-eb22-401e-9078-9800143271fa
X-Ms-Request-Id: 9005de30-4718-4fbc-a1fb-d70bf2b78cdb
Date: Wed, 26 Mar 2025 19:32:50 GMT
Transfer-Encoding: chunked

{
    "value": [
        {
            "id": "/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/mbukatov-aro-hcp-default/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/mbukatov-aro",
            "identity": {
```

Note that one can send the exactly the same requests again by copy pasting the
printf line and piping it to the curl line.
