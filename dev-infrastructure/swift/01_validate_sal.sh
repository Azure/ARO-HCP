#!/bin/bash

set -o nounset
set -o pipefail

source swift_env_vars

if ! is_service_principal; then
    echo "Logging in as service principal"
    source login_fpa.sh
fi

parent_guid=$(az network vnet list -g $resource_group | jq -r '.[].resourceGuid')
api_version=2021-08-01
body=$( jq -n \
    --arg rn $resource \
    --arg lrt $linked_resource_type \
    --arg prg $parent_guid \
    '{name: $rn, properties: {linkedResourceType: $lrt, parentResourceGuid: $prg}}' )

return_code=$(az rest \
    --method POST \
    --url /subscriptions/$subscription/resourceGroups/$resource_group/providers/Microsoft.Network/virtualNetworks/$vnet_name/subnets/$subnet_name/serviceAssociationLinks/$resource/validate?api-version=$api_version \
    --body "$body" \
    --verbose 2>&1 | grep "Response status:" | sed 's/INFO: Response status: \(.*\)/\1/')

echo "Return code: $return_code"

