@description('The VNET that should be tagged')
param vnetName string

@description('The name of the VNet to tag')
param tagName string

@description('The tag value to set')
param tagValue string

@description('The resource ID of the user-assigned managed identity that will be used to execute the script')
param deploymentMsiId string

resource deploymentScript 'Microsoft.Resources/deploymentScripts@2020-10-01' = {
  name: 'vnet-tagging-${vnetName}-${uniqueString(tagName)}'
  location: resourceGroup().location
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
      az network vnet update \
        --name $VNET_NAME \
        --resource-group $VNET_RG \
        --set tags.$TAG_NAME=$TAG_VALUE
    '''
    timeout: 'PT5M'
    cleanupPreference: 'OnSuccess'
    forceUpdateTag: tagValue
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
        name: 'TAG_NAME'
        value: tagName
      }
      {
        name: 'TAG_VALUE'
        value: tagValue
      }
    ]
  }
}
