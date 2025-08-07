param name string
param location string = resourceGroup().location
param currentTime string = utcNow()
param consoleCallbackUrl string // e.g., https://console-openshift-console.apps.example.com/auth/callback

resource script 'Microsoft.Resources/deploymentScripts@2019-10-01-preview' = {
  name: name
  location: location
  kind: 'AzurePowerShell'
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${resourceId("app-reg-automation", "Microsoft.ManagedIdentity/userAssignedIdentities", "AppRegCreator")}': {}
    }
  }
  properties: {
    azPowerShellVersion: '5.0'
    arguments: '-resourceName "${name}" -consoleCallback "${consoleCallbackUrl}"'
    scriptContent: '''
      param([string] $resourceName, [string] $consoleCallback)

      $token = (Get-AzAccessToken -ResourceUrl https://graph.microsoft.com).Token
      $headers = @{'Content-Type' = 'application/json'; 'Authorization' = 'Bearer ' + $token}

      $template = @{
        displayName = $resourceName
        signInAudience = "AzureADMyOrg"
        web = @{
          redirectUris = @($consoleCallback)
        }
        requiredResourceAccess = @(
          @{
            resourceAppId = "00000003-0000-0000-c000-000000000000"
            resourceAccess = @(
              @{
                id = "e1fe6dd8-ba31-4d61-89e7-88639da4683d"
                type = "Scope"
              }
            )
          }
        )
      }

      # Upsert App registration
      $app = (Invoke-RestMethod -Method Get -Headers $headers -Uri "https://graph.microsoft.com/beta/applications?filter=displayName eq '$($resourceName)'").value
      $principal = @{}
      if ($app) {
        $ignore = Invoke-RestMethod -Method Patch -Headers $headers -Uri "https://graph.microsoft.com/beta/applications/$($app.id)" -Body ($template | ConvertTo-Json -Depth 10)
        $principal = (Invoke-RestMethod -Method Get -Headers $headers -Uri "https://graph.microsoft.com/beta/servicePrincipals?filter=appId eq '$($app.appId)'").value
      } else {
        $app = (Invoke-RestMethod -Method Post -Headers $headers -Uri "https://graph.microsoft.com/beta/applications" -Body ($template | ConvertTo-Json -Depth 10))
        $principal = Invoke-RestMethod -Method POST -Headers $headers -Uri  "https://graph.microsoft.com/beta/servicePrincipals" -Body (@{ "appId" = $app.appId } | ConvertTo-Json)
      }

      # Regenerate client secret
      $app = (Invoke-RestMethod -Method Get -Headers $headers -Uri "https://graph.microsoft.com/beta/applications/$($app.id)")
      foreach ($password in $app.passwordCredentials) {
        $body = @{ "keyId" = $password.keyId }
        $ignore = Invoke-RestMethod -Method POST -Headers $headers -Uri "https://graph.microsoft.com/beta/applications/$($app.id)/removePassword" -Body ($body | ConvertTo-Json)
      }

      $body = @{ "passwordCredential" = @{ "displayName"= "Client Secret" } }
      $secret = (Invoke-RestMethod -Method POST -Headers $headers -Uri  "https://graph.microsoft.com/beta/applications/$($app.id)/addPassword" -Body ($body | ConvertTo-Json)).secretText

      $DeploymentScriptOutputs = @{
        objectId = $app.id
        clientId = $app.appId
        clientSecret = $secret
        principalId = $principal.id
        redirectUri = $consoleCallback
      }
    '''
    cleanupPreference: 'OnSuccess'
    retentionInterval: 'P1D'
    forceUpdateTag: currentTime
  }
}

output objectId string = script.properties.outputs.objectId
output clientId string = script.properties.outputs.clientId
output clientSecret string = script.properties.outputs.clientSecret
output principalId string = script.properties.outputs.principalId
output redirectUri string = script.properties.outputs.redirectUri
