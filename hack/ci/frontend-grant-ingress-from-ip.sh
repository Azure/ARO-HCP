#!/bin/bash

if [ "$#" -ne 2 ] || [ -z "$1" ] || [ -z "$2" ]; then
    echo "Usage: $0 RESOURCE_GROUP IP_ADDRESS" >&2
    exit 1
fi

RESOURCE_GROUP=$1
IP_ADDRESS=$2
NSG_NAME="svc-cluster-node-nsg"

az network nsg rule create \
    --resource-group "${RESOURCE_GROUP}" \
    --nsg-name "${NSG_NAME}" \
    --name "allow-istio-ingress" \
    --access Allow \
    --protocol Tcp \
    --direction Inbound \
    --source-address-prefix "${IP_ADDRESS}" \
    --source-port-range "*" \
    --destination-address-prefix "*" \
    --destination-port-range "443" \
    --priority 1000
