#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source swift_env_vars

if ! is_redhat_user; then
    az login
fi

parent_guid=$(az network vnet list -g $resource_group -o json | jq -r '.[].resourceGuid')

subnet_guid=$(az rest --method get --url "/subscriptions/$subscription/resourceGroups/$resource_group/providers/Microsoft.Network/virtualNetworks/$vnet_name/subnets/$subnet_name?api-version=2023-06-01" --output json | jq -r '.properties.serviceAssociationLinks[0].properties.subnetId')

vnet_guid=$(az network vnet show -g $resource_group --name $vnet_name --output json | jq -r '.resourceGuid')

kubectl apply -f - << EOF 
apiVersion: multitenancy.acn.azure.com/v1alpha1
kind: PodNetwork
metadata:
  name: pn1
spec:
  subnetResourceID: /subscriptions/$subscription/resourceGroups/$resource_group/providers/Microsoft.Network/virtualNetworks/$vnet_name/subnets/$subnet_name
  networkID: $vnet_guid 
  subnetGUID: $subnet_guid 
  deviceType: acn.azure.com/vnet-nic
EOF