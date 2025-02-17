
Connect-AzAccount -Identity

Select-AzSubscription 1d3378d3-5a3f-4712-85a1-2485495dfc4b | Out-Null

Get-AzRoleAssignment | Where-Object ObjectType -eq "Unknown" | Remove-AzRoleAssignment

