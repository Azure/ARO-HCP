# PowerShell script to set synchronized OpenShift versions for E2E tests
# This ensures control plane and node pool use the same version

param(
    [string]$ChannelGroup = "candidate",
    [string]$VersionMinor = "4.20"
)

Write-Host "=== Setting OpenShift Versions for E2E Tests ===" -ForegroundColor Cyan
Write-Host "Channel Group: $ChannelGroup"
Write-Host "Version Minor: $VersionMinor"
Write-Host ""

# Function to get latest version from OpenShift graph API
function Get-LatestOpenShiftVersion {
    param(
        [string]$Channel,
        [string]$Minor
    )

    $graphUrl = "https://api.openshift.com/api/upgrades_info/v1/graph?channel=${Channel}-${Minor}"

    try {
        Write-Host "Fetching latest version from Cincinnati..." -ForegroundColor Gray
        $response = Invoke-RestMethod -Uri $graphUrl -Method Get

        if ($response.nodes.Count -eq 0) {
            Write-Error "No versions found for channel ${Channel}-${Minor}"
            return $null
        }

        # Get the latest version using semantic version ordering
        $latestVersion = $response.nodes.version | Sort-Object { [version]$_ } | Select-Object -Last 1
        return $latestVersion
    }
    catch {
        Write-Error "Failed to fetch version: $_"
        return $null
    }
}

# Get the version
$version = Get-LatestOpenShiftVersion -Channel $ChannelGroup -Minor $VersionMinor

if (-not $version) {
    Write-Error "Failed to resolve version"
    exit 1
}

Write-Host "Resolved Version: $version" -ForegroundColor Green
Write-Host ""

# Set environment variables
Write-Host "Setting environment variables..." -ForegroundColor Gray
$env:ARO_HCP_OPENSHIFT_CHANNEL_GROUP = $ChannelGroup
$env:ARO_HCP_OPENSHIFT_NODEPOOL_CHANNEL_GROUP = $ChannelGroup
$env:ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION = $version
$env:ARO_HCP_OPENSHIFT_NODEPOOL_VERSION = $version

# Print what was set
Write-Host ""
Write-Host "✓ Environment variables set:" -ForegroundColor Green
Write-Host "  ARO_HCP_OPENSHIFT_CHANNEL_GROUP = $ChannelGroup"
Write-Host "  ARO_HCP_OPENSHIFT_NODEPOOL_CHANNEL_GROUP = $ChannelGroup"
Write-Host "  ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION = $version"
Write-Host "  ARO_HCP_OPENSHIFT_NODEPOOL_VERSION = $version"
Write-Host ""
Write-Host "These variables are set for this PowerShell session." -ForegroundColor Yellow
Write-Host "To persist them, add to your profile or use:" -ForegroundColor Yellow
Write-Host ""
Write-Host '[System.Environment]::SetEnvironmentVariable("ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION", "' + $version + '", "User")' -ForegroundColor Cyan
Write-Host '[System.Environment]::SetEnvironmentVariable("ARO_HCP_OPENSHIFT_NODEPOOL_VERSION", "' + $version + '", "User")' -ForegroundColor Cyan
