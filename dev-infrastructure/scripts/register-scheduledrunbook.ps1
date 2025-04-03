<#
.SYNOPSIS
    Registers a scheduled runbook in an Azure Automation Account.
.DESCRIPTION
    This script verifies that the specified runbook exists and is published, that the given schedule exists,
    and then registers the scheduled runbook by linking the runbook to the schedule.
    If a job schedule for the runbook already exists, it is removed before the new one is created.
.PARAMETER ResourceGroupName
    The name of the resource group that contains the Automation Account.
.PARAMETER AutomationAccountName
    The name of the Azure Automation Account.
.PARAMETER RunbookName
    The name of the runbook to register.
.PARAMETER ScheduleName
    The name of the schedule to link to the runbook.
.EXAMPLE
    .\register-scheduledrunbook.ps1 -ResourceGroupName ${resourceGroupName} -AutomationAccountName ${automationAccountName} -RunbookName ${runbookName} -ScheduleName ${scheduleName}
#>

param(
    [Parameter(Mandatory = $true)]
    [string]$ResourceGroupName,

    [Parameter(Mandatory = $true)]
    [string]$AutomationAccountName,

    [Parameter(Mandatory = $true)]
    [string]$RunbookName,

    [Parameter(Mandatory = $true)]
    [string]$ScheduleName
)

$ErrorActionPreference = 'Stop'

$output = @{
    success = $false
    message = ''
}
Write-Output "##[PS Version] $($PSVersionTable.PSVersion)"
Write-Output "##[Az Modules] $(Get-Module Az* | Select Name,Version | Out-String)"

try {
    # Ensure required Az modules are installed and imported
    if (-not (Get-Module -ListAvailable -Name Az)) {
        Install-Module -Name Az -Scope CurrentUser -Force -AllowClobber
    }
    Import-Module Az

    if (-not (Get-Module -ListAvailable -Name Az.Automation)) {
        Install-Module -Name Az.Automation -Scope CurrentUser -Force -AllowClobber
    }
    Import-Module Az.Automation

    Write-Host "Automation Account: $AutomationAccountName"
    Write-Host "Runbook: $RunbookName"
    Write-Host "Schedule: $ScheduleName"
    Write-Host "Resource Group: $ResourceGroupName"

    # Validate that the runbook exists and is published
    $runbook = Get-AzAutomationRunbook -AutomationAccountName $AutomationAccountName `
                -ResourceGroupName $ResourceGroupName `
                -Name $RunbookName -ErrorAction SilentlyContinue
    if (-not $runbook) {
        throw "Runbook '$RunbookName' not found in Automation Account '$AutomationAccountName' within resource group '$ResourceGroupName'."
    }
    if ($runbook.State -ne "Published") {
        throw "Runbook '$RunbookName' is not published. Please publish the runbook before registering a schedule."
    }

    # Validate that the schedule exists
    $schedule = Get-AzAutomationSchedule -AutomationAccountName $AutomationAccountName `
                  -ResourceGroupName $ResourceGroupName `
                  -Name $ScheduleName -ErrorAction SilentlyContinue
    if (-not $schedule) {
        throw "Schedule '$ScheduleName' not found in Automation Account '$AutomationAccountName' within resource group '$ResourceGroupName'."
    }

    # Remove an existing job schedule (if any) for the runbook to avoid conflicts
    $existingJob = Get-AzAutomationScheduledRunbook -AutomationAccountName $AutomationAccountName `
                    -ResourceGroupName $ResourceGroupName `
                    -Name $RunbookName -ErrorAction SilentlyContinue
    if ($existingJob) {
        Write-Host "Existing scheduled runbook found for '$RunbookName'. Removing..."
        $existingJob | Unregister-AzAutomationScheduledRunbook -Force
    }

    # Register new schedule
    $jobSchedule = Register-AzAutomationScheduledRunbook -ResourceGroupName $ResourceGroupName `
                        -AutomationAccountName $AutomationAccountName `
                        -RunbookName $RunbookName `
                        -ScheduleName $ScheduleName

    $output.success = $true
    $output.message = "Successfully registered Runbook '$RunbookName' to Schedule '$ScheduleName'"
    $output.jobScheduleId = $jobSchedule.JobScheduleId
}
catch {
    $output.message = "ERROR: $_"
    $output.errorDetails = $_.Exception.Message
}

if (-not $output.success) {
    throw $output.message
}

Write-Output $output.message
exit 0