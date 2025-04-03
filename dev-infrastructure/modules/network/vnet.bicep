param location string

@description('The VNET that should be tagged')
param vnetName string

@description('Enable swift')
param enableSwift bool

@description('The address space for the VNET')
param vnetAddressPrefix string

@description('The resource ID of the user-assigned managed identity that will be used to execute the script')
param deploymentMsiId string


//
//  D E P L O Y   V N E T   W IT H O U T   S W I F T
//

resource vnet 'Microsoft.Network/virtualNetworks@2023-11-01' = if (!enableSwift) {
  location: location
  name: vnetName
  properties: {
    addressSpace: {
      addressPrefixes: [
        vnetAddressPrefix
      ]
    }
  }
}

//
//  D E P L O Y   V N E T   W I T H   S W I F T
//

resource vnetWithSwiftDeployment 'Microsoft.Resources/deploymentScripts@2020-10-01' = if (enableSwift) {
  name: 'vnet-${vnetName}'
  location: location
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${deploymentMsiId}': {}
    }
  }
  kind: 'AzureCLI'
  properties: {
    azCliVersion: '2.53.1'
    scriptContent: '''
      az network vnet show \
        --resource-group "${VNET_RG}" \
        --name "${VNET_NAME}" \
        --output none 2>/dev/null

      if [ $? -ne 0 ]; then
        echo "VNet does not exist. Creating..."
        az network vnet create \
          --resource-group "${VNET_RG}" \
          --name "${VNET_NAME}" \
          --address-prefixes "${VNET_ADDRESS_PREFIX}" \
          --tags stampcreatorserviceinfo=true
      else
        echo "VNet exists. Updating tags..."
        az network vnet update \
          --resource-group "${VNET_RG}" \
          --name "${VNET_NAME}" \
          --set tags.stampcreatorserviceinfo="'true'"
      fi
    '''
    timeout: 'PT5M'
    cleanupPreference: 'OnSuccess'
    retentionInterval: 'P1D'
    environmentVariables: [
      {
        name: 'VNET_NAME'
        value: vnetName
      }
      {
        name: 'VNET_RG'
        value: resourceGroup().name
      }
      {
        name: 'VNET_ADDRESS_PREFIX'
        value: vnetAddressPrefix
      }
    ]
  }
}


output vnetName string = vnetName
