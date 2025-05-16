@description('Azure Automation Account Location')
param location string = resourceGroup().location

@description('Name of the Automation account to be created')
param automationAccountName string = 'hcp-dev-automation'

module automationAccount '../modules/automation-account/account.bicep' = {
  name: 'hcp-dev-automation'
  params: {
    automationAccountName: automationAccountName
    automationAccountManagedIdentity: 'hcp-dev-automation'
    location: location
    python3Packages: [
      {
        name: 'azure_common'
        url: 'https://files.pythonhosted.org/packages/62/55/7f118b9c1b23ec15ca05d15a578d8207aa1706bc6f7c87218efffbbf875d/azure_common-1.1.28-py2.py3-none-any.whl'
        algorithm: 'sha256'
        hash: '5c12d3dcf4ec20599ca6b0d3e09e86e146353d443e7fcc050c9a19c1f9df20ad'
      }
      {
        name: 'azure_core'
        url: 'https://files.pythonhosted.org/packages/ff/29/dbc7182bc207530c7b5858d59f429158465f878845d64a038afc1aa61e35/azure_core-1.29.7-py3-none-any.whl'
        algorithm: 'sha256'
        hash: '95a7b41b4af102e5fcdfac9500fcc82ff86e936c7145a099b7848b9ac0501250'
      }
      {
        name: 'azure_identity'
        url: 'https://files.pythonhosted.org/packages/30/10/5dbf755b368d10a28d55b06ac1f12512a13e88874a23db82defdea9a8cd9/azure_identity-1.15.0-py3-none-any.whl'
        algorithm: 'sha256'
        hash: 'a14b1f01c7036f11f148f22cd8c16e05035293d714458d6b44ddf534d93eb912'
      }
      {
        name: 'azure_mgmt_core'
        url: 'https://files.pythonhosted.org/packages/b1/5a/3a31578b840600dffb75f3ffb383cc4c5e8ea0d06a1085f86b17e18c3193/azure_mgmt_core-1.4.0-py3-none-any.whl'
        algorithm: 'sha256'
        hash: '81071675f186a585555ef01816f2774d49c1c9024cb76e5720c3c0f6b337bb7d'
      }
      {
        name: 'azure_mgmt_resource'
        url: 'https://files.pythonhosted.org/packages/40/14/9e0ffa0b24958081416005b49a7d903c1c12712accdd2cf9ebad7b3b41ee/azure_mgmt_resource-23.0.1-py3-none-any.whl'
        algorithm: 'sha256'
        hash: 'f185eec72bbc39f42bcb83ae6f1bad744f0e3f20a12d9b2b3e70d16c74ad9cc0'
      }
      {
        name: 'msal'
        url: 'https://files.pythonhosted.org/packages/b7/61/2756b963e84db6946e4b93a8e288595106286fc11c7129fcb869267ead67/msal-1.26.0-py2.py3-none-any.whl'
        algorithm: 'sha256'
        hash: 'be77ba6a8f49c9ff598bbcdc5dfcf1c9842f3044300109af738e8c3e371065b5'
      }
      {
        name: 'typing_extensions'
        url: 'https://files.pythonhosted.org/packages/b7/f4/6a90020cd2d93349b442bfcb657d0dc91eee65491600b2cb1d388bc98e6b/typing_extensions-4.9.0-py3-none-any.whl'
        algorithm: 'sha256'
        hash: 'af72aea155e91adfc61c3ae9e0e342dbc0cba726d6cba4b6c72c1f34e47291cd'
      }
    ]
  }
}

module permissions '../modules/automation-account/permissions.bicep' = {
  name: 'hcp-dev-automation-permissions'
  scope: subscription()
  params: {
    automationAccountManagedIdentityId: automationAccount.outputs.automationAccountManagedIdentityId
    automationAccountName: automationAccountName
  }
}

module resouceCleanup '../modules/automation-account/runbook.bicep' = {
  name: 'resourceCleanup'
  params: {
    automationAccountName: automationAccountName
    runbookDescription: 'Clean up old resource groups'
    runbookName: 'resourceCleanup'
    runbookType: 'Python3'
    runbookVersion: '1.0.0'
    location: location
    rubookScript: {
      ref: 'b89e85d56040a2ae807d92ec7e904cd5e792b3ea'
      path: 'tooling/azure-automation/resources-cleanup/src/resources_cleanup.py'
    }
    scheduleName: 'nightly-schedule'
    subscriptionId: subscription().subscriptionId
    managedIdentityId: automationAccount.outputs.automationAccountManagedIdentityId
  }
}

module roleAssignmentsCleanup '../modules/automation-account/runbook.bicep' = {
  name: 'roleAssignmentsCleanup'
  params: {
    automationAccountName: automationAccountName
    runbookDescription: 'Clean up orphaned role assignments'
    runbookName: 'roleAssignmentsCleanup'
    runbookType: 'PowerShell'
    runbookVersion: '1.0.0'
    location: location
    rubookScript: {
      ref: 'b89e85d56040a2ae807d92ec7e904cd5e792b3ea'
      path: 'tooling/azure-automation/resources-cleanup/src/clean-orphaned-role-assignments.ps1'
    }
    scheduleName: 'nightly-schedule'
    subscriptionId: subscription().subscriptionId
    managedIdentityId: automationAccount.outputs.automationAccountManagedIdentityId
  }
}
