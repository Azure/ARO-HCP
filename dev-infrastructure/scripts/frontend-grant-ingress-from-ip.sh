#!/bin/bash

RESOURCE_GROUP=$1

NSG_NAME="svc-cluster-node-nsg"

IP_ADDRESS=$(dig @1.1.1.1 ch txt whoami.cloudflare +short | tr -d '"')

# Fallback to OpenDNS if that fails
if [ -z "$IP_ADDRESS" ]; then
    IP_ADDRESS=$(dig @208.67.222.222 myip.opendns.com +short | tr -d '"')
fi

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
