param location string

@description('The VNET that should be tagged')
param vnetName string

@description('Enable swift')
param enableSwift bool

@description('The address space for the VNET')
param vnetAddressPrefix string

@description('The resource ID of the user-assigned managed identity that will be used to execute the script')
param deploymentMsiId string

// Enabling a VNET for Swift is a matter of placing the stampcreatorserviceinfo=true tag on it.
// The tagging itself needs to be done by an identity that is registered with the Network/Swift RP.
// All bicep code deployed via EV2 is executed by an EV2 identity that is not and cannot be registered
// for Swift usage. Hence we use a deployment script for the tagging where we are in control of the
// identity used to execute the script and tag the VNET.

//
//  D E P L O Y   V N E T   W I T H O U T   S W I F T
//

// for non-swift deployments, we create the VNET regularly... so much faster
resource vnet 'Microsoft.Network/virtualNetworks@2024-05-01' = if (!enableSwift) {
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

// for swift deployments we use a deployment script to create the VNET or just
// tag it when it already exists. The identity used for this needs to be registered
// for swift usage with the network RP.

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
      az account set --subscription "${VNET_SUBSCRIPTION_ID}"

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
        az resource tag \
          --tags stampcreatorserviceinfo=true \
          --resource-group "${VNET_RG}" \
          --name "${VNET_NAME}" --resource-type Microsoft.Network/virtualnetworks
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
        name: 'VNET_SUBSCRIPTION_ID'
        value: subscription().subscriptionId
      }
      {
        name: 'VNET_ADDRESS_PREFIX'
        value: vnetAddressPrefix
      }
    ]
  }
}

output vnetName string = vnetName
