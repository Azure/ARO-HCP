# ARO HCP E2E Setup

This set of scripts implements **setup of ARO HCP clusters in major
configurations** for the needs of [ARO HCP E2E Tests](/test/e2e), OpenShift
Cluster Validation Tests, as well as development and demonstration purposes of
ARO HCP project.

## Who can use ARO HCP E2E Setup and how?

- ARO HCP subteams to create ARO HCP clusters in well known configurations
  for development or testing purposes
- ARO HCP QE subteam as a test setup for *ARO HCP E2E*
  or *OpenShift Cluster Validation* test runs
- ARO HCP subteams to refine and learn about existing
  supported cluster configurations

## In which environments should ARO HCP E2E Setup work?

We plan for the setup to work in [all environments](/docs/environments.md):

- [ARO HCP Dev enviroment](https://github.com/Azure/ARO-HCP/blob/main/dev-infrastructure/docs/development-setup.md)
  for the needs of ARO HCP teams in Red Hat (no matter if a shared integrated
  DEV environment or personal dev environment is used)
- Internal integration and stage environments of ARO HCP team
- Azure Production (in other words, one should be able to use it with
  ARO HCP production service as any customer)

That said right now, Dev env. is not supported. This is a temporary limitation.

## Structure and Design Choices

### Bicep

[Bicep](https://learn.microsoft.com/en-us/azure/azure-resource-manager/bicep/overview?tabs=bicep)
is used a primary way to describe ARO HCP cluster configurations for E2E Setup.

A cluster configuration (to deploy via E2E setup) is defined in a single
bicep file in `test/e2e-setup/bicep` directory. For example:

* [`demo.bicep`](https://github.com/Azure/ARO-HCP/blob/main/test/e2e-setup/bicep/demo.bicep) is a simple configuration based on the original demo,
* [`infra-only.bicep`](https://github.com/Azure/ARO-HCP/blob/main/test/e2e-setup/bicep/infra-only.bicep) is a setup which defines just customer deployed infra
  (like network, managed identities ...) without creating ARO HCP cluster.
  Details about MI are provided via bicep output values, so that one can use it
  to create a cluster via golang SDK.

#### Usage

First of all define env. variables, for example:

```
export LOCATION=francecentral
export SUBSCRIPTION="FOO Subscription"
export BICEP_FILE=demo.bicep
export CUSTOMER_RG_NAME=${USER}-rg
export CLUSTER_NAME=${USER}-cluster
```

And then run `setup.sh` script. It will deploy given bicep file using the
parameters passed via the enviroment variables.

### Other setup methods

Setup implemented via alternative methods (such as
[CAPZ](https://capz.sigs.k8s.io/) or az cli) instead of bicep planned to be
possible. We plan to include small number selected configurations as needed.
