#!/bin/bash
source env_vars
LOCATION=uksouth # The ONLY supported region for ARO-HCP in INT

az group create -n ${CUSTOMER_RG_NAME} -l ${LOCATION} 

az deployment group create \
  --name 'aro-hcp' --resource-group ${CUSTOMER_RG_NAME} \
  --template-file bicep/cluster.bicep \
  --parameters \
    customerNsgName=${CUSTOMER_NSG} \
    customerVnetName=${CUSTOMER_VNET_NAME} \
    customerVnetSubnetName=${CUSTOMER_VNET_SUBNET1} \
    clusterName=${CLUSTER_NAME} \
    location=${LOCATION} \
    managedResourceGroupName=${MANAGED_RESOURCE_GROUP}

az deployment group create \
  --name 'aro-hcp-node-pool' --resource-group ${CUSTOMER_RG_NAME} \
  --template-file bicep/nodepool.bicep \
  --parameters \
    clusterName=${CLUSTER_NAME} \
    location=${LOCATION} --debug