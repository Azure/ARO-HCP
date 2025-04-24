<#
.SYNOPSIS
    Registers a scheduled runbook in an Azure Automation Account.
.DESCRIPTION
    This script verifies that the specified runbook exists (and is published) and that the given schedule exists.
    It then registers the runbook to the schedule. If an existing job schedule for the runbook is found,
    it is removed before creating a new one.
.PARAMETER ResourceGroupName
    The resource group containing the Automation Account.
.PARAMETER AutomationAccountName
    The name of the Azure Automation Account.
.PARAMETER RunbookName
    The name of the runbook to register.
.PARAMETER ScheduleName
    The name of the schedule to link to the runbook.
.PARAMETER Parameters
    Key vaule pairs of parameters
.EXAMPLE
    .\register-scheduledrunbook.ps1 -ResourceGroupName "myRG" `
        -AutomationAccountName "myAA" `
        -RunbookName "myRunbook" `
        -ScheduleName "dailySchedule"
        -Parameters @{"Parameter1"="Value1";"Parameter2"="Value2"}
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

    [Parameter(Mandatory = $false)]
    [object]$Parameters
)

$ErrorActionPreference = 'Stop'

$output = @{
    success = $false
    message = ''
}

try {
    Write-Verbose "PowerShell Version: $($PSVersionTable.PSVersion)"
    Write-Verbose "Loaded Az Modules: $(Get-Module Az.* | Select-Object Name, Version | Out-String)"

    Write-Verbose @"
Parameters:
    Automation Account: $AutomationAccountName
    Runbook:           $RunbookName
    Schedule:          $ScheduleName
    Resource Group:    $ResourceGroupName
    Parameters:        $Parameters
"@

    # Validate that the runbook exists and is published.
    $runbook = Get-AzAutomationRunbook -ResourceGroupName $ResourceGroupName `
                 -AutomationAccountName $AutomationAccountName `
                 -Name $RunbookName -ErrorAction Stop
    if ($runbook.State -ne "Published") {
        $warningMessage = "Runbook '$RunbookName' is not published - schedule registration skipped."
        $output.warnings += $warningMessage
        $output.skipped  = $true
        Write-Output $warningMessage
        exit 0
    }

    # Validate that the schedule exists.
    $schedule = Get-AzAutomationSchedule -ResourceGroupName $ResourceGroupName `
                  -AutomationAccountName $AutomationAccountName `
                  -Name $ScheduleName -ErrorAction SilentlyContinue
    if (-not $schedule) {
        throw "Schedule '$ScheduleName' not found in Automation Account '$AutomationAccountName' within resource group '$ResourceGroupName'."
    }

    # Check for an existing job schedule for the runbook.
    $existingJob = Get-AzAutomationScheduledRunbook -ResourceGroupName $ResourceGroupName `
                   -AutomationAccountName $AutomationAccountName `
                   -Name $RunbookName -ErrorAction SilentlyContinue
    if ($existingJob) {
        Write-Verbose "Removing existing scheduled runbook for '$RunbookName'..."
        $existingJob | Unregister-AzAutomationScheduledRunbook -Force -ErrorAction Stop
    }

    # Register the new schedule.
    Write-Verbose "Registering new schedule '$ScheduleName' for runbook '$RunbookName'..."
    $jobSchedule = Register-AzAutomationScheduledRunbook -ResourceGroupName $ResourceGroupName `
                   -AutomationAccountName $AutomationAccountName `
                   -RunbookName $RunbookName `
                   -ScheduleName $ScheduleName -ErrorAction Stop

    $output.success = $true
    $output.message = "Successfully registered '$RunbookName' to schedule '$ScheduleName' (JobScheduleId: $($jobSchedule.JobScheduleId))"
    $output.jobScheduleId = $jobSchedule.JobScheduleId
}
catch {
    $output.message = "FAILED: $($_.Exception.Message)"
    $output.errorDetails = $_.ScriptStackTrace
    Write-Error $output.message
    exit 1
}

Write-Output $output.message
exit 0