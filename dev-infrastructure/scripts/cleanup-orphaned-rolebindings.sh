#!/bin/bash

RESOURCEGROUP=$1

# List all role assignments and filter for 'ServicePrincipal'
roleAssignments=$(az role assignment list -g ${RESOURCEGROUP} --query "[?principalType=='ServicePrincipal'].{id:id, principalId:principalId}" -o tsv)

if [ -n "$roleAssignments" ]; then
    while IFS=$'\t' read -r id principalId; do
        # Check if the Managed Identity exists
        identityExists=$(az ad sp show --id $principalId --query id -o tsv 2>/dev/null)

        if [ -z "$identityExists" ]; then
            echo "Role Assignment ID $id is bound to a non-existent Managed Identity $principalId... deleting"
            az role assignment delete --ids "$id"
        fi
    done <<< "$roleAssignments"
fi
