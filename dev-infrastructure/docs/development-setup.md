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
   AKSCONFIG=svc-cluster make cluster

   # Management Cluster
   AKSCONFIG=mgmt-cluster make cluster
   ```

1. Access private AKS clusters with:

   ```bash
   az aks command invoke --resource-group ${RESOURCE_GROUP} --name CLUSTER_NAME --command "kubectl get ns"
   ```

   Docs: https://learn.microsoft.com/en-us/azure/aks/access-private-cluster?tabs=azure-cli

1. Access public AKS clusters with:

   ```bash
   AKSCONFIG=svc-cluster make aks.admin-access  # one time
   AKSCONFIG=svc-cluster make aks.kubeconfig
   KUBECONFIG=${HOME}/.kube/${AKSCONFIG}.kubeconfig kubectl get ns
   ```

1. Access cluter via the Azure portal or via `az aks command invoke`

  ```bash
  AKSCONFIG=svc-cluster make aks.admin-access  # one time
  az aks command invoke ...
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
AKSCONFIG=svc-cluster make cluster
AKSCONFIG=svc-cluster make aks.kubeconfig
KUBECONFIG=svc-cluster.kubeconfig scripts/maestro-server.sh

KUBECONFIG=svc-cluster.kubeconfig kubectl port-forward svc/maestro 8000 -n maestro
```

At this point `localhost:8000` forwards traffic to the Maestro server running on the SC.

## Maestro consumer

Before setting up a Maestro consumer, make sure that

* the Maestro infrastructure has been deployed
* the Maestro server has been installed on the SC
* the port forwarding is active to reach the Maestro server

> Currently, the AKS cluster name is used as a consumer name for Maestro. That is subject to change.

To setup broker access for a maestro consumer on a mgmt-cluster, set the `deployMaestroConsumer` toggle to `true` and run

```sh
cd dev-infrastructure
AKSCONFIG=mgmt-cluster make cluster
AKSCONFIG=mgmt-cluster make aks.kubeconfig
KUBECONFIG=mgmt-cluster.kubeconfig scripts/maestro-consumer.sh
```

This will also register the Maestro consumer with the Maestro server. You can verify that the consumer is present in Maestros consumer inventory by running

```sh
curl -s http://localhost:8000/api/maestro/v1/consumers | jq .items
[
  {
    "created_at": "2024-05-20T15:09:51.451048Z",
    "href": "/api/maestro/v1/consumers/913c07f8-de91-4d2b-9610-8edb4e4820b2",
    "id": "913c07f8-de91-4d2b-9610-8edb4e4820b2",
    "kind": "Consumer",
    "name": "aro-hcp-mgmt-cluster-gvfxqtnhh7hi6",
    "updated_at": "2024-05-20T15:09:51.451048Z"
  }
]
```

To post a manifest (e.g. a `Namespace`) to the MC via Maestro, run

```sh
cd dev-infrastructure
kubectl create namespace my-test --dry-run=client -o json | scripts/maestro-send.sh
```

Then verify, that the namespaces has been created on the cluster and also check the result via Maestro.

```sh
kubectl get ns
curl -s http://localhost:8000/api/maestro/v1/resources | jq .items
[
  {
    "consumer_name": "aro-hcp-mgmt-cluster-gvfxqtnhh7hi6",
    "created_at": "2024-05-20T15:20:06.155763Z",
    "href": "/api/maestro/v1/resources/d8b6c827-ac32-4736-a913-a45ad2a86171",
    "id": "d8b6c827-ac32-4736-a913-a45ad2a86171",
    "kind": "Resource",
    "manifest": {
      "apiVersion": "v1",
      "kind": "Namespace",
      "metadata": {
        "name": "my-test"
      }
    },
    "name": "d8b6c827-ac32-4736-a913-a45ad2a86171",
    "status": {
      "ContentStatus": {
        "phase": "Active"
      }
      ...
    }
    "updated_at": "2024-05-20T15:20:06.821849Z",
    "version": 1
  }
]
```

## CS Local Development Setup

Should your development needs require a running instance of CS to test with, here is how to spin up a locally running Clusters Service with containerized database suitable enough for testing.

To complete the below steps you will need:
1) `podman`, `ocm` cli (latest), and [`yq`](https://github.com/mikefarah/yq) cli (version 4+)
2) An up-to-date [Clusters Service repo](https://gitlab.cee.redhat.com/service/uhc-clusters-service) cloned down (can also use a fork if you have one)

> If you don't have or want to install `yq`, any steps below using `yq` can be done manually

From the root of the CS repo on our system:

1) Setup required config files

```bash
# Setup the development.yml
cp ./configs/development.yml .

