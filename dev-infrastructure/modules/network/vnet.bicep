param location string

@description('The VNET that should be tagged')
param vnetName string

@description('Enable swift')
param enableSwift bool

@description('The address space for the VNET')
param vnetAddressPrefix string

@description('The resource ID of the user-assigned managed identity that will be used to execute the script')
param deploymentMsiId string

@description('The name of the storage account used by deployment scripts (must have allowSharedKeyAccess=false and MI granted Storage File Data Privileged Contributor)')
param deploymentScriptStorageAccountName string = ''

@description('The subnet ID for the deployment scripts ACI container (required when using MI-auth storage)')
param deploymentScriptSubnetId string = ''

// Network Contributor Role
// https://www.azadvertizer.net/azrolesadvertizer/4d97b98b-1d4f-4787-a291-c67834d212e7.html
var networkContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '4d97b98b-1d4f-4787-a291-c67834d212e7'
)

// Tag Contributor Role
// https://www.azadvertizer.net/azrolesadvertizer/4a9ae827-6dc8-4573-8ac7-8239d42aa03f.html
var tagContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '4a9ae827-6dc8-4573-8ac7-8239d42aa03f'
)

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

resource deploymentMsiNetworkContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: resourceGroup()
  name: guid(deploymentMsiId, networkContributorRoleId, resourceGroup().id)
  properties: {
    roleDefinitionId: networkContributorRoleId
    principalId: reference(deploymentMsiId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
  }
}

resource deploymentMsiTagContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: resourceGroup()
  name: guid(deploymentMsiId, tagContributorRoleId, resourceGroup().id)
  properties: {
    roleDefinitionId: tagContributorRoleId
    principalId: reference(deploymentMsiId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
  }
}

resource vnetWithSwiftDeployment 'Microsoft.Resources/deploymentScripts@2023-08-01' = if (enableSwift) {
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
      set -euo pipefail
      az account set --subscription "${VNET_SUBSCRIPTION_ID}"

      # The deployment MSI's Network/Tag Contributor role assignments are created in
      # this same deployment. Azure RBAC is eventually consistent, so the first write
      # can fail with AuthorizationFailed ("if access was recently granted, please
      # refresh your credentials") before the assignment propagates to the data plane.
      # Retry the write to absorb that lag, mirroring scripts/validate-mi-aad-propagation.sh.
      MAX_WAIT=120
      POLL_INTERVAL=5

      retry() {
        local start=${SECONDS}
        until "$@"; do
          if [ $((SECONDS - start)) -ge "${MAX_WAIT}" ]; then
            echo "✗ '$*' still failing after $((SECONDS - start))s" >&2
            return 1
          fi
          echo "Command failed (likely RBAC propagation); retrying in ${POLL_INTERVAL}s..." >&2
          sleep "${POLL_INTERVAL}"
        done
      }

      if az network vnet show \
        --resource-group "${VNET_RG}" \
        --name "${VNET_NAME}" \
        --output none 2>/dev/null; then
        echo "VNet exists. Updating tags..."
        retry az resource tag \
          --tags stampcreatorserviceinfo=true \
          --resource-group "${VNET_RG}" \
          --name "${VNET_NAME}" \
          --resource-type Microsoft.Network/virtualnetworks \
          --api-version 2024-05-01
      else
        echo "VNet does not exist or not yet authorized. Creating..."
        retry az network vnet create \
          --resource-group "${VNET_RG}" \
          --name "${VNET_NAME}" \
          --address-prefixes "${VNET_ADDRESS_PREFIX}" \
          --tags stampcreatorserviceinfo=true
      fi
    '''
    timeout: 'PT10M'
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
    storageAccountSettings: !empty(deploymentScriptStorageAccountName)
      ? {
          storageAccountName: deploymentScriptStorageAccountName
        }
      : null
    containerSettings: !empty(deploymentScriptSubnetId)
      ? {
          subnetIds: [
            {
              id: deploymentScriptSubnetId
            }
          ]
        }
      : null
  }
  dependsOn: [
    deploymentMsiNetworkContributorRoleAssignment
    deploymentMsiTagContributorRoleAssignment
  ]
}

resource provisionedSwiftVnet 'Microsoft.Network/virtualNetworks@2024-05-01' existing = if (enableSwift) {
  name: vnetName
  dependsOn: [
    vnetWithSwiftDeployment
  ]
}

output vnetId string = enableSwift ? provisionedSwiftVnet.id : vnet.id
output vnetName string = enableSwift ? provisionedSwiftVnet.name : vnet.name
