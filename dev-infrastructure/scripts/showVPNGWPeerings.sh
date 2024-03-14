#!/bin/bash

VPNGWRP=$1

LOCATION=$(az group list --query "[?name=='$VPNGWRP'].{Location:location}" -o tsv)
echo "Checking RG $VPNGWRP in $LOCATION for peerings"

echo "Found these Peerings:"
az network vnet peering list  \
    --vnet-name dev-vpn-vnet \
    -g $VPNGWRP \
    --query "[].{Name:name, Peeringstate:peeringState,Remote:remoteVirtualNetwork.id, AddressSpace:remoteVirtualNetworkAddressSpace.addressPrefixes[0]} "  \
    -o tsv
