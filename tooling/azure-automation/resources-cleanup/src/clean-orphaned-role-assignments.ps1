$dryRun = Get-AutomationVariable -Name dry_run
$dryRun = @('true','1','yes','y','on') -contains ($dryRun.ToString().Trim().ToLower())

if ($dryRun) { Write-Output "dry-run mode enabled, deletions actions will not be executed." }

$SubscriptionId = Get-AutomationVariable -Name subscription_id
$ManagedIdentityClientId = Get-AutomationVariable -Name client_id

Connect-AzAccount -Identity -SubscriptionId $SubscriptionId -AccountId $ManagedIdentityClientId

Set-AzContext -SubscriptionId $SubscriptionId

$x = (Get-AzRoleAssignment |
    Where-Object DisplayName -eq "aro-hcp-engineering-App Developer" |
    Where-Object Scope -eq /subscriptions/$SubscriptionId |
    Where-Object RoleDefinitionName -eq "Contributor").ObjectType

if ($x -ne "Group" ) {
    Write-Error "Wrong value for Objecttype, perhaps missing Directory Reader permissions on identity or IDs changed"
    exit 1
}

if ($dryRun) {
    Write-Output "The following orphaned role assignments would be deleted (dry-run mode, no changes will be made):"
    Get-AzRoleAssignment | Where-Object ObjectType -eq "Unknown"
} else {
    Get-AzRoleAssignment | Where-Object ObjectType -eq "Unknown" | Remove-AzRoleAssignment
}
