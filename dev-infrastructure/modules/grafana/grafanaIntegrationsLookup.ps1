param (
    [string]$grafanaResourceGroup,
    [string]$grafanaName
)

$ErrorActionPreference = 'Stop'

try {
    Write-Output "running"
    $c = Get-AzContext -ErrorAction stop
    if ($c)
    {
        Write-Output "we have azure context"
        # Ensure the Az.ResourceGraph module is available
        if (-not (Get-Module -ListAvailable -Name Az.ResourceGraph)) {
            Write-Output "Az.ResourceGraph module not found. Installing..."
            Install-Module -Name Az.ResourceGraph -Force -Scope CurrentUser
            Import-Module Az.ResourceGraph
        }

        # Query Azure Resource Graph to check if Grafana exists
        $query = @"
        resources
        | where type == 'microsoft.dashboard/grafana'
        | where name == '$grafanaName'
        | where resourceGroup == '$grafanaResourceGroup'
        | project grafanaIntegrations = properties.grafanaIntegrations.azureMonitorWorkspaceIntegrations

"@

        $result = Search-AzGraph -Query $query

        if (-not $result) {
            # Grafana does not exist, return empty array
            Write-Output "No Grafana integrations found or Grafana does not exist."
            $output = @()
        } else {
            # Extract workspace IDs from the azureMonitorWorkspaceIntegrations list
            Write-Output "Grafana integrations found: $($result | ConvertTo-Json -Depth 10)"
            $output = @()
            foreach ($item in $result.grafanaIntegrations) {
                if ($item.azureMonitorWorkspaceResourceId) {
                    $output += $item.azureMonitorWorkspaceResourceId
                }
            }
        }

        # Ensure Bicep can process the output, setting JSON depth to prevent truncation
        $DeploymentScriptOutputs = @{
            workspaceIds = $output
        }

    }
    else
    {
        Write-Output "no context"
        throw 'Cannot get a context'
    }
}
catch {
    Write-Error $_
    exit 1
}
