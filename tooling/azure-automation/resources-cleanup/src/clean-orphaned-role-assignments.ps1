Param
(
  [Parameter (Mandatory= $false)]
  [System.Boolean] $dryRun = $false
)

Connect-AzAccount -Identity

Select-AzSubscription 1d3378d3-5a3f-4712-85a1-2485495dfc4b | Out-Null

$x = (Get-AzRoleAssignment |
    Where-Object DisplayName -eq "aro-hcp-engineering-App Developer" |
    Where-Object Scope -eq /subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b |
    Where-Object RoleDefinitionName -eq "Contributor").ObjectType

if ($x -ne "Group" ) { 
    Write-Error "Wrong value for Objecttype, perhaps missing Directory Reader permissions on identity or IDs changed"
    exit 1
}

if ($dryRun -eq "dry-run") {
    Write-Host "Running in dry-run, would delete these Role Assignments"
    Get-AzRoleAssignment | Where-Object ObjectType -eq "Unknown"
} else {
    Get-AzRoleAssignment | Where-Object ObjectType -eq "Unknown" | Remove-AzRoleAssignment
}
