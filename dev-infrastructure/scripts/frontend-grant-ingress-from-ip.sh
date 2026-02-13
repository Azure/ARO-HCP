#!/bin/bash

RESOURCE_GROUP=$1

NSG_NAME="svc-cluster-node-nsg"
for url in "ifconfig.me" "icanhazip.com" "ifconfig.co"; do
    IP_ADDRESS=$(curl -sf -4 "$url")
    
    if [ -n "$IP_ADDRESS" ]; then
        break
    fi
done

if [ -z "$IP_ADDRESS" ]; then
    echo "ERROR: Could not determine public IP from any provider."
    exit 1
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
