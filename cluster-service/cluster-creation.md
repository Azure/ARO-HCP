# Creating an HCP via Cluster Service

This document outlines the process of creating an HCP via the Cluster Service running either on a developer machine or in a [personal DEV environment](../docs/personal-dev.md).

## Prerequisites

* Create a [personal DEV environment](../docs/personal-dev.md) and ensure you have access
* If running CS on the AKS cluster, port-forward the CS service to your local machine

    ```bash
    kubectl port-forward svc/clusters-service 8000:8000 -n clusters-service
    ```

* Otherwise start CS on your local machine

## Creating a cluster

1) Login to your CS deployment

* Access your CS deployment locally

    ```bash
    KUBECONFIG=$(make infra.svc.aks.kubeconfigfile) kubectl port-forward svc/clusters-service 8000:8000 -n clusters-service
    ```

  Alternative: if you run CS on your local machine, this step is not necessary.

* Login to your CS deployment

    ```bash
    ocm login --url=http://localhost:8000 --use-auth-code
    ```

1) Create pre-requisite resources for cluster creation

Replace `resource-group`, `vnet-name`, `nsg-name` and `subnet-name` with any valid names.

* Create a resource group for your ARO HCP cluster. This is used, alongside the resource name and subscription ID, to represent your ARO HCP cluster resource in Azure.

    ```bash
    az group create --name <resource-group> --location "westus3"
    ```

* Create a Virtual Network.
  > [!NOTE] This may be created in the same resource group above, or a different one.

    ```bash
    az network vnet create -n <vnet-name> -g <resource-group> --subnet-name <subnet-name>
    ```

* Create a Network security group
  > [!NOTE] This may be created in the same resource group above, or a different one.

    ```bash
    az network nsg create -n <nsg-name> -g <resource-group>
    ```

* Associate the created VNet with the subnet of the created NSG

    ```bash
    az network vnet subnet update -g <resource-group> -n <subnet-name> --vnet-name <vnet-name> --network-security-group <nsg-name>
    ```

* Generate a random alphanumeric string used as a suffix for the User-Assigned Managed Identities of the operators of the cluster
  > [!NOTE] The random suffix used has to be different for each cluster to be created

    ```bash
    export OPERATORS_UAMIS_SUFFIX=$(openssl rand -hex 3)
    ```

* Define and export an environment variable with the desired name of the ARO-HCP Cluster in CS

    ```bash
    export CS_CLUSTER_NAME="<desired-cluster-name>"
    ```

* Create the User-Assigned Managed Identities for the Control Plane operators. This assumes OCP 4.18 based will be created.
  > [!NOTE] Managed Identities cannot be reused between operators nor between clusters. This is, each operator must use a different managed identity, and different clusters must use different managed identities, even for the same operators.
  >
  > [!NOTE] Remember to cleanup the created Managed Identities once you are done with the cluster. See the `Cleaning up a Cluster` section

    ```bash
    # We create the control plane operators User-Assigned Managed Identities
    az identity create -n ${USER}-${CS_CLUSTER_NAME}-cp-control-plane-${OPERATORS_UAMIS_SUFFIX} -g <resource-group>
    az identity create -n ${USER}-${CS_CLUSTER_NAME}-cp-cluster-api-azure-${OPERATORS_UAMIS_SUFFIX} -g <resource-group>
    az identity create -n ${USER}-${CS_CLUSTER_NAME}-cp-cloud-controller-manager-${OPERATORS_UAMIS_SUFFIX} -g <resource-group>
    az identity create -n ${USER}-${CS_CLUSTER_NAME}-cp-ingress-${OPERATORS_UAMIS_SUFFIX} -g <resource-group>
    az identity create -n ${USER}-${CS_CLUSTER_NAME}-cp-disk-csi-driver-${OPERATORS_UAMIS_SUFFIX} -g <resource-group>
    az identity create -n ${USER}-${CS_CLUSTER_NAME}-cp-file-csi-driver-${OPERATORS_UAMIS_SUFFIX} -g <resource-group>
    az identity create -n ${USER}-${CS_CLUSTER_NAME}-cp-image-registry-${OPERATORS_UAMIS_SUFFIX} -g <resource-group>
    az identity create -n ${USER}-${CS_CLUSTER_NAME}-cp-cloud-network-config-${OPERATORS_UAMIS_SUFFIX} -g <resource-group>
    az identity create -n ${USER}-${CS_CLUSTER_NAME}-cp-kms-${OPERATORS_UAMIS_SUFFIX} -g <resource-group>

    # And then we create variables containing their Azure resource IDs and export them to be used later
    export CP_CONTROL_PLANE_UAMI=$(az identity show -n ${USER}-${CS_CLUSTER_NAME}-cp-control-plane-${OPERATORS_UAMIS_SUFFIX} -g <resource-group> | jq -r '.id')
    export CP_CAPZ_UAMI=$(az identity show -n ${USER}-${CS_CLUSTER_NAME}-cp-cluster-api-azure-${OPERATORS_UAMIS_SUFFIX} -g <resource-group> | jq -r '.id')
    export CP_CCM_UAMI=$(az identity show -n ${USER}-${CS_CLUSTER_NAME}-cp-cloud-controller-manager-${OPERATORS_UAMIS_SUFFIX} -g <resource-group> | jq -r '.id')
    export CP_INGRESS_UAMI=$(az identity show -n ${USER}-${CS_CLUSTER_NAME}-cp-ingress-${OPERATORS_UAMIS_SUFFIX} -g <resource-group> | jq -r '.id')
    export CP_DISK_CSI_DRIVER_UAMI=$(az identity show -n ${USER}-${CS_CLUSTER_NAME}-cp-disk-csi-driver-${OPERATORS_UAMIS_SUFFIX} -g <resource-group> | jq -r '.id')
    export CP_FILE_CSI_DRIVER_UAMI=$(az identity show -n ${USER}-${CS_CLUSTER_NAME}-cp-file-csi-driver-${OPERATORS_UAMIS_SUFFIX} -g <resource-group> | jq -r '.id')
    export CP_IMAGE_REGISTRY_UAMI=$(az identity show -n ${USER}-${CS_CLUSTER_NAME}-cp-image-registry-${OPERATORS_UAMIS_SUFFIX} -g <resource-group> | jq -r '.id')
    export CP_CNC_UAMI=$(az identity show -n ${USER}-${CS_CLUSTER_NAME}-cp-cloud-network-config-${OPERATORS_UAMIS_SUFFIX} -g <resource-group> | jq -r '.id')
    export CP_KMS_UAMI=$(az identity show -n ${USER}-${CS_CLUSTER_NAME}-cp-kms-${OPERATORS_UAMIS_SUFFIX} -g <resource-group> | jq -r '.id')
    ```

