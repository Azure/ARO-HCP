#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source swift_env_vars
if ! is_redhat_user; then
    az login
    parent_guid=$(az network vnet list -g $resource_group | jq -r '.[].resourceGuid')
fi

parent_guid=$(az network vnet list -g $resource_group | jq -r '.[].resourceGuid')

if ! is_service_principal; then
    source login_fpa.sh
fi

if [[ "$parent_guid" == "" ]]; then
    error_msg "Parent GUID is empty"
fi

primary_context_id=$(uuidgen)
secondary_context_id=$(uuidgen)

api_version=2021-08-01

body=$( jq -n \
    --arg rn $resource \
    --arg lrt $linked_resource_type \
    --arg prg $parent_guid \
    --arg lc $location \
    --arg pcid $primary_context_id \
    --arg scid $secondary_context_id \
    '{name: $rn, properties: {linkedResourceType: $lrt, parentResourceGuid: $prg, locations: [ $lc ], Details: [{properties: {primaryContextRequestId: $pcid, secondaryContextRequestId: $scid}, name: "arohcpdetail"}]}}' )

az rest \
    --method PUT \
    --url /subscriptions/$subscription/resourceGroups/$resource_group/providers/Microsoft.Network/virtualNetworks/$vnet_name/subnets/$subnet_name/serviceAssociationLinks/$resource?api-version=$api_version \
    --body "$body" \
    --debug

