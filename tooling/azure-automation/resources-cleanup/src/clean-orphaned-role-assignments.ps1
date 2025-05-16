Param
(
  [Parameter (Mandatory= $false)]
  [System.Boolean] $dryRun = $false,
  [Parameter (Mandatory= $false)]
  [string] $SubscriptionId = "1d3378d3-5a3f-4712-85a1-2485495dfc4b",
  [Parameter (Mandatory= $false)]
  [string] $ManagedIdentityId
)

Connect-AzAccount -Identity -SubscriptionId $SubscriptionId -AccountId "4579fe55-83eb-45a5-ba5e-ca90ffadd763"

Set-AzContext -SubscriptionId $SubscriptionId

$x = (Get-AzRoleAssignment |
    Where-Object DisplayName -eq "aro-hcp-engineering-App Developer" |
    Where-Object Scope -eq /subscriptions/$SubscriptionId |
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