* Create the User-Assigned Managed Identities for the Data Plane operators. This assumes OCP 4.18 clusters will be created.
  > [!NOTE] Managed Identities cannot be reused between operators nor between clusters. This is, each operator must use a different managed identity, and different clusters must use different managed identities, even for the same operators.
  >
  > [!NOTE] Remember to cleanup the created Managed Identities once you are done with the cluster. See the `Cleaning up a Cluster` section

    ```bash
    # We create the data plane operators User-Assigned Managed Identities
    az identity create -n ${USER}-${CS_CLUSTER_NAME}-dp-disk-csi-driver-${OPERATORS_UAMIS_SUFFIX} -g <resource-group>
    az identity create -n ${USER}-${CS_CLUSTER_NAME}-dp-image-registry-${OPERATORS_UAMIS_SUFFIX} -g <resource-group>
    az identity create -n ${USER}-${CS_CLUSTER_NAME}-dp-file-csi-driver-${OPERATORS_UAMIS_SUFFIX} -g <resource-group>

    # And then we create variables containing their Azure resource IDs and export them to be used later
    export DP_DISK_CSI_DRIVER_UAMI=$(az identity show -n ${USER}-${CS_CLUSTER_NAME}-dp-disk-csi-driver-${OPERATORS_UAMIS_SUFFIX} -g <resource-group> | jq -r '.id')
    export DP_IMAGE_REGISTRY_UAMI=$(az identity show -n ${USER}-${CS_CLUSTER_NAME}-dp-image-registry-${OPERATORS_UAMIS_SUFFIX} -g <resource-group> | jq -r '.id')
    export DP_FILE_CSI_DRIVER_UAMI=$(az identity show -n ${USER}-${CS_CLUSTER_NAME}-dp-file-csi-driver-${OPERATORS_UAMIS_SUFFIX} -g <resource-group> | jq -r '.id')
    ```

* Create the User-Assigned Service Managed Identity
  > [!NOTE] Managed Identities cannot be reused between operators nor between clusters. This is, each operator must use a different managed identity, and different clusters must use different managed identities, even for the same operators.
  >
  > [!NOTE] Remember to cleanup the created Managed Identities once you are done with the cluster. See the `Cleaning up a Cluster` section

    ```bash
    az identity create -n ${USER}-${CS_CLUSTER_NAME}-service-managed-identity-${OPERATORS_UAMIS_SUFFIX} -g <resource-group>

    export SERVICE_MANAGED_IDENTITY_UAMI=$(az identity show -n ${USER}-${CS_CLUSTER_NAME}-service-managed-identity-${OPERATORS_UAMIS_SUFFIX} -g <resource-group> | jq -r '.id')
    ```

