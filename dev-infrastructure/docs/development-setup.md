# Development setup

[[_TOC_]]

## Background

The idea of this repo is to provide means to create a development environment that resemble the (future) production setup in a repeatable way. In order to do so, the creation of all infrastructure resources is based on bicep templates and parameter files.

## Prerequisites

* `az` version >= 2.60, `jq`, `make`, `kubelogin` (from <https://azure.github.io/kubelogin/install.html>), `kubectl` version >= 1.30, `helm`
* `az login` with your Red Hat email
* Register the needed [AFEC](https://aka.ms/afec) feature flags using `cd dev-infrastructure && make feature-registration
* __NOTE:__ This will take awhile, you will have to wait until they're in a registered state.
* Your Red Hat account has been added to the ARO HCP Engineering App Developer group in Azure portal. This will give your account access to resources on Azure for development purposes. Please reach out to your manager or team lead to add you to this group.

## Infrastructure

This section describes how to create the infrastructure required to run ARO HCP.

The infrastructure roughly consists of two AKS clusters:

* Service Cluster: the cluster hosting supporting services for a an ARO HCP region, e.g. the Frontend, Cluster Service, Maestro, etc.

* Management Cluster: the cluster hosting the actual hosted controlplanes and supporting services to provision and manage them

In addition to the clusters, the services require supporting infrastructure as well, consisting of managed identities (and their permissions), Key Vaults, Databases, Networking, DNS, Storage, ...

All this infrastructure is managed by the bicep templates found in the `dev-infrastructure` folder. Despite the name of this folder, these templates are also being used in higher environments (MSFT INT, MSFT PROD) for infrastructure management.

### Shared infrastructure

Every developer creates their own set of service/management clusters, including the supporting infrastructure. This allows for independent development. Certain parts of the infrastructure are shared between developers though for various reasons (cost, ease of management, time):

* Service Key Vault `aro-hcp-dev-svc-kv`: this KV holds various shared secrets that are the same for all developer setups (e.g. 1P app certificates, ARM helper certificates, Quay.io pullsecrets). Some of these need to be recycled occasionally so sharing them allows for a central cycle process. Access to this KV is read-only, therefore sharing is not going to result in conflicts between individual developers. See [SD-DDR-0043](https://docs.google.com/document/d/1YKnMFPFvdIuGpGC1il78O9d3WwTyiVgw7jzCpDTUlII/edit#heading=h.bupciudrwmna) for more details about this KV.

* SVC ACR: this ACR holds mirrored service image to be used by developers. Having these mirrored only once saves time and money. The mirror process for this ACR is driven by the integrated DEV environment. Developers access this ACR read-only, therefore sharing it is not going to result in conflicts.

* OCP ACR: this ACR holds mirrored OCP release payloads. The mirror process for this ACR is driven by the integrated DEV environment. Developers access this ACR read-only, therefore sharing it is not going to result in conflicts.

* Image sync: since we share ACRs, we can also share the image sync deployment

#### Shared SVC KV Secrets

* `acm-d-componentsync-password` and `acm-d-componentsync-username`
  what: credentials for the `quay.io/acm-d` organization
  purpose: used for ACR caching to make ACM prerelease images available for ACR HCP

* `quay-componentsync-password` and `quay-componentsync-password`
  what: credentials for the `quay.io/app-sre` organization
  purpose: used for ACR caching to make CS sandbox images available to the CS PR check environment

* `quay-password` and `quay-username`
  what: credentials for the `quay.io/openshift-release-dev` organization
  purpose: we only sync stable releases with `oc-mirror` but a ACR caching rule makes
    other releases like nightly available for testing purposes

* `component-sync-pull-secret`
  what: base64 encoded pull secret for container registries
  purpose: used by image-sync to mirror component images

* `bearer-secret`
  what: base64 encoded access token for the `quay.io/app-sre` organization
  purposes: used by image-sync to mirror component images

* `aro-hcp-dev-sp`
  what: Azure SP credentials to be used for HCPs
  purpose: until managed identities are available for HCPs, this is the auth creds
    for controlplane operators to interact with Azure. This SP has contributer
    permissions in the subscription

* `aro-hcp-dev-sp-cs`
  what: the same Azure SP credentials as `aro-hcp-dev-sp` but formatted for CS
  purpose: until the 1P mock certificate is going to be used by CS to interact
    with Azure, it will use these static creds instead

* `pull-secret`
  what: pull secret for quay and redhat registries of user `aro-hcp-service-lifecycle-team+quay@redhat.com`
  purpose: used by `oc-mirror` to mirror OCP release payloads into the ACR

* `aro-hcp-dev-pull-secret` - can be removed????
  what: pull secret for quay.io and registry.redhat.io and the `arohcpdev` ACR
  purpose: this was used during P1 while we still installed clusters from quay.io payloads
    later it was used to for HCPs to get access to the ACR while CS was not
    yet creating dedicated pull secrets for them
  note: since HCPs don't pull from quay or RH registries anymore and CS now creates
    dedicated pull secrets for the ACR, this should be safe to delete

* `component-pull-secret` - can be removed????
  what: holds the same a pull secret for quay.io (same as `component-sync-pull-secret`) but
        with an incomplete one for arohcpdev as well

* `quay-pull-token` - can be removed????
  what: a quay token
  purpose: unknown

* `testing` - can be removed????
  what: foo-bar
  purpose: unkown

### Customizing infra deployment

The basic configuration for infrastructure deployment can be found in the `config/config.yaml` file. It holds configuration key/value pairs that can be used in bicep parameter template files (`*.tmpl.bicepparam`) and Makefile config template file (`config.tmpl.mk`).

The configuration file offers multiple levels of overrides depending on cloud, deployment environments and regions.

* `cloud` allows to distinguish between the Azure public cloud and Fairfax.
* `environment` describes a deployment environment archetype, e.g. production deployment, integrated DEV deployment, CS PR check deployment or personal DEV deployment

The following describes the sections where configuration data and overwrites can be defined.

```yaml
defaults: (1)
  subnetPrefix: "10.128.8.0/21"
  podSubnetPrefix: "10.128.64.0/18"
  clusterServicePostgresPrivate: true
  maxHCPPerMC: 100
clouds:
  public: (2)
    defaults: (3)
      baseDnsZoneName: "arohcp.azure.com"
    environments:
      personal-dev: (4)
        defaults:
          baseDnsZoneName: "hcp.osadev.cloud" (5)
      production:
        defaults:
        regions:
          westus3: (6)
            defaults:
              maxHCPPerMC: 100
```

* (1) `.defaults` provides the most general configurations that should serve most environments
* (2) `.clouds.${cloud}` inherits from `.defaults`
* (3) ... and allow overrides and introduction of new configuration
* (4) deployment environments inherit configuration from their cloud and the global defaults
* (5) ... and allow overrides and introduction of new configuration
* (6) regional overrides customize a deployment environment to accommodate for regional specifics

The base configuration for all Red Hat Azure Subscription based deployments can be found under `clouds.public.defaults`. This configures the shared infrastructure and component versions to be used in general.

The deployment environment used for personal developer infrastructure is found under `.clouds.public.environments.personal-dev`. It inherits the global configuration from `defaults` and the cloud specific ones under `clouds.public.defaults`.

You can inspect the final results of configuration value overrides by running

  ```bash
  ./templatize.sh <DEPLOY_ENV> | jq
  e.g.
  ./templatize.sh personal-dev | jq
  ```

If you introduce changes to `config.yaml`, run the following command and review the change to the json files in the `config` directory. Make sure all changes are expected and only then commit them to be part of of your next PR (otherwise the PR check will fail):

   ```bash
   cd config
   make detect-change
   ```

### Before creating infrastructure

> A word of caution upfront: dev infrastructure is usually automatically deleted after 48h. If you want to keep your infrastructure indefinitely, run all the following commands with an env variable `PERSIST=true`.

All the following make commands will assume that you want to deploy a `personal-dev` environment in the public cloud section. If you want to deploy/update/interact with other deployment environments, define an environment variable `DEPLOY_ENV=the-env-name`.

* if you want to interact with the integrated DEV environment use `DEPLOY_ENV=dev make ...`
* if you want to interact with the CS PR check environment use `DEPLOY_ENV=cs-pr make ...`
* if you want to interact with the personal DEV environment of a colleague use `USER=other-user DEPLOY_ENV=personal-dev make ...`

### Create infrastructure the easy way

To create the service cluster, management cluster and supporting infrastructure run the following command from the root of this repository.

  ```bash
  SKIP_CONFIRM=1 make infra.all
  ```

Running this the first time takes around 60 minutes. Afterwards you can access your clusters with

  ```bash
  export KUBECONFIG=$(make infra.svc.aks.kubeconfigfile)
  export KUBECONFIG=$(make infra.mgmt.aks.kubeconfigfile)
  ```

If you only need a management cluster or service cluster for development work, consider using one of the following commands. They take less time and the resulting infrastructure costs less money

  ```bash
  SKIP_CONFIRM=1 make infra.svc
  or
  SKIP_CONFIRM=1 make infra.mgmt
  ```

### Updating infrastructure

To update already existing infrastructure you can run `make infra.all` again. You can also use more fine grained make tasks that finish quicker, e.g.

  ```bash
  make infra.svc
  make infra.mgmt
  ```

### Access AKS clusters

Running `make infra.all` will provide you with cluster admin on your clusters and kubeconfig files being created under `~/.kube`. The kubeconfigs are named after the resource group name that holds the cluster. The term `svc` and `mgmt` used in these file names indicate what cluster they are for.

Please not that these kubeconfig files require an active Azure CLI session (`az login`) to work properly.

If you loose these files, you can recreate them by running

  ```bash
  make --directory=dev-infrastructure svc.aks.admin-access svc.aks.kubeconfig
  or
  make --directory=dev-infrastructure mgmt.aks.admin-access mgmt.aks.kubeconfig
  ```

> Freshly granted cluster admin permissions might not be effective immediately. If you get permission denied errors on your `kubectl` commands, consider waiting a couple of minutes for the permissions to be propagated

### Cleanup

To clean up the entire infrastructure of a personal dev environment, run the following command

  ```bash
  make infra.clean
  ```

There are more fine grained cleanup tasks available as well

  ```bash
  make infra.svc.clean
  make infra.mgmt.clean
  make infra.region.clean
  make infra.imagesync.clean
  ```

> Please note that all resource groups not tagged with `persist=true` will be deleted by our cleanup pipeline after 48 hours. In order to prevent that from happening, run the infrastructure deployment make targets with a `PERSIST=true` env variable defined

## Deploying Services quick and easy

To followup sections describe how to deploy the components individually. But if you are looking for a quick and easy way to install or update ALL components on both clusters with one command, then run this:

  ```bash
  make deploy.svc.all
  make deploy.mgmt.all
  ```

Or even simpler with

  ```bash
  make deploy.all
  ```

## Deploy Services to the service cluster

> The service cluster has no ingress. To interact with the services you deploy use `kubectl port-forward`

### Maestro Server

  ```bash
  make maestro.server.deploy
  ```

To validate, have a look at the `maestro` namespace on the service cluster. Some pod restarts are expected in the first 1 minute until the containerized DB is ready.

To access the HTTP and GRPC endpoints of maestro, run

  ```bash
  kubectl port-forward svc/maestro 8001:8000 -n maestro
  kubectl port-forward svc/maestro-grpc 8090 -n maestro
  ```

### Cluster Service

> This might not work with `oc` 4.17.0, please use oc 4.16.x until this is fixed in 4.17

   ```bash
   make cs.deploy
   ```

To validate, have a look at the `cluster-service` namespace or the service cluster.

### Resource Provider / Frontend

The ARO-HCP resource provider consists of independent frontend and backend components.

  ```bash
  make rp.frontend.deploy
  make rp.backend.deploy
  ```

To validate, have a look at the `aro-hcp` namespace on the service cluster.

## Deploy Services to the management cluster

### ACM

  ```bash
  make acm.deploy
  ```

### Hypershift Operator and External DNS

  ```bash
  make hypershift.deploy
  ```

### Maestro Agent

First install the agent

  ```bash
  make maestro.agent.deploy
  ```

Then register it with the Maestro Server

  ```bash
  make maestro.registration.deploy
  ```

## Creating an ARO HCP Cluster via Cluster Service

### Creating a cluster
1) Login to your CS deployment
  - Access your CS deployment locally
    ```bash
    KUBECONFIG=$(make infra.svc.aks.kubeconfigfile) kubectl port-forward svc/clusters-service 8000:8000 -n cluster-service
    ```
  - Login to your CS deployment
    ```bash
    ocm login --url=http://localhost:8000 --use-auth-code
    ```

2) Create pre-requisite resources for cluster creation

    Replace `resource-group`, `vnet-name`, `nsg-name` and `subnet-name` with any valid names.
  
  - Create a resource group for your ARO HCP cluster. This is used, alongside the resource name and subscription ID, to represent
    your ARO HCP cluster resource in Azure. 
    ```
    az group create --name <resource-group> --location "westus3"
    ```
  - Create a Virtual Network. 
    > NOTE: This may be created in the same resource group above, or a different one.
    ```
    az network vnet create -n <vnet-name> -g <resource-group> --subnet-name <subnet-name>
    ```
  - Create a Network security group
    > NOTE: This may be created in the same resource group above, or a different one.
    ```
    az network nsg create -n <nsg-name> -g <resource-group>
    ```
  - Associate the created VNet with the subnet of the created NSG
    ```
    az network vnet subnet update -g <resource-group> -n <subnet-name> --vnet-name <vnet-name> --network-security-group <nsg-name>
    ```

3) Create the cluster
    > **NOTE** See the [Cluster Service API](https://api.openshift.com/#/default/post_api_clusters_mgmt_v1_clusters) documentation
    > for further information on the properties within the payload below

    ```bash
    NAME="<INSERT-NAME-HERE>"
    SUBSCRIPTION_NAME="ARO Hosted Control Planes (EA Subscription 1)"
    RESOURCENAME="<INSERT-NAME>"
    SUBSCRIPTION=$(echo $(az account subscription list | jq '.[] | select(.displayName == $SUBSCRIPTION_NAME)' | jq -r '.subscriptionId'))
    RESOURCEGROUPNAME="<INSERT-NAME>"
    TENANTID=$(echo $(cat azure-creds.json | jq -r '.tenantId'))
    MANAGEDRGNAME="<INSERT-NAME>"
    SUBNETRESOURCEID="<INSERT-NAME>"
    NSG="<INSERT-NAME>"
    cat <<EOF > cluster-test.json
    {
      "name": "$NAME",
      "product": {
        "id": "aro"
      },
      "ccs": {
        "enabled": true
      },
      "region": {
        "id": "westus3"
      },
      "hypershift": {
        "enabled": true
      },
      "multi_az": true,
      "azure": {
        "resource_name": "$RESOURCENAME",
        "subscription_id": "$SUBSCRIPTION",
        "resource_group_name": "$RESOURCEGROUPNAME",
        "tenant_id": "$TENANTID",
        "managed_resource_group_name": "$MANAGEDRGNAME",
        "subnet_resource_id": "$SUBNETRESOURCEID",
        "network_security_group_resource_id":"$NSG"
      },
      "version": {
        "id": "openshift-v4.17.0"
      }
    }
    EOF

    cat cluster-test.json | ocm post /api/clusters_mgmt/v1/clusters
    ```

    You should now have a cluster in OCM. You can verify using `ocm list clusters` or `ocm get cluster CLUSTERID`

### Creating node pools
> NOTE: See the [Cluster Service API](https://api.openshift.com/#/default/post_api_clusters_mgmt_v1_clusters__cluster_id__node_pools) documentation for further information on the properties within the payload below

```bash
CLUSTER_ID="<INSERT-CLUSTER-ID-HERE>"
UID="<INSERT-ID-HERE>"
NAME="<INSERT-NAME-HERE>"
REPLICAS="<INSERT-NUM-OF-REPLICAS-HERE>"
cat <<EOF > nodepool-test.json
{
    "id": "$UID",
    "replicas": $REPLICAS,
    "auto_repair": false,
    "azure_node_pool": {
        "resource_name": "$NAME",
        "vm_size": "Standard_D8s_v3",
        "os_disk_size_gibibytes": 30,
        "os_disk_storage_account_type": "StandardSSD_LRS",
        "ephemeral_os_disk_enabled": false
    }
}
EOF

cat nodepool-test.json | ocm post /api/clusters_mgmt/v1/clusters/$CLUSTER_ID/node_pools
```

You should now have a nodepool for your cluster in Cluster Service. You can verify using:
```
ocm get /api/clusters_mgmt/v1/clusters/$CLUSTER_ID/node_pools/$UID
```

## Creating an ARO HCP Cluster via Frontend
To create a cluster in CS using a locally running Frontend, see the frontend [README](../../frontend/README.md)

## Appendix

### Access Maestro Postgres from outside of the AKS cluster

To connect to the database as current user run

  ```sh
  eval $(AKSCONFIG=svc-cluster make maestro-current-user-pg-connect)
  psql -d maestro
  ```

The output of the make target is in ENV var format for the `psql` tool, so this works to get a connection into the DB.

To connect to the database with the managed identity of Maestro, make sure to have a KUBECONFIG for the cluster that runs Maestro Server and run

  ```sh
  eval $(AKSCONFIG=svc-cluster make maestro-miwi-pg-connect)
  psql -d maestro
  ```

Once logged in, verify the connection with `\conninfo`

> The password is a temporary access token that is valid only for 1

### Access Cluster Service Postgres from outside of the AKS cluster

To create a Postgres DB on Azure enabled for Entra authentication, a svc cluster needs to be created with the `deployCsInfra` parameter set to `true` in the `svc-cluster.bicepparam` file.

### Access the database from outside of the AKS cluster

To connect to the database as current user run

  ```sh
  eval $(make cs-current-user-pg-connect)
  psql -d clusters-service
  ```

The output of the make target is in ENV var format for the `psql` tool, so this works to get a connection into the DB.

To connect to the database with the managed identity of CS, make sure to have a KUBECONFIG for the cluster that runs CS and run

  ```sh
  eval $(make cs-miwi-pg-connect)
  psql -d clusters-service
  ```

Once logged in, verify the connection with `\conninfo`

> The password is a temporary access token that is valid only for 1h

### Azure Credentials and Pull Secret for HCP creation

To test HCP creation, an Azure credentials file with clientId/clientSecret and a pull secret are required.
The `aro-hcp-dev-svc-kv` KV hosts shared secrets for the creds file and the pull secrets, that can be used by the team for testing.

Users require membership in the `aro-hcp-engineering` group to read secrets.  This group has been assigned the
`Key Vault Secrets User` role on the `aro-hcp-dev-svc-kv` KV.

* Pull secrets that can pull from RH registries and the DEV ACR

  ```sh
  az keyvault secret show --vault-name "aro-hcp-dev-svc-kv" --name "aro-hcp-dev-pull-secret" | jq .value -r > pull-secret.json
  ````

* Azure SP credentials in the format HyperShift Operator requires it (line format)

  ```sh
  az keyvault secret show --vault-name "aro-hcp-dev-svc-kv" --name "aro-hcp-dev-sp" | jq .value -r > azure-creds
  ```

* Azure SP credentials in the format CS requires it (json format)

  ```sh
  az keyvault secret show --vault-name "aro-hcp-dev-svc-kv" --name "aro-hcp-dev-sp-cs" | jq .value -r > azure-creds.json
  ```

### Access integrated DEV environment

The integrated DEV environment is hosted in `westus3` and consists of

* the RG `hcp-underlay-westus3-dev` containing shared regional resources (regional DNS zone, Maestro Eventgrid, Maestro KV)
* the RG `hcp-underlay-westus3-svc-dev` the AKS service cluster and the resources required by the service components running on the SC (Postgres for Maestro Server, Postgres for Cluster Service, CosmosDB for RP, Service Key Vault, ...)
* the RG `hcp-underlay-westus3-mgmt-dev-1` containing the AKS mgmt cluster
* the shared ACRs `arohcpsvcdev` and `arohcpocpdev` running in the `global` RG

To access the SC run

```sh
DEPLOY_ENV=dev make svc.aks.admin-access svc.aks.kubeconfig
export KUBECONFIG=$(DEPLOY_ENV=dev make svc.aks.kubeconfigfile)
kubectl get ns
```

To access the MC run

```sh
DEPLOY_ENV=dev make mgmt.aks.admin-access mgmt.aks.kubeconfig
export KUBECONFIG=$(DEPLOY_ENV=dev make mgmt.aks.kubeconfigfile)
kubectl get ns
```

> It might take a couple of minutes for the permissions created by `make xxx.aks.admin-access` to take effect.
