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

        # Get the latest version using semantic version sorting
        # This handles pre-release versions like "4.20.0-0.nightly-..." correctly
        $versions = $response.nodes.version

        # Try to use SemanticVersion (PowerShell 6+), fall back to custom sorting
        try {
            $latestVersion = $versions | ForEach-Object {
                [PSCustomObject]@{
                    Original = $_
                    SemVer = [System.Management.Automation.SemanticVersion]::new($_)
                }
            } | Sort-Object -Property SemVer | Select-Object -Last 1 -ExpandProperty Original
        }
        catch {
            # Fallback for Windows PowerShell 5.1 or if SemanticVersion parsing fails
            # Sort by splitting version components and comparing numerically
            $latestVersion = $versions | Sort-Object -Property {
                $parts = $_ -split '[\.-]'
                # Pad each part to ensure numeric comparison
                ($parts[0].PadLeft(10, '0') +
                 $parts[1].PadLeft(10, '0') +
                 $parts[2].PadLeft(10, '0'))
            } | Select-Object -Last 1
        }

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
