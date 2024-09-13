# Development setup

[[_TOC_]]

## Background

The idea of this repo is to provide means to create a development environment that resemble the (future) production setup in a repeatable way. In order to do so, the creation of all infrastructure resources is based on bicep templates and parameter files.

## Prerequisites

* `az` version >= 2.60, `jq`, `make`, `kubelogin` (from <https://azure.github.io/kubelogin/install.html>), `kubectl` version >= 1.30, `helm`
* `az login` with your Red Hat email
* Register the needed [AFEC](https://aka.ms/afec) feature flags using `cd dev-infrastructure && make feature-registration
    * __NOTE:__ This will take awhile, you will have to wait until they're in a registered state.

## Cluster creation procedure

There are a few variants to chose from when creating an AKS cluster:

* Service Cluster: Public AKS cluster with optional params that can be modified to include all Azure resources needed to run a Service cluster
* Management Cluster: Public AKS cluster with optional params that can be modified to include all Azure resources needed to run a Management cluster

When creating a cluster, also supporting infrastructure is created, e.g. managed identities, permissions, databases, keyvaults, ...

### Create a Service Cluster

The service cluster base configuration to use for development is `configurations/svc-cluster.bicepparam`. Depending on the personal requirements this file offers some features toggles for the main features of the service cluster and the regional resources.

* `deployFrontendCosmos` - set to `true` if you want a CosmosDB created for the RP

  This also includes managed identity and access permissions

* `deployCsInfra` - set to `true` if you want CS infra to be provisioned, e.g. if you want to develop on RP and run it towards an on-cluster CS

  This includes a Postgres DB and access permissions to the DB and the service KeyVault, as well as the Maestro Server
  and supporting infrastructure (EventGrid Namespaces instance, Postgres DB and necessary access permissions).

* `persist` - if set to `true` the resourcegroup holding the cluster and the regional resources will not be deleted after a couple of days

Change those flags accordingly and then run the following command. Depending on the selected features, this may take a while:

  ```bash
  AKSCONFIG=svc-cluster make cluster
  ```

Enable metrics for the svc-cluster
   ```bash
  AKSCONFIG=svc-cluster make enable-aks-metrics
   ```

### Create a Management Cluster

The service cluster base configuration to use for development is `configurations/mgmt-cluster.bicepparam`. This parameter file offers feature toggles as well.

* `deployMaestroConsumer` - if set to `true` deploys the required infrastructure to run a Maestro Consumer (TODO find a better name for this flag because it does not deploy the consumer itself).

* `persist` - if set to `true` the resourcegroup holding the cluster will not be deleted after a couple of days

> A Management Cluster depends on certain resources found in the resource group of the Service Cluster. Therefore, a standalone Management Cluster can't be created right now and requires a Service Cluster

  ```bash
  AKSCONFIG=mgmt-cluster make cluster
  ```

Enable metrics for the mgmt-cluster
  ```bash
  AKSCONFIG=mgmt-cluster make enable-aks-metrics
  ```

### Access AKS clusters

   ```bash
   AKSCONFIG=svc-cluster make aks.admin-access  # one time
   AKSCONFIG=svc-cluster make aks.kubeconfig
   AKSCONFIG=svc-cluster export KUBECONFIG=${HOME}/.kube/${AKSCONFIG}.kubeconfig
   kubectl get ns
   ```

   (Replace svc with mgmt for management clusters)

### Access cluster via the Azure portal or via `az aks command invoke`

  ```bash
  AKSCONFIG=svc-cluster make aks.admin-access  # one time
  az aks command invoke ...
  ```

### Cleanup

> Please note that all resource groups not tagged with `persist=true` will be deleted by our cleanup pipeline after 48 hours

Setting the correct `AKSCONFIG`, this will cleanup all resources created in Azure

   ```bash
   AKSCONFIG=svc-cluster make clean
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

## Deploy Services to the service cluster

> Make sure your `KUBECONFIG` points to the service cluster!!!

> The service cluster has no ingress. To interact with the services you deploy use `kubectl port-forward`

### Maestro Server

  ```bash
  cd maestro
  AKSCONFIG=svc-cluster make deploy-server
  ```

To validate, have a look at the `maestro` namespace on the service cluster. Some pod restarts are expected in the first 1 minute until the containerized DB is ready.

To access the HTTP and GRPC endpoints of maestro, run

  ```bash
  kubectl port-forward svc/maestro 8001:8000 -n maestro
  kubectl port-forward svc/maestro-grpc 8090 -n maestro
  ```

### Cluster Service

   ```bash
   cd cluster-service/
   make deploy
   ```

To validate, have a look at the `cluster-service` namespace.

### Frontend

  ```bash
  cd frontend/
  make deploy
  ```

To validate, have a look at the `aro-hcp` namespace on the service cluster.

## Deploy Services to the management cluster

> Make sure your `KUBECONFIG` points to the management cluster!!!

### ACM

  ```bash
  cd acm
  make deploy
  ```

### Hypershift Operator and External DNS

  ```bash
  cd hypershiftoperator/
  make deploy
  ```

## Maestro Agent

First install the agent

  ```bash
  cd maestro
  AKSCONFIG=mgmt-cluster make deploy-agent
  ```

Then register it with the Maestro Server

Make sure your `KUBECONFIG` points to the service cluster, then run

  ```bash
  cd maestro
  AKSCONFIG=svc-cluster make register-agent
  ```

## CS Local Development Setup

Should your development needs require a running instance of CS to test with, here is how to spin up a locally running Clusters Service with containerized database suitable enough for testing.

To complete the below steps you will need:

1) `podman`, `ocm` cli (latest), and [`yq`](https://github.com/mikefarah/yq) cli (version 4+)
2) An up-to-date [Clusters Service repo](https://gitlab.cee.redhat.com/service/uhc-clusters-service) cloned down (can also use a fork if you have one)

> If you don't have or want to install `yq`, any steps below using `yq` can be done manually

### Configure and run CS

Option 1: Configure and initialize Cluster Service using the script:
Run ./dev-infrastructure/local_CS.sh from the root of ARO-HCP repo where "uhc-clusters-service" and "ARO-HCP" repos should be at the same level:

* uhc-clusters-service/
* ARO-HCP/
* etc

Option 2: You can follow the below manual steps from the root of the CS repo on our system:

1) Follow [Azure Credentials and Pull Secret for HCP creation](#azure-credentials-and-pull-secret-for-hcp-creation) to fetch `azure-creds.json`.

2) Setup required config files

```bash
# Setup the development.yml
cp ./configs/development.yml .

# Update any required empty strings to 'none'
yq -i '(.aws-access-key-id, .aws-secret-access-key, .route53-access-key-id, .route53-secret-access-key, .oidc-access-key-id, .oidc-secret-access-key, .network-verifier-access-key-id, .network-verifier-secret-access-key, .client-id, .client-secret) = "none"' development.yml

# Generate a provision_shards.config for port-forwarded maestro ...
make -C $the_aro_hcp_dir/cluster-service provision-shard > provision_shards.config

# the resulting configuration requires two portforwardings into the service cluster
kubectl port-forward svc/maestro 8001:8000 -n maestro
kubectl port-forward svc/maestro-grpc 8090 -n maestro

# Alternatively, update provision shards config with new shard manually
cat <<EOF > ./provision_shards.config
provision_shards:
- id: 1
  maestro_config: |
    {
      "rest_api_config": {
        "url": "http://localhost:8001"
      },
      "grpc_api_config": {
        "url": "localhost:8090"
      },
      "consumer_name": "<<maestro_consumer_name>>"
    }
  status: active
  azure_base_domain: "<azure_resource_id_of_your_azure_dns_domain>"
  management_cluster_id: local-cluster
  region: westus3
  cloud_provider: azure
  topology: dedicated
EOF

# Enable the westus3 region in cloud region config

cat <<EOF>> ./configs/cloud-resources/cloud-regions.yaml
  - id: westus3
    cloud_provider_id: azure
    display_name: West US 3
    supports_multi_az: true
EOF

cat <<EOF>> ./configs/cloud-resources/cloud-regions-constraints.yaml
  - id: westus3
    enabled: true
    govcloud: false
    ccs_only: true
EOF

# you can verify the region change with the below
yq '.cloud_regions[] | select(.id == "westus3")' configs/cloud-resource-constraints/cloud-region-constraints.yaml

# Update region_constraints.config with new cloud provider
cat <<EOF > ./region_constraints.config
cloud_providers:
- name: azure
  regions:
    - name: westus3
      version_constraints:
        min_version: 4.11.0
      product_constraints:
        - product: hcp
          version_constraints:
            min_version: 4.12.23
EOF

cat <<EOF > ./configs/cloud-resources/instance-types.yaml
instance_types:
  - id: Standard_D4as_v4
    name: Standard_D4as_v4 - General purpose
    cloud_provider_id: azure
    cpu_cores: 4
    memory: 17179869184
    category: general_purpose
    size: d4as_v4
    generic_name: standard-d4as_v4
EOF

cat <<EOF > ./configs/cloud-resource-constraints/instance-type-constraints.yaml
instance_types:
  - id: Standard_D4as_v4
    ccs_only: true
    enabled: true
EOF
```

3) Follow CS dev setup process:

```bash
# Build CS
make cmds

# Setup local DB
make db/setup

# Initialize the DB
./clusters-service init --config-file ./development.yml
```

4) Start CS:

```bash
./clusters-service serve --config-file development.yml --runtime-mode aro-hcp --azure-auth-config-path azure-creds.json
```

You now have a running, functioning local CS deployment

### Interact with CS

1) Login to your local CS deployment

```bash
ocm login --url=http://localhost:8000 --use-auth-code
```

2) Create a test cluster - note that `version.id` must match the version inserted into the database earlier.

```bash
NAME="<INSERT-NAME-HERE>"
RESOURCENAME="<INSERT-NAME>"
SUBSCRIPTION="<INSERT-NAME>"
RESOURCEGROUPNAME="<INSERT-NAME>"
TENANTID="<INSERT-NAME>"
MANAGEDRGNAME="<INSERT-NAME>"
SUBNETRESOURCEID="<INSERT-NAME>"
$NSG="<INSERT-NAME>"
cat <<EOF > cluster-test.json
{
  "name": "$NAME-aro-hcp",
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
  "properties": {
    "provision_shard_id": "1"
  },
  "version": {
    "id": "openshift-v4.16.0"
  }
}
EOF

cat cluster-test.json | ocm post /api/clusters_mgmt/v1/clusters
```

You should now have a cluster in OCM. You can verify using `ocm list clusters` or `ocm get cluster CLUSTERID`

To create a cluster in CS using a locally running Frontend, see the frontend [README](../../frontend/README.md)

## CS Dev Cleanup

To tear down your CS setup:

1) Kill the running clusters-service process
2) Clean up the database `make db/teardown`

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
The `service-kv-aro-hcp-dev` KV hosts shared secrets for the creds file and the pull secrets, that can be used by the team for testing.

Users require membership in the `aro-hcp-engineering` group to read secrets.  This group has been assigned the
`Key Vault Secrets User` role on the `service-kv-aro-hcp-dev` KV.

* Pull secrets that can pull from RH registries and the DEV ACR

  ```sh
  az keyvault secret show --vault-name "service-kv-aro-hcp-dev" --name "aro-hcp-dev-pull-secret" | jq .value -r > pull-secret.json
  ````

* Azure SP credentials in the format Hypershift Operator requires it (line format)

  ```sh
  az keyvault secret show --vault-name "service-kv-aro-hcp-dev" --name "aro-hcp-dev-sp" | jq .value -r > azure-creds
  ```

* Azure SP credentials in the format CS requires it (json format)

  ```sh
  az keyvault secret show --vault-name "service-kv-aro-hcp-dev" --name "aro-hcp-dev-sp-cs" | jq .value -r > azure-creds.json
  ```

In case the `service-kv-aro-hcp-dev` KV gets recreated as part of a DEV environment recreation, the lost secrets can be replayed from the `aro-hcp-dev-global-kv` KV by ensuring you have `Secret Officer` permissions in the target KV and running

```sh
dev-infrastructure/scripts/import-kv.sh aro-hcp-dev-global-kv service-kv-aro-hcp-dev
```

### Access integrated DEV environment

The integrated DEV environment is hosted in `westus3` and consists of

* the RG `aro-hcp-dev-westus3` containing shared regional resources (regional DNS zone, Maestro Eventgrid, Maestro KV)
* the RG `aro-hcp-dev-westus3-sc` the AKS service cluster and the resources required by the service components running on the SC (Postgres for Maestro Server, Postgres for Cluster Service, CosmosDB for RP, Service Key Vault, ...)
* the RG `aro-hcp-dev-westus3-mc-1` containing the AKS mgmt cluster
* the ACR `devarohcp` running in the `global` RG

To access the SC run

```sh
AKSCONFIG=svc-cluster RESOURCEGROUP=aro-hcp-dev-westus3-sc make aks.admin-access # run one
AKSCONFIG=svc-cluster RESOURCEGROUP=aro-hcp-dev-westus3-sc make aks.kubeconfig
export KUBECONFIG=${HOME}/.kube/svc-cluster.kubeconfig
kubectl get ns
```

To access the MC run

```sh
AKSCONFIG=mgmt-cluster RESOURCEGROUP=aro-hcp-dev-westus3-mc-1 make aks.admin-access # run one
AKSCONFIG=mgmt-cluster RESOURCEGROUP=aro-hcp-dev-westus3-mc-1 make aks.kubeconfig
export KUBECONFIG=${HOME}/.kube/mgmt-cluster.kubeconfig
kubectl get ns
```

> It might take a couple of minutes for the permissions created by `make aks.admin-access` to take effect.
