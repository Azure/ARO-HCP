#!/bin/bash

RESOURCE_GROUP=$1

NSG_NAME="svc-cluster-node-nsg"
IP_ADDRESS=$(curl -s -4 ifconfig.me || curl -s -4 icanhazip.com)

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
