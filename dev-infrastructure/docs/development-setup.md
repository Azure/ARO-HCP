# Development setup

## Background

The idea of this repo is to provide means to create a development environment that resemble the (future) production setup in a repeatable way. In order to do so, the creation of all infrastructure resources is based on bicep templates and parameter files.

## Prerequisites

* `az`, `jq`, `make`
* `az login` with your Red Hat email

## Procedure

There are a few variants to chose from when creating an AKS cluster:
* Service Cluster: Public AKS cluster with optional params that can be modified to include all Azure resources needed to run a Service cluster
* Management Cluster: Public AKS cluster with optional params that can be modified to include all Azure resources needed to run a Management cluster (coming soon)

1. Decide on the variant and update the corresponding configuration file as desired

  For example, you can toggle `deployFrontendCosmos` in configurations/svc-cluster.bicepparam to control whether or not to deploy a CosmosDB for frontend development.

1. Provision an AKS Cluster for each Variant

   ```bash
   # Service Cluster
   AKSCONFIG=svc-cluster make svc-cluster

   # Management Cluster
   AKSCONFIG=mgmt-cluster make mgmt-cluster
   ```

1. Access private AKS clusters with:

   ```bash
   az aks command invoke --resource-group ${RESOURCE_GROUP} --name aro-hcp-cluster-001 --command "kubectl get ns"
   ```

   Docs: https://learn.microsoft.com/en-us/azure/aks/access-private-cluster?tabs=azure-cli

1. Access public AKS clusters with:

   ```bash
   AKSCONFIG=svc-cluster make aks.kubeconfig
   KUBECONFIG=aks.kubeconfig kubectl get ns
   ```

## Creating your own "First Party Application"
In order for a resource provider to interact with a customers tenant, we create a special type of Application + Service Principal called a First Party Application. This applications' service principal is then granted permissions over certain resources / resource groups within the customers tenant. In the dev tenant we do not need nor can create a First Party Application (they are tied to AME). Instead, we create a Third Party Application, and grant it permissions over our dev subscription so the RP can then interact and create the required resources.

### Step 1 - Log into the dev account
Follow the "Preparation" steps

### Step 2 - Create the Application and its dependencies

Make sure you have `jq` installed on the system running the script. It is used to modify the role definition json file.

```bash
cd dev-infrastructure/scripts
sh dev-application.sh create
```
A unique prefix for all resources created by the script is a 20 character combination of the values $USER and $LOCATION.
To change which region the resources are deployed in, update $LOCATION in the script.

This will create:
1. A resource group
1. A keyvault
1. A default certificate in the keyvault
1. A custom role definition with required access as defined in `dev-infrastructure/scripts/mock-dev-role-definition.json`
1. A service principal/application using the created cert as its authentication, and given access based on the custom role definition

### Step 3 (optional) - log in as the mock application
You may need to manually interact with resources as the service principal, however this shouldn't be required. If you do need to, the 'login' command will download the cert and login with it. Don't forget to logout of the service principal in order to log back in via your personal account.

```bash
cd dev-infrastructure/scripts
sh dev-application.sh login
```

### Step 99 - Delete the application

```bash
cd dev-infrastructure/scripts
sh dev-application.sh delete
```

This will delete:
1. All role assignments using the custom role
1. The service principal
1. The app registration
1. The custom role definition
1. The keyvault, then purge the keyvault
1. The resource group

## Cleanup

> Please note that all resource groups not tagged with `persist=true` will be deleted by our cleanup pipeline after 48 hours

1. Setting the correct `AKSCONFIG`, this will cleanup all resources created in Azure

   ```bash
   AKSCONFIG=svc-cluster make clean
   ```

## Maestro Infrastructure

Maestro infrastructure is provisioned as part of the svc-cluster. To deploy the Maestro infrastructure and deploy the Maestro server onto the service cluster set the `deployMaestroInfra` toggle to `true` and run

```sh
cd dev-infrastructure
AKSCONFIG=svc-cluster make svc-cluster
AKSCONFIG=svc-cluster make aks.kubeconfig
KUBECONFIG=svc-cluster.kubeconfig scripts/maestro-server.sh

kubectl get svc -n maestro
curl http://${LB_IP}:$port
```
