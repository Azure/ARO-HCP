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

        # Try to use SemanticVersion (PowerShell 6+)
        $useSemanticVersion = $true
        $parsedVersions = @()

        foreach ($ver in $versions) {
            try {
                $semVer = [System.Management.Automation.SemanticVersion]::new($ver)
                $parsedVersions += [PSCustomObject]@{
                    Original = $ver
                    SemVer = $semVer
                }
            }
            catch {
                # SemanticVersion not available or version string incompatible
                $useSemanticVersion = $false
                break
            }
        }

        if ($useSemanticVersion -and $parsedVersions.Count -gt 0) {
            # Use SemanticVersion sorting (PowerShell 6+)
            $latestVersion = $parsedVersions | Sort-Object -Property SemVer | Select-Object -Last 1 -ExpandProperty Original
        }
        else {
            # Fallback: Custom semver-aware sorting for PowerShell 5.1
            # Properly handles major.minor.patch and pre-release identifiers
            $latestVersion = $versions | Sort-Object -Property {
                $v = $_

                # Parse version: separate base version from pre-release/build metadata
                if ($v -match '^(\d+)\.(\d+)\.(\d+)(?:-(.+?))?(?:\+(.+))?$') {
                    $major = [int]$matches[1]
                    $minor = [int]$matches[2]
                    $patch = [int]$matches[3]
                    $prerelease = $matches[4]  # e.g., "0.nightly-2024-05-26"

                    # Sort key: major.minor.patch as padded numbers, then pre-release
                    # Versions without pre-release come AFTER pre-release (semver rule)
                    $sortKey = "{0:D10}.{1:D10}.{2:D10}" -f $major, $minor, $patch

                    if ($prerelease) {
                        # Has pre-release: append "0" + prerelease to sort before release
                        $sortKey += ".0.$prerelease"
                    }
                    else {
                        # No pre-release: append "1" to sort after pre-release versions
                        $sortKey += ".1"
                    }

                    return $sortKey
                }
                else {
                    # Fallback for unexpected formats: use original string
                    Write-Warning "Version '$v' doesn't match expected semver format"
                    return "0000000000.0000000000.0000000000.9.$v"
                }
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
