#!/usr/bin/env pwsh
<#
.SYNOPSIS
    Azure Automation runbook for service tag monitoring using managed identity.

.DESCRIPTION
    This runbook connects to Azure using the automation account's managed identity
    and runs the service tag monitoring script to push metrics to Azure Monitor.
#>

param(
    [string]$WorkspaceEndpoint,
    [string]$RuleId,
    [string]$StreamName,
    [string]$SubscriptionId,
    [string]$ManagedIdentityClientId
)

Write-Output "Starting service tag monitoring automation runbook..."
Write-Output "Workspace Endpoint: $WorkspaceEndpoint"
Write-Output "Rule ID: $RuleId"
Write-Output "Stream Name: $StreamName"
Write-Output "Subscription ID: $SubscriptionId"
Write-Output "Managed Identity Client ID: $ManagedIdentityClientId"

try {
    # Connect using user-assigned managed identity
    Write-Output "Connecting to Azure using user-assigned managed identity..."
    if ($ManagedIdentityClientId) {
        Write-Output "Using user-assigned managed identity with client ID: $ManagedIdentityClientId"
        $context = Connect-AzAccount -Identity -AccountId $ManagedIdentityClientId -ErrorAction Stop
    } else {
        Write-Output "Using system-assigned managed identity"
        $context = Connect-AzAccount -Identity -ErrorAction Stop
    }
    Write-Output "Successfully connected as: $($context.Context.Account.Id)"
    
    # Import required modules if not already available
    Write-Output "Importing required Azure modules..."
    Import-Module Az.Accounts -Force -ErrorAction Stop
    Import-Module Az.Network -Force -ErrorAction Stop
    Import-Module Az.Resources -Force -ErrorAction Stop
    
    # Get the specified subscription, current context subscription, or all accessible subscriptions
    if ($SubscriptionId) {
        Write-Output "Processing specific subscription: $SubscriptionId"
        $subscriptions = @(Get-AzSubscription -SubscriptionId $SubscriptionId -ErrorAction Stop)
        Write-Output "Target subscription: $($subscriptions[0].Name) ($($subscriptions[0].Id))"
    } else {
        # Use current subscription from context
        $currentContext = Get-AzContext
        if ($currentContext -and $currentContext.Subscription) {
            Write-Output "Using current subscription from Azure context: $($currentContext.Subscription.Id)"
            $subscriptions = @(Get-AzSubscription -SubscriptionId $currentContext.Subscription.Id -ErrorAction Stop)
            Write-Output "Target subscription: $($subscriptions[0].Name) ($($subscriptions[0].Id))"
        } else {
            Write-Output "No subscription specified and no current context - discovering all accessible subscriptions..."
            $subscriptions = Get-AzSubscription
            Write-Output "Found $($subscriptions.Count) accessible subscription(s)"
        }
    }
    
    # Collect all public IPs across all subscriptions
    $allPublicIPs = @()
    $subscriptionCounter = 0
    foreach ($subscription in $subscriptions) {
        $subscriptionCounter++
        Write-Output "[$subscriptionCounter/$($subscriptions.Count)] Processing subscription: $($subscription.Name) ($($subscription.Id))"
        
        try {
            Write-Output "  - Switching to subscription context..."
            Set-AzContext -SubscriptionId $subscription.Id | Out-Null
            Write-Output "  - Successfully switched to subscription: $($subscription.Name)"
            
            # Test permissions first
            Write-Output "  - Testing permissions by listing resource groups..."
            try {
                $resourceGroups = Get-AzResourceGroup -ErrorAction Stop
                Write-Output "  - SUCCESS: Found $($resourceGroups.Count) resource groups in subscription"
            }
            catch {
                Write-Warning "  - PERMISSION ERROR: Cannot list resource groups in subscription '$($subscription.Name)': $($_.Exception.Message)"
                continue
            }
            
            # Try to get all public IPs at subscription level
            Write-Output "  - Attempting to get all public IPs at subscription level..."
            try {
                $publicIPs = Get-AzPublicIpAddress -ErrorAction Stop
                Write-Output "  - SUCCESS: Found $($publicIPs.Count) public IPs at subscription level"
                
                $processedIPs = $publicIPs | ForEach-Object {
                    [PSCustomObject]@{
                        SubscriptionId = $subscription.Id
                        ResourceGroup = $_.ResourceGroupName
                        Name = $_.Name
                        IpAddress = $_.IpAddress
                        Location = $_.Location
                        ProvisioningState = $_.ProvisioningState
                        IpTags = $_.IpTags
                    }
                }
                $allPublicIPs += $processedIPs
                Write-Output "  - Added $($processedIPs.Count) IPs to collection"
            }
            catch {
                Write-Warning "  - PERMISSION ERROR: Cannot get public IPs at subscription level: $($_.Exception.Message)"
                Write-Output "  - Trying resource group by resource group approach..."
                
                $rgCounter = 0
                $rgPublicIPs = @()
                foreach ($rg in $resourceGroups) {
                    $rgCounter++
                    if ($rgCounter % 10 -eq 0) {
                        Write-Output "    - Processed $rgCounter/$($resourceGroups.Count) resource groups..."
                    }
                    
                    try {
                        $rgIPs = Get-AzPublicIpAddress -ResourceGroupName $rg.ResourceGroupName -ErrorAction Stop
                        if ($rgIPs.Count -gt 0) {
                            Write-Output "    - Found $($rgIPs.Count) public IPs in RG: $($rg.ResourceGroupName)"
                            $rgPublicIPs += $rgIPs
                        }
                    }
                    catch {
                        Write-Warning "    - PERMISSION ERROR in RG '$($rg.ResourceGroupName)': $($_.Exception.Message)"
                    }
                }
                
                if ($rgPublicIPs.Count -gt 0) {
                    $processedIPs = $rgPublicIPs | ForEach-Object {
                        [PSCustomObject]@{
                            SubscriptionId = $subscription.Id
                            ResourceGroup = $_.ResourceGroupName
                            Name = $_.Name
                            IpAddress = $_.IpAddress
                            Location = $_.Location
                            ProvisioningState = $_.ProvisioningState
                            IpTags = $_.IpTags
                        }
                    }
                    $allPublicIPs += $processedIPs
                    Write-Output "  - Added $($processedIPs.Count) IPs from resource groups"
                }
            }
            
            Write-Output "  - COMPLETED subscription '$($subscription.Name)' - Total IPs in this subscription: $(($allPublicIPs | Where-Object {$_.SubscriptionId -eq $subscription.Id}).Count)"
        }
        catch {
            Write-Warning "  - CRITICAL ERROR in subscription '$($subscription.Name)': $($_.Exception.Message)"
        }
        
        Write-Output "  - Running total IPs collected: $($allPublicIPs.Count)"
        Write-Output ""
    }
    
    Write-Output "Total public IPs found: $($allPublicIPs.Count)"
    
    # Group by subscription, region, and ip_tag
    Write-Output "Grouping IPs by subscription, region, and IP tags..."
    $groupedData = @{}
    $processedCount = 0
    
    foreach ($ip in $allPublicIPs) {
        $processedCount++
        if ($processedCount % 50 -eq 0) {
            Write-Output "  - Processed $processedCount/$($allPublicIPs.Count) IPs for grouping..."
        }
        
        $subscriptionId = $ip.SubscriptionId
        $location = $ip.Location
        $ipAddress = $ip.IpAddress
        
        if ($ip.IpTags -and $ip.IpTags.Count -gt 0) {
            Write-Output "  - IP $ipAddress has $($ip.IpTags.Count) tag(s)"
            foreach ($tag in $ip.IpTags) {
                $key = "$subscriptionId|$location|$($tag.IpTagType)|$($tag.Tag)"
                if (-not $groupedData.ContainsKey($key)) {
                    $groupedData[$key] = @()
                }
                $groupedData[$key] += $ipAddress
                Write-Output "    - Added to group: $key"
            }
        } else {
            # Handle IPs with no tags
            $key = "$subscriptionId|$location|None|None"
            if (-not $groupedData.ContainsKey($key)) {
                $groupedData[$key] = @()
            }
            $groupedData[$key] += $ipAddress
        }
    }
    
    Write-Output "Grouping complete - Created $($groupedData.Keys.Count) unique groupings"
    Write-Output "Breakdown by subscription:"
    $subscriptionSummary = @{}
    foreach ($key in $groupedData.Keys) {
        $parts = $key -split '\|'
        $subId = $parts[0]
        $ipCount = ($groupedData[$key] | Where-Object { $null -ne $_ -and $_ -ne "" }).Count
        if (-not $subscriptionSummary.ContainsKey($subId)) {
            $subscriptionSummary[$subId] = 0
        }
        $subscriptionSummary[$subId] += $ipCount
    }
    foreach ($subId in $subscriptionSummary.Keys) {
        Write-Output "  - Subscription ${subId}: $($subscriptionSummary[$subId]) IPs"
    }
    
    # Send metrics to Azure Monitor if parameters are provided
    if ($WorkspaceEndpoint -and $RuleId -and $StreamName) {
        Write-Output "Sending metrics to Azure Monitor Workspace..."
        
        # Get access token for Azure Monitor
        $accessToken = Get-AzAccessToken -ResourceUrl "https://monitor.azure.com/" -ErrorAction Stop
        $accessToken = $accessToken.Token
        
        # Convert grouped data to metrics
        $metricEntries = @()
        $timestamp = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
        
        foreach ($key in $groupedData.Keys) {
            $parts = $key -split '\|'
            $subscriptionId = $parts[0]
            $region = $parts[1]
            $ipTagType = $parts[2]
            $tagValue = $parts[3]
            $ipCount = ($groupedData[$key] | Where-Object { $null -ne $_ -and $_ -ne "" }).Count
            
            $metricEntry = @{
                Time = $timestamp
                Name = "azure_public_ip_tag_count"
                Value = $ipCount
                subscription = $subscriptionId
                region = $region
                ipTagType = $ipTagType
                tag = $tagValue
            }
            $metricEntries += $metricEntry
        }
        
        # Prepare the request
        $headers = @{
            "Authorization" = "Bearer $accessToken"
            "Content-Type" = "application/json"
        }
        
        $body = @{
            $StreamName = $metricEntries
        } | ConvertTo-Json -Depth 10
        
        $uri = "$WorkspaceEndpoint/dataCollectionRules/$RuleId/streams/$StreamName" + "?api-version=2023-01-01"
        
        # Send the request
        Invoke-RestMethod -Uri $uri -Method Post -Headers $headers -Body $body -ErrorAction Stop | Out-Null
        
        Write-Output "Successfully sent $($metricEntries.Count) metric entries to Azure Monitor"
    } else {
        Write-Output "Azure Monitor parameters not provided - skipping metrics upload"
    }
    
    Write-Output "Service tag monitoring completed successfully!"
}
catch {
    Write-Error "Error in service tag monitoring: $_"
    throw
}