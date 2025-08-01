#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source sal_env_vars

if ! is_service_principal; then
    error_msg "Logged in as incorrect user, expecting Service Principal"
fi

api_version=2021-08-01

az rest \
    --method DELETE \
    --url /subscriptions/$subscription/resourceGroups/$resource_group/providers/Microsoft.Network/virtualNetworks/$vnet_name/subnets/$subnet_name/serviceAssociationLinks/$resource?api-version=$api_version \
    --debug

