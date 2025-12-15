@description('Azure Automation Account Location')
param location string = resourceGroup().location

@description('Environment short name, defaults to <dev>')
param environment string = 'dev'

@description('Automation account dry run mode')
param automationDryRun bool = true

@description('Name of the Automation account to be created')
param automationAccountName string = 'hcp-${environment}-automation'

@description('The start time for the nightly schedule')
param dailyScheduleStartTime string = '${substring(dateTimeAdd(utcNow(), 'P1D'), 0, 10)}T06:00:00Z'

@description('The start time for the hourly schedule')
param hourlyScheduleStartTime string = '${substring(dateTimeAdd(utcNow(), 'P1D'), 0, 10)}T00:00:00Z'

@description('The commit hash of the script to use')
param scriptVersion string = '0de69144a537d9e5a032605a5fa82e863fc45a9e'

module automationAccount '../modules/automation-account/account.bicep' = {
  name: 'hcp-${environment}-automation'
  params: {
    dryRun: automationDryRun
    automationAccountName: automationAccountName
    automationAccountManagedIdentity: 'hcp-${environment}-automation'
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
        url: 'https://files.pythonhosted.org/packages/ef/c5/ca55106564d2044ab90614381368b3756690fb7e3ab04552e17f308e4e4f/azure_identity-1.16.1-py3-none-any.whl'
        algorithm: 'sha256'
        hash: '8fb07c25642cd4ac422559a8b50d3e77f73dcc2bbfaba419d06d6c9d7cff6726'
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
        url: 'https://files.pythonhosted.org/packages/40/41/646c00154efa437bf01b30444421285fb29ef624e86b2446e71eff50b7a9/msal-1.28.0-py3-none-any.whl'
        algorithm: 'sha256'
        hash: '3064f80221a21cd535ad8c3fafbb3a3582cd9c7e9af0bb789ae14f726a0ca99b'
      }
      {
        name: 'typing_extensions'
        url: 'https://files.pythonhosted.org/packages/b7/f4/6a90020cd2d93349b442bfcb657d0dc91eee65491600b2cb1d388bc98e6b/typing_extensions-4.9.0-py3-none-any.whl'
        algorithm: 'sha256'
        hash: 'af72aea155e91adfc61c3ae9e0e342dbc0cba726d6cba4b6c72c1f34e47291cd'
      }
      {
        name: 'cryptography'
        url: 'https://files.pythonhosted.org/packages/0e/16/a28ddf78ac6e7e3f25ebcef69ab15c2c6be5ff9743dd0709a69a4f968472/cryptography-43.0.3-cp37-abi3-manylinux_2_28_x86_64.whl'
        algorithm: 'sha256'
        hash: '74f57f24754fe349223792466a709f8e0c093205ff0dca557af51072ff47ab18'
      }
      {
        name: 'msal_extensions'
        url: 'https://files.pythonhosted.org/packages/2c/69/314d887a01599669fb330da14e5c6ff5f138609e322812a942a74ef9b765/msal_extensions-1.2.0-py3-none-any.whl'
        algorithm: 'sha256'
        hash: 'cf5ba83a2113fa6dc011a254a72f1c223c88d7dfad74cc30617c4679a417704d'
      }
      {
        name: 'cffi'
        url: 'https://files.pythonhosted.org/packages/8d/fb/4da72871d177d63649ac449aec2e8a29efe0274035880c7af59101ca2232/cffi-1.17.1-cp310-cp310-manylinux_2_17_x86_64.manylinux2014_x86_64.whl'
        algorithm: 'sha256'
        hash: '2bb1a08b8008b281856e5971307cc386a8e9c5b625ac297e853d36da6efe9c17'
      }
      {
        name: 'pycparser'
        url: 'https://files.pythonhosted.org/packages/13/a3/a812df4e2dd5696d1f351d58b8fe16a405b234ad2886a0dab9183fb78109/pycparser-2.22-py3-none-any.whl'
        algorithm: 'sha256'
        hash: 'c3702b6d3dd8c7abc1afa565d7e63d53a1d0bd86cdc24edd75470f4de499cfcc'
      }
      {
        name: 'portalocker'
        url: 'https://files.pythonhosted.org/packages/9b/fb/a70a4214956182e0d7a9099ab17d50bfcba1056188e9b14f35b9e2b62a0d/portalocker-2.10.1-py3-none-any.whl'
        algorithm: 'sha256'
        hash: '53a5984ebc86a025552264b459b46a2086e269b21823cb572f8f28ee759e45bf'
      }
      {
        name: 'isodate'
        url: 'https://files.pythonhosted.org/packages/15/aa/0aca39a37d3c7eb941ba736ede56d689e7be91cab5d9ca846bde3999eba6/isodate-0.7.2-py3-none-any.whl'
        algorithm: 'sha256'
        hash: '28009937d8031054830160fce6d409ed342816b543597cece116d966c6d99e15'
      }
      {
        name: 'requests'
        url: 'https://files.pythonhosted.org/packages/f9/9b/335f9764261e915ed497fcdeb11df5dfd6f7bf257d4a6a2a686d80da4d54/requests-2.32.3-py3-none-any.whl'
        algorithm: 'sha256'
        hash: '70761cfe03c773ceb22aa2f671b4757976145175cdfca038c02654d061d6dcc6'
      }
      {
        name: 'urllib3'
        url: 'https://files.pythonhosted.org/packages/ce/d9/5f4c13cecde62396b0d3fe530a50ccea91e7dfc1ccf0e09c228841bb5ba8/urllib3-2.2.3-py3-none-any.whl'
        algorithm: 'sha256'
        hash: 'ca899ca043dcb1bafa3e262d73aa25c465bfb49e0bd9dd5d59f1d0acba2f8fac'
      }
      {
        name: 'certifi'
        url: 'https://files.pythonhosted.org/packages/12/90/3c9ff0512038035f59d279fddeb79f5f1eccd8859f06d6163c58798b9487/certifi-2024.8.30-py3-none-any.whl'
        algorithm: 'sha256'
        hash: '922820b53db7a7257ffbda3f597266d435245903d80737e34f8a45ff3e3230d8'
      }
      {
        name: 'idna'
        url: 'https://files.pythonhosted.org/packages/76/c6/c88e154df9c4e1a2a66ccf0005a88dfb2650c1dffb6f5ce603dfbd452ce3/idna-3.10-py3-none-any.whl'
        algorithm: 'sha256'
        hash: '946d195a0d259cbba61165e88e65941f16e9b36ea6ddb97f00452bae8b1287d3'
      }
      {
        name: 'charset_normalizer'
        url: 'https://files.pythonhosted.org/packages/bf/9b/08c0432272d77b04803958a4598a51e2a4b51c06640af8b8f0f908c18bf2/charset_normalizer-3.4.0-py3-none-any.whl'
        algorithm: 'sha256'
        hash: 'fe9f97feb71aa9896b81973a7bbada8c49501dc73e58a10fcef6663af95e5079'
      }
      {
        name: 'PyJWT'
        url: 'https://files.pythonhosted.org/packages/79/84/0fdf9b18ba31d69877bd39c9cd6052b47f3761e9910c15de788e519f079f/PyJWT-2.9.0-py3-none-any.whl'
        algorithm: 'sha256'
        hash: '3b02fb0f44517787776cf48f2ae25d8e14f300e6d7545a4315cee571a415e850'
      }
    ]
  }
}

