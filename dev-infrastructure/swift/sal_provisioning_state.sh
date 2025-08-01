#!/bin/bash

source sal_env_vars

parent_guid=$(az network vnet list -g $resource_group | jq -r '.[].resourceGuid')

az network vnet subnet show -g $resource_group -n $subnet_name --vnet-name $vnet_name | jq '.serviceAssociationLinks[]'
