#!/bin/bash

echo "Gateway Peerings"
echo "------------------------"
for RG in $( az network vnet list --query "[?contains(name,'dev-vpn')].{Rg:resourceGroup}" -o tsv ); do
    for vnet in $( az network vnet list -g $RG --query "[?contains(name,'dev-vpn')].{Name:name}" -o tsv); do
        location=$( az group show  -g $RG --query "{location:location}" -o tsv ).
        config=$( az group show  -g $RG --query "{Config:tags.CreatedByConfig}" -o tsv )    
        echo "-ResourceGroup: $RG VNet: $vnet Location: $location Config Used: $config" 
        IFS=$'\n' read -r -d '' -a arr < <( az network vnet peering list \
                --vnet-name $vnet \
                -g $RG \
                --query "[].{Name:name, Peeringstate:peeringState,Remote:remoteVirtualNetwork.id, AddressSpace:remoteAddressSpace.addressPrefixes[0] } "  \
                -o tsv && printf '\0' )        
        for line in "${arr[@]}"  
        do
            splitarr=( $(echo $line |  cut -d "/" -f 1,5,9,10 | cut -d " " -f 1-6 ))
            printf " %-25s %-15s %-35s %20s" ${splitarr[0]} ${splitarr[1]} ${splitarr[2]} ${splitarr[3]}
            echo " " 
        done
    done
    echo " " 
   
done


echo " "
echo "AKS Clusters"
az aks list --query "[?contains(resourceGroup,'hcp')].{Name:name,RG:resourceGroup,ProvisionState:provisioningState,Config:tags.CreatedByConfig}" -o table