module permissions '../modules/automation-account/permissions.bicep' = {
  name: 'hcp-${environment}-automation-permissions'
  scope: subscription()
  params: {
    automationAccountName: automationAccount.outputs.name
    principalId: automationAccount.outputs.managedIdentityPrincipalId
  }
}

module resouceCleanup '../modules/automation-account/runbook.bicep' = {
  name: 'resourceCleanup'
  params: {
    automationAccountName: automationAccount.outputs.name
    runbookDescription: 'Clean up old resource groups'
    runbookName: 'resourceCleanup'
    runbookType: 'Python'
    runtimeEnvironment: automationAccount.outputs.customRuntimeName
    runbookVersion: '1.0.0'
    location: location
    runbookScript: {
      ref: scriptVersion
      path: 'tooling/azure-automation/resources-cleanup/src/resources_cleanup.py'
    }
    scheduleName: 'daily-schedule'
    startTime: dailyScheduleStartTime
  }
}

module roleAssignmentsCleanup '../modules/automation-account/runbook.bicep' = {
  name: 'roleAssignmentsCleanup'
  params: {
    automationAccountName: automationAccount.outputs.name
    runbookDescription: 'Clean up orphaned role assignments'
    runbookName: 'roleAssignmentsCleanup'
    runbookType: 'PowerShell'
    runtimeEnvironment: 'PowerShell-7.2'
    runbookVersion: '1.0.0'
    location: location
    runbookScript: {
      ref: scriptVersion
      path: 'tooling/azure-automation/resources-cleanup/src/clean-orphaned-role-assignments.ps1'
    }
    scheduleName: 'bihourly-schedule'
    startTime: hourlyScheduleStartTime
    frequency: 'Hour'
    interval: 2
  }
}
