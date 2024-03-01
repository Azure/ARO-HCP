#!/bin/bash

VPNGWRP=$1

echo "Checking RG $VPNGWRP for disconnected Peerings"

if [ $(az group exists --name $VPNGWRP) = false ]; then
   echo "Resourcegroup $VPNGWRP does not exist (anymore). Nothing to do."
   exit 0
fi

echo "Found these Peerings:"
az network vnet peering list  \
    --vnet-name dev-vpn-vnet \
    -g $VPNGWRP \
    --query "[].{Name:name, Peeringstate:peeringState,Remote:remoteVirtualNetwork.id } "  \
    -o tsv


for peering in $( az network vnet peering list  \
    --vnet-name dev-vpn-vnet \
    -g $VPNGWRP \
    --query "[? peeringState=='Disconnected'] | [].{Name:name} "  \
    -o tsv )
do
    echo "Deleting $peering"
    az network vnet peering delete --vnet-name dev-vpn-vnet -g $VPNGWRP -n $peering
done