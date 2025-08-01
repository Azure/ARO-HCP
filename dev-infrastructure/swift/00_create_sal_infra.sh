#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source sal_env_vars

if ! is_dev_subscription; then
    error_msg "Check subscription you are logged into it should be: \"ARO SRE Team - INT (EA Subscription 3)\""
fi

if ! is_redhat_user; then
    error_msg "Logged in as incorrect user, expecting Red Hat user"
fi

echo "Creating resource group"
az group create --name "$resource_group" --location "$location"

echo "Creating vnet and subnet"
az network vnet create -g "$resource_group" -n "$vnet_name" --address-prefix "$address_prefix" --subnet-name "$subnet_name" --subnet-prefixes "$subnet_prefix" --location "$location"

echo "Delegate subnet to $linked_resource_type"
az network vnet subnet update -g "$resource_group" --vnet-name "$vnet_name" --name "$subnet_name" --delegations "$linked_resource_type"