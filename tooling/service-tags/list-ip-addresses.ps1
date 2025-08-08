#!/usr/bin/env pwsh
<#
.SYNOPSIS
    Script to connect to Azure and list all public IP addresses with tag counts.

.DESCRIPTION
    This script connects to Azure, retrieves all public IP addresses across all accessible subscriptions,
    groups them by subscription, region, and IP tags, and outputs the count in Prometheus metric format.

.PARAMETER OutputFile
    Optional output file to save results to.

.PARAMETER WorkspaceEndpoint
    Azure Monitor Data Collection Endpoint URL (e.g., https://myendpoint-abc.eastus-1.ingest.monitor.azure.com)

.PARAMETER RuleId
    Data Collection Rule ID for Azure Monitor

.PARAMETER StreamName
    Data stream name for Azure Monitor

.EXAMPLE
    .\list-ip-addresses.ps1

.EXAMPLE
    .\list-ip-addresses.ps1 -OutputFile "results.txt"

.EXAMPLE
    .\list-ip-addresses.ps1 -WorkspaceEndpoint "https://myendpoint-abc.eastus-1.ingest.monitor.azure.com" -RuleId "dcr-12345678901234567890123456789012" -StreamName "Custom-MyMetrics_CL"
#>

param(
    [string]$OutputFile = $env:OUTPUT_FILE,
    [string]$WorkspaceEndpoint,
    [string]$RuleId,
    [string]$StreamName
)

Write-Host "Azure Public IP Tag Counter - Starting..." -ForegroundColor Cyan
Write-Host "=========================================" -ForegroundColor Cyan

# Suppress module import warnings globally
$WarningPreference = "SilentlyContinue"

# Import required modules
Write-Host "[1/4] Loading Azure PowerShell modules..." -ForegroundColor Yellow
try {
    Write-Host "  - Importing Az.Accounts..." -ForegroundColor Gray
    Import-Module Az.Accounts -Force -DisableNameChecking -WarningAction SilentlyContinue -ErrorAction Stop
    Write-Host "  - Importing Az.Network..." -ForegroundColor Gray
    Import-Module Az.Network -Force -DisableNameChecking -WarningAction SilentlyContinue -ErrorAction Stop
    Write-Host "  - Importing Az.Resources..." -ForegroundColor Gray
    Import-Module Az.Resources -Force -DisableNameChecking -WarningAction SilentlyContinue -ErrorAction Stop
    Write-Host "  [SUCCESS] All modules loaded successfully" -ForegroundColor Green
}
catch {
    Write-Error "Failed to import required Azure modules. Please install the Az PowerShell module: Install-Module -Name Az"
    exit 1
}

# Reset warning preference to show legitimate script warnings
$WarningPreference = "Continue"

function Get-AllSubscriptions {
    # Get all accessible subscriptions
    Write-Host "[2/4] Discovering Azure subscriptions..." -ForegroundColor Yellow
    try {
        $subscriptions = Get-AzSubscription
        Write-Host "  [SUCCESS] Found $($subscriptions.Count) accessible subscription(s)" -ForegroundColor Green
        return $subscriptions
    }
    catch {
        Write-Error "Failed to get subscriptions: $_"
        return @()
    }
}

function Get-PublicIPsInSubscription {
    param(
        [string]$SubscriptionId,
        [string]$SubscriptionName
    )

    # List all public IP addresses in a subscription
    try {
        Write-Host "    - Switching to subscription: $SubscriptionName" -ForegroundColor Gray
        Set-AzContext -SubscriptionId $SubscriptionId -ErrorAction Stop | Out-Null

        # Test permissions first
        Write-Host "    - Testing permissions..." -ForegroundColor Gray
        try {
            $resourceGroups = Get-AzResourceGroup -ErrorAction Stop
            Write-Host "    - Found $($resourceGroups.Count) resource groups" -ForegroundColor Gray
        }
        catch {
            Write-Warning "    [PERMISSION ISSUE] Cannot list resource groups in subscription '$SubscriptionName': $($_.Exception.Message)"
            return @()
        }

        Write-Host "    - Querying public IP addresses..." -ForegroundColor Gray
        $publicIPs = @()
        $permissionErrors = 0

        # Try to get all public IPs at subscription level first
        try {
            $allPublicIPs = Get-AzPublicIpAddress -ErrorAction Stop
            $publicIPs += $allPublicIPs
            Write-Host "    - Found $($allPublicIPs.Count) public IPs at subscription level" -ForegroundColor Gray
        }
        catch {
            Write-Warning "    [PERMISSION ISSUE] Cannot list public IPs at subscription level: $($_.Exception.Message)"

            # Fall back to checking each resource group individually
            Write-Host "    - Trying resource group by resource group..." -ForegroundColor Gray
            foreach ($rg in $resourceGroups) {
                try {
                    $rgPublicIPs = Get-AzPublicIpAddress -ResourceGroupName $rg.ResourceGroupName -ErrorAction Stop
                    if ($rgPublicIPs.Count -gt 0) {
                        Write-Host "    - Found $($rgPublicIPs.Count) public IPs in RG: $($rg.ResourceGroupName)" -ForegroundColor Gray
                        $publicIPs += $rgPublicIPs
                    }
                }
                catch {
                    $permissionErrors++
                    if ($permissionErrors -le 3) {
                        Write-Warning "    [PERMISSION ISSUE] Cannot access RG '$($rg.ResourceGroupName)': $($_.Exception.Message)"
                    }
                }
            }
            if ($permissionErrors -gt 3) {
                Write-Warning "    [INFO] ... and $($permissionErrors - 3) more resource groups with permission issues"
            }
        }


        $results = @()
        foreach ($pip in $publicIPs) {
            $ipInfo = [PSCustomObject]@{
                SubscriptionId = $SubscriptionId
                ResourceGroup = $pip.ResourceGroupName
                Name = $pip.Name
                IpAddress = $pip.IpAddress
                Location = $pip.Location
                ProvisioningState = $pip.ProvisioningState
                IpTags = $pip.IpTags
                Source = "PublicIP"
            }
            $results += $ipInfo
        }

        Write-Host "    [SUCCESS] Found $($results.Count) total public IP(s) in subscription '$SubscriptionName'" -ForegroundColor Green
        if ($permissionErrors -gt 0) {
            Write-Host "    [WARNING] Encountered $permissionErrors permission issues - some IPs may be missing" -ForegroundColor Yellow
        }
        return $results
    }
    catch {
        Write-Warning "Error in subscription ${SubscriptionId} ($SubscriptionName): $_"
        return @()
    }
}

function Send-MetricsToWorkspace {
    param(
        [string]$WorkspaceEndpoint,
        [string]$RuleId,
        [string]$StreamName,
        [hashtable]$GroupedData
    )

    try {
        Write-Host "[WORKSPACE] Sending metrics to Azure Monitor Workspace..." -ForegroundColor Yellow
        
        # Get access token for Azure Monitor
        try {
            $accessToken = Get-AzAccessToken -ResourceUrl "https://monitor.azure.com/" -ErrorAction Stop
            $accessToken = $accessToken.Token
        }
        catch {
            Write-Warning "  [ERROR] Failed to get access token for Azure Monitor: $($_.Exception.Message)"
            return
        }

        # Convert grouped data to Prometheus-style metrics
        $metricEntries = @()
        $timestamp = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
        
        foreach ($key in $GroupedData.Keys) {
            $parts = $key -split '\|'
            $subscriptionId = $parts[0]
            $region = $parts[1]
            $ipTagType = $parts[2]
            $tagValue = $parts[3]
            $ipCount = ($GroupedData[$key] | Where-Object { $null -ne $_ -and $_ -ne "" }).Count
            
            $metricEntry = @{
                Time = $timestamp
                Name = "azure_public_ip_tag_count"
                Value = $ipCount
                Labels = @{
                    subscription = $subscriptionId
                    region = $region
                    ipTagType = $ipTagType
                    tag = $tagValue
                }
            }
            $metricEntries += $metricEntry
        }

        if ($metricEntries.Count -eq 0) {
            Write-Host "  [INFO] No metrics to send to Azure Monitor Workspace" -ForegroundColor Gray
            return
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
        
        Write-Host "  [SUCCESS] Successfully sent $($metricEntries.Count) metric entries to Azure Monitor Workspace" -ForegroundColor Green
    }
    catch {
        Write-Warning "  [ERROR] Failed to send metrics to Azure Workspace: $($_.Exception.Message)"
        Write-Host "  [DEBUG] Endpoint: $WorkspaceEndpoint" -ForegroundColor Gray
        Write-Host "  [DEBUG] Rule ID: $RuleId" -ForegroundColor Gray
        Write-Host "  [DEBUG] Stream: $StreamName" -ForegroundColor Gray
    }
}

function Main {
    $startTime = Get-Date
    try {
        # Check if authenticated
        Write-Host "[2/4] Checking Azure authentication..." -ForegroundColor Yellow
        $context = Get-AzContext
        if (-not $context) {
            Write-Error "Not authenticated to Azure. Please run 'Connect-AzAccount' first."
            return 1
        }
        Write-Host "  [SUCCESS] Authenticated as: $($context.Account.Id)" -ForegroundColor Green
        Write-Host "  [SUCCESS] Active tenant: $($context.Tenant.Id)" -ForegroundColor Green

        # Get all subscriptions
        $subscriptions = Get-AllSubscriptions

        if ($subscriptions.Count -eq 0) {
            Write-Warning "No accessible subscriptions found."
            return 0
        }

        # Display subscription list
        Write-Host "`n  Subscriptions to process:" -ForegroundColor Cyan
        foreach ($sub in $subscriptions) {
            Write-Host "    - $($sub.Name) ($($sub.Id))" -ForegroundColor Gray
        }

        Write-Host "`n[3/4] Collecting public IP addresses from all subscriptions..." -ForegroundColor Yellow
        $allPublicIPs = @()
        $subscriptionCounter = 0

        foreach ($subscription in $subscriptions) {
            $subscriptionCounter++
            Write-Host "  Processing subscription $subscriptionCounter of $($subscriptions.Count): $($subscription.Name)" -ForegroundColor Cyan

            $publicIPs = Get-PublicIPsInSubscription -SubscriptionId $subscription.Id -SubscriptionName $subscription.Name
            $allPublicIPs += $publicIPs
        }

        Write-Host "`n  [SUMMARY] Collection Summary:" -ForegroundColor Cyan
        Write-Host "    - Total subscriptions processed: $($subscriptions.Count)" -ForegroundColor White
        Write-Host "    - Total public IPs found: $($allPublicIPs.Count)" -ForegroundColor White

        # Show per-subscription breakdown
        Write-Host "`n  [BREAKDOWN] IPs per subscription:" -ForegroundColor Cyan
        $subscriptionsWithIPs = $allPublicIPs | Group-Object SubscriptionId
        foreach ($sub in $subscriptions) {
            $subGroup = $subscriptionsWithIPs | Where-Object { $_.Name -eq $sub.Id }
            $ipCount = if ($subGroup) { $subGroup.Count } else { 0 }
            $color = if ($ipCount -gt 0) { "Green" } else { "Gray" }
            Write-Host "    - $($sub.Name): $ipCount IPs" -ForegroundColor $color
        }

        # Group by subscription, region, and ip_tag
        if ($allPublicIPs.Count -gt 0) {
            Write-Host "`n[4/4] Analyzing and grouping IP addresses by tags..." -ForegroundColor Yellow
            $groupedData = @{}
            $processedIPs = 0
            $taggedIPs = 0
            $untaggedIPs = 0

            foreach ($ip in $allPublicIPs) {
                $processedIPs++
                $subscriptionId = $ip.SubscriptionId
                $location = $ip.Location
                $ipAddress = $ip.IpAddress

                if ($ip.IpTags -and $ip.IpTags.Count -gt 0) {
                    $taggedIPs++
                    foreach ($tag in $ip.IpTags) {
                        $key = "$subscriptionId|$location|$($tag.IpTagType)|$($tag.Tag)"

                        if (-not $groupedData.ContainsKey($key)) {
                            $groupedData[$key] = @()
                        }
                        $groupedData[$key] += $ipAddress
                    }
                } else {
                    $untaggedIPs++
                    # Handle IPs with no tags
                    $key = "$subscriptionId|$location|None|None"
                    if (-not $groupedData.ContainsKey($key)) {
                        $groupedData[$key] = @()
                    }
                    $groupedData[$key] += $ipAddress
                }

                # Show progress for large datasets
                if ($processedIPs % 50 -eq 0 -and $allPublicIPs.Count -gt 100) {
                    Write-Host "    - Processed $processedIPs of $($allPublicIPs.Count) IPs..." -ForegroundColor Gray
                }
            }

            Write-Host "  [SUCCESS] Analysis complete" -ForegroundColor Green
            Write-Host "`n  [SUMMARY] Tagging Summary:" -ForegroundColor Cyan
            Write-Host "    - IPs with tags: $taggedIPs" -ForegroundColor White
            Write-Host "    - IPs without tags: $untaggedIPs" -ForegroundColor White
            Write-Host "    - Unique groupings: $($groupedData.Keys.Count)" -ForegroundColor White

            # Output in Prometheus format
            Write-Host "`n[OUTPUT] Generating Prometheus metrics..." -ForegroundColor Cyan
            $output = @()
            $metricsGenerated = 0

            foreach ($key in $groupedData.Keys | Sort-Object) {
                $parts = $key -split '\|'
                $subscriptionId = $parts[0]
                $region = $parts[1]
                $ipTagType = $parts[2]
                $tagValue = $parts[3]
                $ipCount = ($groupedData[$key] | Where-Object { $null -ne $_ -and $_ -ne "" }).Count

                $line = "azure_public_ip_tag_count{subscription=`"$subscriptionId`",region=`"$region`",ipTagType=`"$ipTagType`",tag=`"$tagValue`"} $ipCount"
                $output += $line
                $metricsGenerated++
                Write-Output $line
            }

            Write-Host "`n[SUCCESS] Process completed successfully!" -ForegroundColor Green
            Write-Host "  [INFO] Generated $metricsGenerated Prometheus metrics" -ForegroundColor White

            # Optionally save to file
            if ($OutputFile) {
                $output | Out-File -FilePath $OutputFile -Encoding utf8
                Write-Host "  [INFO] Results saved to: $OutputFile" -ForegroundColor Yellow
            }

            # Send metrics to Azure Workspace if configured
            if ($WorkspaceEndpoint -and $RuleId -and $StreamName) {
                Send-MetricsToWorkspace -WorkspaceEndpoint $WorkspaceEndpoint -RuleId $RuleId -StreamName $StreamName -GroupedData $groupedData
            } elseif ($WorkspaceEndpoint -or $RuleId -or $StreamName) {
                Write-Warning "[WARNING] To send metrics to Azure Workspace, all three parameters are required: -WorkspaceEndpoint, -RuleId, and -StreamName"
            }
        } else {
            Write-Host "`n[WARNING] No public IP addresses found across all subscriptions." -ForegroundColor Yellow
        }

        # Final summary
        $endTime = Get-Date
        $duration = $endTime - $startTime
        Write-Host "`n=========================================" -ForegroundColor Cyan
        Write-Host "[FINAL] EXECUTION SUMMARY" -ForegroundColor Cyan
        Write-Host "=========================================" -ForegroundColor Cyan
        Write-Host "Total execution time: $($duration.TotalSeconds.ToString("F2")) seconds" -ForegroundColor White
        Write-Host "Subscriptions processed: $($subscriptions.Count)" -ForegroundColor White
        Write-Host "Total public IPs found: $($allPublicIPs.Count)" -ForegroundColor White
        if ($allPublicIPs.Count -gt 0) {
            Write-Host "Prometheus metrics generated: $metricsGenerated" -ForegroundColor White
            Write-Host "Average IPs per subscription: $([math]::Round($allPublicIPs.Count / $subscriptions.Count, 2))" -ForegroundColor White
        }
        Write-Host "=========================================" -ForegroundColor Cyan
    }
    catch {
        $endTime = Get-Date
        $duration = $endTime - $startTime
        Write-Host "`n[ERROR] Error occurred after $($duration.TotalSeconds.ToString("F2")) seconds" -ForegroundColor Red
        Write-Error "Error: $_"
        return 1
    }

    return 0
}

# Execute main function
exit (Main)