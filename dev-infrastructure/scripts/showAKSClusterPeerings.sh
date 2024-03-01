#!/bin/bash

RG=$1

echo "Checking RG $RG for VPN GW peerings"

for vnet in $( az network vnet list -g $RG --query "[].{Name:name}" -o tsv); do
    az network vnet peering list  \
        --vnet-name $vnet \
        -g $RG \
        --query "[].{Name:name, Peeringstate:peeringState,Remote:remoteVirtualNetwork.id, AddressSpace:addressSpace.addressPrefixes[0] } "  \
        -o tsv
done
