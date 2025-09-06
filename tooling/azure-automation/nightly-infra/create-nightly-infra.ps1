param (
    [Parameter(Mandatory=$true)]
    [Hashtable] $subscriptions = @{
        "svc" = "0ef1ad54-9296-44cd-9600-5dc8e9a74034"
        "mgnt" = "e8c5a115-842d-4d7e-98ad-cfb2c50b209e"
        "global" = "974ebd46-8ad3-41e3-afef-7ef25fd5c371"
    },
    [Parameter(Mandatory=$true)]
    [string] $ManagedIdentityClientId,

    [Parameter(Mandatory=$true)]
    [string] $ContainerImage,

    [Parameter(Mandatory=$true)]
    [string] $EnvName
)

$resourceGroup = "hcp-dev-automation-account"
$location = "eastus"

Write-Output "[INFO] Logging in with Managed Identity..."
az login --identity --username $ManagedIdentityClientId | Out-Null


function Invoke-ContainerJob {
    param (
        [string] $subscriptionId,
        [string] $envType,          # svc, mgnt, global
        [string] $command           # deploy or delete
    )

    $jobName = "${command}-${envType}-${EnvName}"
    $resourceGroupName = "rg-${envType}-${EnvName}"


    Write-Output "[INFO] Switching to subscription $subscriptionId"
    az account set --subscription $subscriptionId

    Write-Output "[INFO] Launching $command container job: $jobName"

    az container create `
        --resource-group $resourceGroup `
        --name $jobName `
        --image $ContainerImage `
        --location $location `
        --restart-policy Never `
        --environment-variables `
            TARGET_SUBSCRIPTION_ID=$subscriptionId `
            ENV_NAME=$envType `
            RESOURCE_GROUP=$resourceGroupName `
        --command-line "/nightly-infra/$command.sh $resourceGroupName $envType $subscriptionId"
}


foreach ($envType in $subscriptions.Keys) {
    $subId = $subscriptions[$envType]
    Invoke-ContainerJob -subscriptionId $subId -envType $envType -command "delete"
}

Start-Sleep -Seconds 60

foreach ($envType in $subscriptions.Keys) {
    $subId = $subscriptions[$envType]
    Invoke-ContainerJob -subscriptionId $subId -envType $envType -command "deploy"
}

Write-Output "[SUCCESS] All jobs triggered across svc/mgnt/global subscriptions."