# Update any required empty strings to 'none'
yq -i '(.aws-access-key-id, .aws-secret-access-key, .route53-access-key-id, .route53-secret-access-key, .oidc-access-key-id, .oidc-secret-access-key, .network-verifier-access-key-id, .network-verifier-secret-access-key, .client-id, .client-secret) = "none"' development.yml

# Update provision shards config with new shard
cat <<EOF > ./provision_shards.config
provision_shards:
- id: 1
  hypershift_config: |
    apiVersion: v1
    kind: Config
    clusters:
    - name: default
      cluster:
        server: https://api.hs-sc-81qmsevf0.dksu.i1.devshift.org:6443
    users:
    - name: default
      user:
        token: ${HYPERSHIFT_INTEGRATION_TOKEN}
    contexts:
    - name: default
      context:
        cluster: default
        user: default
    current-context: default
  status: active
  region: eastus
  cloud_provider: azure
  topology: dedicated
EOF

# Enable the eastus region in cloud region constraints config
yq -i '.cloud_regions |= map(select(.id == "eastus").enabled = true)' configs/cloud-resource-constraints/cloud-region-constraints.yaml

# you can verify the region change with the below
yq '.cloud_regions[] | select(.id == "eastus")' configs/cloud-resource-constraints/cloud-region-constraints.yaml

# Update region_constraints.config with new cloud provider
cat <<EOF > ./region_constraints.config
cloud_providers:
- name: azure
  regions:
    - name: eastus
      version_constraints:
        min_version: 4.11.0
      product_constraints:
        - product: hcp
          version_constraints:
            min_version: 4.12.23
EOF
```

2) Follow CS dev setup process:

```bash
# Build CS
make cmds

# Setup local DB
make db/setup

# Initialize the DB
./clusters-service init --config-file ./development.yml
```

3) Start CS:

```bash
./clusters-service serve --config-file development.yml --demo-mode
```

You now have a running, functioning local CS deployment

To interact with CS:
1) Login to your local CS deployment

```bash
ocm login --url=http://localhost:8000 --use-auth-code
```

2) Create a test cluster - note that `version.id` must match the version inserted into the database earlier.
```bash
NAME="<INSERT-NAME-HERE>"
cat <<EOF > cluster-test.json
{
  "name": "$YOURNAME-aro-hcp",
  "product": {
    "id": "aro"
  },
  "ccs": {
    "enabled": true
  },
  "region": {
    "id": "eastus"
  },
  "hypershift": {
    "enabled": true
  },
  "multi_az": true,
  "azure": {
    "resource_name": "$YOURNAME-test-name",
    "subscription_id": "00000000-0000-0000-0000-000000000000",
    "resource_group_name": "$YOURNAME-test-rg",
    "tenant_id": "$YOURNAME-test-tenant",
    "managed_resource_group_name": "$YOURNAME-test-mrg",
    "subnet_resource_id": "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/$YOURNAME-test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/mySubnet",
    "network_security_group_resource_id":"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/$YOURNAME-test-rg/providers/Microsoft.Network/networkSecurityGroups/myNSG"
  },
  "properties": {
    "provision_shard_id": "1"
  },
  "version": {
    "id": "openshift-v4.15.11"
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

## CS Azure Database Postgres

To create a Postgres DB on Azure enabled for Entra authentication, a svc cluster needs to be created with the `deployCsInfra` parameter set to `true` in the `svc-cluster.bicepparam` file.

### Access the database from outside of the AKS cluster

The connection parameters for the database can be aquired via `AKSCONFIG=svc-cluster make cs-out-of-cluster-db-access`.

The output are in ENV var format for the `psql` tool, so this works to get a connection into the DB#

```sh
eval $(AKSCONFIG=svc-cluster make cs-out-of-cluster-db-access)
psql
```

> The password is a temporary access token that is valid only for 1h

### Access the database from inside of the AKS cluster

* the `cs` namespace of the svc cluster contains
  * a `ConfigMap` named `db` with the connection parameters
  * a `ServiceAccount` named `cs` that is annotated for workload identity usage
* the CS pod will need to be labeled with `azure.workload.identity/use: "true"`, which injects several ENV variables prefixed with `AZ_*`

TODO: CS needs to use these `AZ_*` env variables to get an access token to be used as a DB password