1) Create the cluster. This assumes OCP 4.18 clusters will be created.
    > [!NOTE] See the [Cluster Service API](https://api.openshift.com/#/default/post_api_clusters_mgmt_v1_clusters) documentation
    > for further information on the properties within the payload below

    ```bash
    SUBSCRIPTION_NAME="ARO Hosted Control Planes (EA Subscription 1)"
    RESOURCENAME="<INSERT-NAME>"
    SUBSCRIPTIONID=$(az account list | jq -r ".[] | select (.name == \"$SUBSCRIPTION_ID\") | .id")
    RESOURCEGROUPNAME="<INSERT-NAME>"
    TENANTID=$(az account list | jq -r ".[] | select (.name == \"$SUBSCRIPTION_ID\") | .tenantId")
    MANAGEDRGNAME="<INSERT-NAME>"
    SUBNETRESOURCEID="<INSERT-NAME>"
    NSG="<INSERT-NAME>"
    cat <<EOF > cluster-test.json
    {
      "name": "$CS_CLUSTER_NAME",
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
        "subscription_id": "$SUBSCRIPTIONID",
        "resource_group_name": "$RESOURCEGROUPNAME",
        "tenant_id": "$TENANTID",
        "managed_resource_group_name": "$MANAGEDRGNAME",
        "subnet_resource_id": "$SUBNETRESOURCEID",
        "network_security_group_resource_id":"$NSG",
        "operators_authentication": {
          "managed_identities": {
            "managed_identities_data_plane_identity_url": "https://dummyhost.identity.azure.net",
            "control_plane_operators_managed_identities": {
              "control-plane": {
                "resource_id": "$CP_CONTROL_PLANE_UAMI"
              },
              "cluster-api-azure": {
                "resource_id": "$CP_CAPZ_UAMI"
              },
              "cloud-controller-manager": {
                "resource_id": "$CP_CCM_UAMI"
              },
              "ingress": {
                "resource_id": "$CP_INGRESS_UAMI"
              },
              "disk-csi-driver": {
                "resource_id": "$CP_DISK_CSI_DRIVER_UAMI"
              },
              "file-csi-driver": {
                "resource_id": "$CP_FILE_CSI_DRIVER_UAMI"
              },
              "image-registry": {
                "resource_id": "$CP_IMAGE_REGISTRY_UAMI"
              },
              "cloud-network-config": {
                "resource_id": "$CP_CNC_UAMI"
              },
              "kms": {
                "resource_id": "$CP_KMS_UAMI"
              }
            },
            "data_plane_operators_managed_identities": {
              "disk-csi-driver": {
                "resource_id": "$DP_DISK_CSI_DRIVER_UAMI"
              },
              "image-registry": {
                "resource_id": "$DP_IMAGE_REGISTRY_UAMI"
              },
              "file-csi-driver": {
                "resource_id": "$DP_FILE_CSI_DRIVER_UAMI"
              }
            },
            "service_managed_identity": {
              "resource_id": "$SERVICE_MANAGED_IDENTITY_UAMI"
            }
          }
        }
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

```bash
ocm get /api/clusters_mgmt/v1/clusters/$CLUSTER_ID/node_pools/$UID
```

### Cleaning up a Cluster

1. Delete the cluster

   ```bash
   ocm delete /api/clusters_mgmt/v1/clusters/$CLUSTER_ID
   ```

   > [!NOTE] Deleting it will also delete all of its associated node pools.

2. Delete the created managed identities that were initially created for the cluster:

   ```bash
   az identity delete --ids "${CP_CONTROL_PLANE_UAMI}"
   az identity delete --ids "${CP_CAPZ_UAMI}"
   az identity delete --ids "${CP_INGRESS_UAMI}"
   az identity delete --ids "${CP_DISK_CSI_DRIVER_UAMI}"
   az identity delete --ids "${CP_FILE_CSI_DRIVER_UAMI}"
   az identity delete --ids "${CP_IMAGE_REGISTRY_UAMI}"
   az identity delete --ids "${CP_CNC_UAMI}"
   az identity delete --ids "${CP_KMS_UAMI}"
   az identity delete --ids "${DP_DISK_CSI_DRIVER_UAMI}"
   az identity delete --ids "${DP_IMAGE_REGISTRY_UAMI}"
   az identity delete --ids "${DP_FILE_CSI_DRIVER_UAMI}"
   az identity delete --ids "${SERVICE_MANAGED_IDENTITY_UAMI}"
   ```

## Creating an ARO HCP Cluster via Frontend

To create a cluster via the Frontend, check out the documentation and scripts in the [demo](../demo) folder.
